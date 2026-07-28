package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kepthx/gym-tracker/internal/db"
)

// Reading and writing ready-made rows. Used by the export importer, where rows arrive in
// bulk from outside rather than one operation at a time.

type ownedSession struct {
	SessionRow
	UserID int64
}

func nextRev(ctx context.Context, tx *sql.Tx) (int64, error) {
	return db.NextRev(ctx, tx)
}

func readSession(ctx context.Context, tx *sql.Tx, id string) (*ownedSession, error) {
	var row ownedSession
	var deleted int
	err := tx.QueryRowContext(ctx,
		`SELECT user_id, id, date, day_id, program_hash, started_at, finished_at,
		        deleted, note, updated_ts, updated_by, rev
		 FROM sessions WHERE id = ?`, id).
		Scan(&row.UserID, &row.ID, &row.Date, &row.DayID, &row.ProgramHash, &row.StartedAt,
			&row.FinishedAt, &deleted, &row.Note, &row.UpdatedTS, &row.UpdatedBy, &row.Rev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("прочитать тренировку: %w", err)
	}
	row.Deleted = deleted == 1
	return &row, nil
}

func writeSession(ctx context.Context, tx *sql.Tx, userID int64, row SessionRow, rev int64) error {
	// There can only be one unfinished workout — a database invariant. If the export
	// holds several, the conflict is settled by the same rule as during sync: the one
	// started later stays open.
	if row.FinishedAt == nil && !row.Deleted {
		resolved, err := resolveOpen(ctx, tx, userID, row)
		if err != nil {
			return err
		}
		row = resolved
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions(id, user_id, date, day_id, program_hash, started_at,
		                      finished_at, deleted, note, updated_ts, updated_by, rev)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   date = excluded.date, day_id = excluded.day_id, program_hash = excluded.program_hash,
		   started_at = excluded.started_at, finished_at = excluded.finished_at,
		   deleted = excluded.deleted, note = excluded.note,
		   updated_ts = excluded.updated_ts, updated_by = excluded.updated_by, rev = excluded.rev`,
		row.ID, userID, row.Date, row.DayID, row.ProgramHash, row.StartedAt,
		row.FinishedAt, boolToInt(row.Deleted), row.Note, row.UpdatedTS, row.UpdatedBy, rev,
	); err != nil {
		return fmt.Errorf("записать тренировку %s: %w", row.ID, err)
	}
	return nil
}

// resolveOpen frees the single "open" slot and returns the row, possibly already closed
// if the one already in the database won.
func resolveOpen(ctx context.Context, tx *sql.Tx, userID int64, incoming SessionRow) (SessionRow, error) {
	var openID string
	var openStarted int64
	err := tx.QueryRowContext(ctx,
		`SELECT id, started_at FROM sessions
		 WHERE user_id = ? AND id != ? AND finished_at IS NULL AND deleted = 0`,
		userID, incoming.ID).Scan(&openID, &openStarted)
	if errors.Is(err, sql.ErrNoRows) {
		return incoming, nil
	}
	if err != nil {
		return incoming, fmt.Errorf("найти открытую тренировку: %w", err)
	}

	if !startsLater(incoming.StartedAt, incoming.ID, openStarted, openID) {
		// The incoming one started earlier — it is the one that closes, at the start of
		// the already open workout.
		closed := openStarted
		incoming.FinishedAt = &closed
		return incoming, nil
	}

	rev, err := nextRev(ctx, tx)
	if err != nil {
		return incoming, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET finished_at = ?, rev = ? WHERE id = ?`,
		incoming.StartedAt, rev, openID); err != nil {
		return incoming, fmt.Errorf("закрыть предыдущую тренировку: %w", err)
	}
	return incoming, nil
}

func readSet(ctx context.Context, tx *sql.Tx, key SetRow) (*SetRow, error) {
	var row SetRow
	var done, deleted int
	err := tx.QueryRowContext(ctx,
		`SELECT session_id, exercise_id, idx, done, weight, reps, deleted,
		        updated_ts, updated_by, rev
		 FROM sets WHERE session_id = ? AND exercise_id = ? AND idx = ?`,
		key.SessionID, key.ExerciseID, key.Idx).
		Scan(&row.SessionID, &row.ExerciseID, &row.Idx, &done, &row.Weight, &row.Reps,
			&deleted, &row.UpdatedTS, &row.UpdatedBy, &row.Rev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("прочитать подход: %w", err)
	}
	row.Done = done == 1
	row.Deleted = deleted == 1
	return &row, nil
}

func writeSet(ctx context.Context, tx *sql.Tx, row SetRow, rev int64) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sets(session_id, exercise_id, idx, done, weight, reps, deleted,
		                  updated_ts, updated_by, rev)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id, exercise_id, idx) DO UPDATE SET
		   done = excluded.done, weight = excluded.weight, reps = excluded.reps,
		   deleted = excluded.deleted, updated_ts = excluded.updated_ts,
		   updated_by = excluded.updated_by, rev = excluded.rev`,
		row.SessionID, row.ExerciseID, row.Idx, boolToInt(row.Done), row.Weight, row.Reps,
		boolToInt(row.Deleted), row.UpdatedTS, row.UpdatedBy, rev,
	); err != nil {
		return fmt.Errorf("записать подход: %w", err)
	}
	return nil
}
