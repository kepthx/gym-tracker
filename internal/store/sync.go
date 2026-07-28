package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kepthx/gym-tracker/internal/db"
)

// ErrBatchTooLarge — the request carries more operations than are allowed at once.
var ErrBatchTooLarge = errors.New("слишком много операций в батче")

// ApplyBatch applies a batch of operations in a single transaction.
//
// Three properties are the reason everything here is arranged this way:
//
//   - idempotence: redelivering the same batch does not change state (the applied_ops
//     ledger, plus the fact that every operation is itself either a full row upsert
//     or a monotone merge);
//   - order independence: reordering the operations, or splitting them into different
//     batches, yields the same final state;
//   - one broken operation does not sink the rest: it is rolled back to its own
//     savepoint, marked rejected, and does not jam the client queue forever.
//
// An error is returned only when the database fails — then the whole batch is rolled
// back, the client retries it in full, and the retry is safe thanks to the ledger.
func (s *Store) ApplyBatch(
	ctx context.Context,
	userID int64,
	deviceID string,
	ops []Op,
	now time.Time,
) ([]OpResult, error) {
	if len(ops) > MaxOpsPerBatch {
		return nil, fmt.Errorf("%w: %d при пределе %d", ErrBatchTooLarge, len(ops), MaxOpsPerBatch)
	}

	// The writer pool is opened with _txlock=immediate, so this starts a BEGIN IMMEDIATE:
	// the write lock is taken up front rather than escalated along the way.
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("начать транзакцию: %w", err)
	}
	defer tx.Rollback()

	results := make([]OpResult, 0, len(ops))
	for i := range ops {
		op := &ops[i]

		// A savepoint per operation: a rejection rolls back both its writes and its row
		// in the idempotence ledger, without touching its neighbours in the batch.
		if _, err := tx.ExecContext(ctx, `SAVEPOINT op`); err != nil {
			return nil, fmt.Errorf("создать точку сохранения: %w", err)
		}

		res, err := s.processOp(ctx, tx, userID, deviceID, op, now)
		if err != nil {
			return nil, err
		}
		if res.Status == StatusRejected {
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO op`); err != nil {
				return nil, fmt.Errorf("откатить операцию: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `RELEASE op`); err != nil {
			return nil, fmt.Errorf("снять точку сохранения: %w", err)
		}

		results = append(results, res)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("зафиксировать батч: %w", err)
	}

	// The backup runs AFTER the commit: otherwise it would capture the state without
	// this workout in it.
	if s.onSessionFinished != nil && finishedAny(ops, results) {
		s.onSessionFinished()
	}
	return results, nil
}

func finishedAny(ops []Op, results []OpResult) bool {
	applied := make(map[string]bool, len(results))
	for _, r := range results {
		applied[r.OpID] = r.Status == StatusApplied
	}
	for i := range ops {
		if ops[i].Type == OpSessionFinish && applied[ops[i].OpID] {
			return true
		}
	}
	return false
}

func (s *Store) processOp(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	deviceID string,
	op *Op,
	now time.Time,
) (OpResult, error) {
	if reason := op.validate(); reason != "" {
		return rejected(op, reason), nil
	}

	state, err := recordOp(ctx, tx, userID, op.OpID, now)
	if err != nil {
		return OpResult{}, err
	}
	switch state {
	case dupSame:
		return OpResult{OpID: op.OpID, Status: StatusDuplicate}, nil
	case dupOther:
		return rejected(op, "op_id уже использован другим пользователем"), nil
	}

	effTS := clampTS(op.TS, now)

	switch op.Type {
	case OpSessionStart:
		return s.applySessionStart(ctx, tx, userID, deviceID, op, effTS)
	case OpSetUpsert:
		return s.applySetUpsert(ctx, tx, userID, deviceID, op, effTS)
	case OpSessionFinish:
		return s.applySessionFinish(ctx, tx, userID, op)
	case OpSessionDelete:
		return s.applySessionDelete(ctx, tx, userID, op)
	default:
		// Unreachable: the type has already been checked in validate.
		return rejected(op, "неизвестный тип операции"), nil
	}
}

type dupState int

const (
	dupNone dupState = iota
	dupSame
	dupOther
)

// recordOp is the second layer of idempotence, on top of operations being idempotent
// in their own right.
//
// It costs a few lines, but it gives an honest per-operation status, answers the
// "did my last request make it?" question while debugging, and insures against the day
// an operation appears that is not idempotent by nature.
func recordOp(ctx context.Context, tx *sql.Tx, userID int64, opID string, now time.Time) (dupState, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO applied_ops(op_id, user_id, applied_at) VALUES (?, ?, ?)
		 ON CONFLICT(op_id) DO NOTHING`,
		opID, userID, now.UnixMilli())
	if err != nil {
		return dupNone, fmt.Errorf("записать операцию в журнал: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return dupNone, fmt.Errorf("проверить запись в журнал: %w", err)
	}
	if affected == 1 {
		return dupNone, nil
	}

	var owner int64
	if err := tx.QueryRowContext(ctx,
		`SELECT user_id FROM applied_ops WHERE op_id = ?`, opID).Scan(&owner); err != nil {
		return dupNone, fmt.Errorf("прочитать журнал операций: %w", err)
	}
	if owner != userID {
		return dupOther, nil
	}
	return dupSame, nil
}

func (s *Store) applySessionStart(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	deviceID string,
	op *Op,
	effTS int64,
) (OpResult, error) {
	res := OpResult{OpID: op.OpID, Status: StatusApplied}

	allowed, err := programAllowed(ctx, tx, userID, op.ProgramHash)
	if err != nil {
		return res, err
	}
	if !allowed {
		return rejected(op, "программа с таким хешем не принадлежит пользователю"), nil
	}

	var owner, curTS int64
	var curBy string
	err = tx.QueryRowContext(ctx,
		`SELECT user_id, updated_ts, updated_by FROM sessions WHERE id = ?`,
		op.SessionID).Scan(&owner, &curTS, &curBy)

	switch {
	case err == nil:
		if owner != userID {
			return rejected(op, "тренировка принадлежит другому пользователю"), nil
		}
		// The same workout started again by another operation: the start fields are
		// updated by last-write-wins. finished_at and deleted are left alone here —
		// otherwise a late replay of the start would resurrect a finished workout.
		if newer(effTS, deviceID, curTS, curBy) {
			rev, err := db.NextRev(ctx, tx)
			if err != nil {
				return res, err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE sessions SET date = ?, day_id = ?, program_hash = ?, started_at = ?,
				        updated_ts = ?, updated_by = ?, rev = ? WHERE id = ?`,
				op.Date, op.DayID, op.ProgramHash, op.StartedAt,
				effTS, deviceID, rev, op.SessionID); err != nil {
				return res, fmt.Errorf("обновить тренировку: %w", err)
			}
		}
		return res, nil

	case !errors.Is(err, sql.ErrNoRows):
		return res, fmt.Errorf("прочитать тренировку: %w", err)
	}

	// The workout does not exist yet. Check whether the single "open" slot is free.
	var openID string
	var openStarted int64
	err = tx.QueryRowContext(ctx,
		`SELECT id, started_at FROM sessions
		 WHERE user_id = ? AND finished_at IS NULL AND deleted = 0`,
		userID).Scan(&openID, &openStarted)

	var finishedAt sql.NullInt64
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// The slot is free, the workout is stored as open.

	case err != nil:
		return res, fmt.Errorf("найти открытую тренировку: %w", err)

	case startsLater(op.StartedAt, op.SessionID, openStarted, openID):
		// The new one started later — it stays open, and the previous one is closed at
		// its start time. The previous one's data goes nowhere: it just moves to history.
		rev, err := db.NextRev(ctx, tx)
		if err != nil {
			return res, err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE sessions SET finished_at = ?, rev = ? WHERE id = ?`,
			op.StartedAt, rev, openID); err != nil {
			return res, fmt.Errorf("закрыть предыдущую тренировку: %w", err)
		}
		res.Warning, res.ClosedSessionID = WarnAutoClosed, openID

	default:
		// The open one started later — it stays open, and the arriving one is stored
		// already closed. Same outcome as under the reverse delivery order.
		finishedAt = sql.NullInt64{Int64: openStarted, Valid: true}
		res.Warning, res.ClosedSessionID = WarnAutoClosed, op.SessionID
	}

	rev, err := db.NextRev(ctx, tx)
	if err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions(id, user_id, date, day_id, program_hash, started_at,
		                      finished_at, updated_ts, updated_by, rev)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.SessionID, userID, op.Date, op.DayID, op.ProgramHash, op.StartedAt,
		finishedAt, effTS, deviceID, rev); err != nil {
		return res, fmt.Errorf("создать тренировку: %w", err)
	}

	if err := clampOverlappingFinishes(ctx, tx, userID, op.SessionID, op.StartedAt); err != nil {
		return res, err
	}
	return res, nil
}

// clampOverlappingFinishes pulls in the finish time of workouts that, on the record,
// were still "running" after this one started.
//
// A workout cannot continue past the start of the next one. The rule is not only there
// for common sense: without it an explicit finish and an auto-close would produce
// different results depending on delivery order. Auto-close only fires on an OPEN
// workout, so "finished explicitly first, then the next start arrived" would not agree
// with the reverse order. This is where that is levelled out.
//
// The rule only shortens an already recorded finish time; it never deletes anything and
// never reopens anything.
func clampOverlappingFinishes(ctx context.Context, tx *sql.Tx, userID int64, sessionID string, startedAt int64) error {
	const overlapping = `user_id = ? AND id != ? AND deleted = 0
	                     AND finished_at IS NOT NULL AND started_at <= ? AND finished_at > ?`

	var needed bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM sessions WHERE `+overlapping+`)`,
		userID, sessionID, startedAt, startedAt).Scan(&needed); err != nil {
		return fmt.Errorf("найти пересекающиеся тренировки: %w", err)
	}
	if !needed {
		return nil
	}

	rev, err := db.NextRev(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET finished_at = ?, rev = ? WHERE `+overlapping,
		startedAt, rev, userID, sessionID, startedAt, startedAt); err != nil {
		return fmt.Errorf("подтянуть время завершения: %w", err)
	}
	return nil
}

