package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kepthx/gym-tracker/internal/db"
)

const (
	DefaultChangesLimit = 2000
	MaxChangesLimit     = 5000
)

type SessionRow struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	DayID       string `json:"day_id"`
	ProgramHash string `json:"program_hash"`
	StartedAt   int64  `json:"started_at"`
	FinishedAt  *int64 `json:"finished_at"`
	Deleted     bool   `json:"deleted"`
	Note        string `json:"note"`
	UpdatedTS   int64  `json:"updated_ts"`
	UpdatedBy   string `json:"updated_by"`
	Rev         int64  `json:"rev"`
}

type SetRow struct {
	SessionID  string   `json:"session_id"`
	ExerciseID string   `json:"exercise_id"`
	Idx        int64    `json:"idx"`
	Done       bool     `json:"done"`
	Weight     *float64 `json:"weight"`
	Reps       *string  `json:"reps"`
	Deleted    bool     `json:"deleted"`
	UpdatedTS  int64    `json:"updated_ts"`
	UpdatedBy  string   `json:"updated_by"`
	Rev        int64    `json:"rev"`
}

// ProgramRow is a program snapshot. It is immutable, so the client caches it by hash
// forever and renders history with the program that was in force at workout time.
type ProgramRow struct {
	Hash string          `json:"hash"`
	JSON json.RawMessage `json:"json"`
}

type ChangeSet struct {
	Sessions []SessionRow `json:"sessions"`
	Sets     []SetRow     `json:"sets"`
	Programs []ProgramRow `json:"programs"`
}

type ChangesResult struct {
	Changes ChangeSet
	Cursor  int64
	HasMore bool
}

// Changes returns everything that changed for a user after the since cursor.
//
// Sessions and sets share one rev number space, so the limit is applied at a common
// boundary: otherwise some rows with the same rev would end up past the cursor and be
// lost forever.
//
// knownPrograms are the hashes of snapshots the client already has; they are not resent.
func (s *Store) Changes(
	ctx context.Context,
	userID int64,
	since int64,
	limit int,
	knownPrograms []string,
) (*ChangesResult, error) {
	if limit <= 0 || limit > MaxChangesLimit {
		limit = DefaultChangesLimit
	}

	// The cursor is taken BEFORE the queries: anything written after this moment lands
	// in the next delta. The reverse order could skip a row.
	globalRev, err := db.CurrentRev(ctx, s.db.R)
	if err != nil {
		return nil, err
	}

	// Reading limit+1 rows is how we learn the result was truncated without a second query.
	sessions, err := s.selectSessions(ctx, userID, since, limit+1)
	if err != nil {
		return nil, err
	}
	sets, err := s.selectSets(ctx, userID, since, limit+1)
	if err != nil {
		return nil, err
	}

	cursor := globalRev
	hasMore := false

	if len(sessions) > limit || len(sets) > limit {
		hasMore = true

		// The boundary is the smallest rev that did NOT fit into this page.
		boundary := int64(-1)
		if len(sessions) > limit {
			boundary = sessions[limit].Rev
		}
		if len(sets) > limit && (boundary < 0 || sets[limit].Rev < boundary) {
			boundary = sets[limit].Rev
		}

		sessions = trimSessions(sessions, boundary)
		sets = trimSets(sets, boundary)
		cursor = boundary - 1
	}

	programs, err := s.programsFor(ctx, userID, sessions, knownPrograms)
	if err != nil {
		return nil, err
	}

	return &ChangesResult{
		Changes: ChangeSet{Sessions: sessions, Sets: sets, Programs: programs},
		Cursor:  cursor,
		HasMore: hasMore,
	}, nil
}

