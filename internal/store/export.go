package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ExportVersion is the version of the export format. It changes only if the format stops
// being readable by the previous importer.
const ExportVersion = 1

// Export is a complete dump of one user's data.
//
// It doubles as a backup and as material for analysing progress elsewhere, so the format
// is self-contained: program snapshots sit next to the workouts, and the file reads
// without contacting the server and without knowing which program is current.
type Export struct {
	Version    int          `json:"version"`
	ExportedAt int64        `json:"exported_at"`
	User       ExportUser   `json:"user"`
	Programs   []ProgramRow `json:"programs"`
	Sessions   []SessionRow `json:"sessions"`
	Sets       []SetRow     `json:"sets"`
}

type ExportUser struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// Export assembles the dump for a single user.
func (s *Store) Export(ctx context.Context, userID int64, now time.Time) (*Export, error) {
	var user ExportUser
	if err := s.db.R.QueryRowContext(ctx,
		`SELECT username, display_name FROM users WHERE id = ?`, userID).
		Scan(&user.Username, &user.DisplayName); err != nil {
		return nil, fmt.Errorf("прочитать пользователя: %w", err)
	}

	// EVERYTHING is exported, including deleted rows: tombstones are part of the sync
	// history, and restoring from a backup without them would resurrect deleted workouts.
	sessions, err := s.selectSessions(ctx, userID, -1, MaxChangesLimit*100)
	if err != nil {
		return nil, err
	}
	sets, err := s.selectSets(ctx, userID, -1, MaxChangesLimit*100)
	if err != nil {
		return nil, err
	}

	hashes := map[string]struct{}{}
	for _, session := range sessions {
		hashes[session.ProgramHash] = struct{}{}
	}
	var current *string
	if err := s.db.R.QueryRowContext(ctx,
		`SELECT current_program_hash FROM users WHERE id = ?`, userID).Scan(&current); err == nil && current != nil {
		hashes[*current] = struct{}{}
	}

	programs := []ProgramRow{}
	for hash := range hashes {
		var raw string
		if err := s.db.R.QueryRowContext(ctx,
			`SELECT json FROM programs WHERE hash = ?`, hash).Scan(&raw); err != nil {
			return nil, fmt.Errorf("прочитать снапшот программы %s: %w", hash, err)
		}
		programs = append(programs, ProgramRow{Hash: hash, JSON: json.RawMessage(raw)})
	}

	return &Export{
		Version:    ExportVersion,
		ExportedAt: now.UnixMilli(),
		User:       user,
		Programs:   programs,
		Sessions:   sessions,
		Sets:       sets,
	}, nil
}

// ImportResult reports what the import did.
type ImportResult struct {
	Programs int
	Sessions int
	Sets     int
	Skipped  int
}

// Import pours an export back into the database.
//
// A backup that has never been restored from is not a backup. Import makes the export
// verifiable by a round-trip test and at the same time gives a real way to recover.
//
// Rows merge by the same rules as during sync, so importing on top of a non-empty
// database does not clobber fresher data, and it can be repeated.
func (s *Store) Import(ctx context.Context, userID int64, data *Export, now time.Time) (*ImportResult, error) {
	if data.Version != ExportVersion {
		return nil, fmt.Errorf("версия выгрузки %d, поддерживается %d", data.Version, ExportVersion)
	}

	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("начать транзакцию: %w", err)
	}
	defer tx.Rollback()

	result := &ImportResult{}

	for _, program := range data.Programs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO programs(hash, json, created_at) VALUES (?, ?, ?)
			 ON CONFLICT(hash) DO NOTHING`,
			program.Hash, string(program.JSON), now.UnixMilli()); err != nil {
			return nil, fmt.Errorf("вставить снапшот программы: %w", err)
		}
		result.Programs++
	}

	// Workouts are poured in with the "only one unfinished" rule relaxed: an export
	// assembled from several sources could hold more than one. The conflict is settled
	// afterwards, by the same rules as during sync.
	for _, incoming := range data.Sessions {
		current, err := readSession(ctx, tx, incoming.ID)
		if err != nil {
			return nil, err
		}
		if current != nil && current.UserID != userID {
			result.Skipped++
			continue
		}

		var base *SessionRow
		if current != nil {
			base = &current.SessionRow
		}
		merged := MergeSession(base, incoming)

		rev, err := nextRev(ctx, tx)
		if err != nil {
			return nil, err
		}
		if err := writeSession(ctx, tx, userID, merged, rev); err != nil {
			return nil, err
		}
		result.Sessions++
	}

	for _, incoming := range data.Sets {
		owner, err := sessionOwner(ctx, tx, incoming.SessionID)
		if err != nil || owner != userID {
			result.Skipped++
			continue
		}

		current, err := readSet(ctx, tx, incoming)
		if err != nil {
			return nil, err
		}
		merged := MergeSet(current, incoming)

		rev, err := nextRev(ctx, tx)
		if err != nil {
			return nil, err
		}
		if err := writeSet(ctx, tx, merged, rev); err != nil {
			return nil, err
		}
		result.Sets++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("зафиксировать импорт: %w", err)
	}
	return result, nil
}