func (s *Store) applySetUpsert(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	deviceID string,
	op *Op,
	effTS int64,
) (OpResult, error) {
	res := OpResult{OpID: op.OpID, Status: StatusApplied}

	owner, err := sessionOwner(ctx, tx, op.SessionID)
	if errors.Is(err, ErrNotFound) {
		return rejected(op, "нет такой тренировки"), nil
	}
	if err != nil {
		return res, err
	}
	if owner != userID {
		return rejected(op, "тренировка принадлежит другому пользователю"), nil
	}

	var curTS int64
	var curBy string
	err = tx.QueryRowContext(ctx,
		`SELECT updated_ts, updated_by FROM sets
		 WHERE session_id = ? AND exercise_id = ? AND idx = ?`,
		op.SessionID, op.ExerciseID, *op.Idx).Scan(&curTS, &curBy)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// No row for this set yet: rows are created on first touch, not in bulk at start.
	case err != nil:
		return res, fmt.Errorf("прочитать подход: %w", err)
	case !newer(effTS, deviceID, curTS, curBy):
		// The write lost last-write-wins. The operation is handled, the row is unchanged.
		return res, nil
	}

	rev, err := db.NextRev(ctx, tx)
	if err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sets(session_id, exercise_id, idx, done, weight, reps,
		                  updated_ts, updated_by, rev)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id, exercise_id, idx) DO UPDATE SET
		   done       = excluded.done,
		   weight     = excluded.weight,
		   reps       = excluded.reps,
		   updated_ts = excluded.updated_ts,
		   updated_by = excluded.updated_by,
		   rev        = excluded.rev`,
		op.SessionID, op.ExerciseID, *op.Idx, boolToInt(*op.Done), op.Weight, op.Reps,
		effTS, deviceID, rev); err != nil {
		return res, fmt.Errorf("сохранить подход: %w", err)
	}
	return res, nil
}

func (s *Store) applySessionFinish(ctx context.Context, tx *sql.Tx, userID int64, op *Op) (OpResult, error) {
	res := OpResult{OpID: op.OpID, Status: StatusApplied}

	var owner, startedAt int64
	var finishedAt sql.NullInt64
	err := tx.QueryRowContext(ctx,
		`SELECT user_id, started_at, finished_at FROM sessions WHERE id = ?`,
		op.SessionID).Scan(&owner, &startedAt, &finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rejected(op, "нет такой тренировки"), nil
	}
	if err != nil {
		return res, fmt.Errorf("прочитать тренировку: %w", err)
	}
	if owner != userID {
		return rejected(op, "тренировка принадлежит другому пользователю"), nil
	}

	// The same pair of rules as in clampOverlappingFinishes, approached from the other
	// side: a workout cannot finish later than the next one starts, nor earlier than its
	// own start. Together these two places make the outcome delivery-order independent.
	finish := op.FinishedAt
	var earliestNext sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MIN(started_at) FROM sessions
		 WHERE user_id = ? AND id != ? AND deleted = 0 AND started_at > ?`,
		userID, op.SessionID, startedAt).Scan(&earliestNext); err != nil {
		return res, fmt.Errorf("найти следующую тренировку: %w", err)
	}
	if earliestNext.Valid && earliestNext.Int64 < finish {
		finish = earliestNext.Int64
	}
	if finish < startedAt {
		finish = startedAt
	}

	// Monotone merge: of all proposed finish times the smallest one survives. The
	// operation is commutative, so a replay and any order give the same result.
	if finishedAt.Valid && finishedAt.Int64 <= finish {
		return res, nil
	}

	rev, err := db.NextRev(ctx, tx)
	if err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET finished_at = ?, rev = ? WHERE id = ?`,
		finish, rev, op.SessionID); err != nil {
		return res, fmt.Errorf("завершить тренировку: %w", err)
	}
	return res, nil
}

func (s *Store) applySessionDelete(ctx context.Context, tx *sql.Tx, userID int64, op *Op) (OpResult, error) {
	res := OpResult{OpID: op.OpID, Status: StatusApplied}

	var owner int64
	var deleted int
	err := tx.QueryRowContext(ctx,
		`SELECT user_id, deleted FROM sessions WHERE id = ?`,
		op.SessionID).Scan(&owner, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return rejected(op, "нет такой тренировки"), nil
	}
	if err != nil {
		return res, fmt.Errorf("прочитать тренировку: %w", err)
	}
	if owner != userID {
		return rejected(op, "тренировка принадлежит другому пользователю"), nil
	}
	// The tombstone is monotone: a workout deleted once never comes back, so a replay
	// and any delivery order give the same result.
	if deleted == 1 {
		return res, nil
	}

	rev, err := db.NextRev(ctx, tx)
	if err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET deleted = 1, rev = ? WHERE id = ?`,
		rev, op.SessionID); err != nil {
		return res, fmt.Errorf("удалить тренировку: %w", err)
	}
	return res, nil
}

// programAllowed checks that a program hash belongs to this user: either it is their
// current program, or a program they have already trained by.
//
// A client may start a workout offline against a program that has been replaced in the
// meantime — such a workout is recorded under the old hash, because history has to show
// what the person actually did, not what became current afterwards.
func programAllowed(ctx context.Context, tx *sql.Tx, userID int64, hash string) (bool, error) {
	var ok bool
	err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM users    WHERE id = ?      AND current_program_hash = ?)
		     OR EXISTS(SELECT 1 FROM sessions WHERE user_id = ? AND program_hash = ?)`,
		userID, hash, userID, hash).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("проверить программу: %w", err)
	}
	return ok, nil
}

func sessionOwner(ctx context.Context, tx *sql.Tx, sessionID string) (int64, error) {
	var owner int64
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE id = ?`, sessionID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("прочитать владельца тренировки: %w", err)
	}
	return owner, nil
}

func rejected(op *Op, reason string) OpResult {
	return OpResult{OpID: op.OpID, Status: StatusRejected, Reason: reason}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