func (s *Store) selectSessions(ctx context.Context, userID, since int64, limit int) ([]SessionRow, error) {
	rows, err := s.db.R.QueryContext(ctx,
		`SELECT id, date, day_id, program_hash, started_at, finished_at,
		        deleted, note, updated_ts, updated_by, rev
		 FROM sessions
		 WHERE user_id = ? AND rev > ?
		 ORDER BY rev
		 LIMIT ?`, userID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("выбрать тренировки: %w", err)
	}
	defer rows.Close()

	out := []SessionRow{}
	for rows.Next() {
		var r SessionRow
		var deleted int
		if err := rows.Scan(&r.ID, &r.Date, &r.DayID, &r.ProgramHash, &r.StartedAt,
			&r.FinishedAt, &deleted, &r.Note, &r.UpdatedTS, &r.UpdatedBy, &r.Rev); err != nil {
			return nil, fmt.Errorf("прочитать тренировку: %w", err)
		}
		r.Deleted = deleted == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) selectSets(ctx context.Context, userID, since int64, limit int) ([]SetRow, error) {
	// user_id is deliberately not duplicated into sets: ownership lives on the workout,
	// and denormalising it invites drift between two sources of truth.
	rows, err := s.db.R.QueryContext(ctx,
		`SELECT st.session_id, st.exercise_id, st.idx, st.done, st.weight, st.reps,
		        st.deleted, st.updated_ts, st.updated_by, st.rev
		 FROM sets st
		 JOIN sessions se ON se.id = st.session_id
		 WHERE se.user_id = ? AND st.rev > ?
		 ORDER BY st.rev
		 LIMIT ?`, userID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("выбрать подходы: %w", err)
	}
	defer rows.Close()

	out := []SetRow{}
	for rows.Next() {
		var r SetRow
		var done, deleted int
		if err := rows.Scan(&r.SessionID, &r.ExerciseID, &r.Idx, &done, &r.Weight, &r.Reps,
			&deleted, &r.UpdatedTS, &r.UpdatedBy, &r.Rev); err != nil {
			return nil, fmt.Errorf("прочитать подход: %w", err)
		}
		r.Done = done == 1
		r.Deleted = deleted == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// programsFor collects the snapshots the client will need to render the workouts being
// sent, plus the current program — and subtracts the ones the client already has.
func (s *Store) programsFor(
	ctx context.Context,
	userID int64,
	sessions []SessionRow,
	knownPrograms []string,
) ([]ProgramRow, error) {
	known := make(map[string]struct{}, len(knownPrograms))
	for _, h := range knownPrograms {
		known[h] = struct{}{}
	}

	needed := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	add := func(hash string) {
		if hash == "" {
			return
		}
		if _, ok := known[hash]; ok {
			return
		}
		if _, ok := seen[hash]; ok {
			return
		}
		seen[hash] = struct{}{}
		needed = append(needed, hash)
	}

	var current *string
	err := s.db.R.QueryRowContext(ctx,
		`SELECT current_program_hash FROM users WHERE id = ?`, userID).Scan(&current)
	// The user may not exist (a request with an invalid token), or the program may not be
	// written yet — both mean "no current snapshot", not a failure.
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("прочитать текущую программу: %w", err)
	}
	if current != nil {
		add(*current)
	}
	for _, s := range sessions {
		add(s.ProgramHash)
	}

	out := []ProgramRow{}
	for _, hash := range needed {
		var raw string
		if err := s.db.R.QueryRowContext(ctx,
			`SELECT json FROM programs WHERE hash = ?`, hash).Scan(&raw); err != nil {
			return nil, fmt.Errorf("прочитать снапшот программы %s: %w", hash, err)
		}
		out = append(out, ProgramRow{Hash: hash, JSON: json.RawMessage(raw)})
	}
	return out, nil
}

func trimSessions(rows []SessionRow, boundary int64) []SessionRow {
	for i, r := range rows {
		if r.Rev >= boundary {
			return rows[:i]
		}
	}
	return rows
}

func trimSets(rows []SetRow, boundary int64) []SetRow {
	for i, r := range rows {
		if r.Rev >= boundary {
			return rows[:i]
		}
	}
	return rows
}
