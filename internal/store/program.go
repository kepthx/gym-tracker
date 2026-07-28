package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kepthx/gym-tracker/internal/program"
)

// ProgramSyncReport describes how the files in programs/ were laid out across users.
// It is returned to the caller so the service can log who was left without a program and
// whose file is sitting there for nothing — both situations are silent, and so dangerous.
type ProgramSyncReport struct {
	// Attached — which program is attached to which user.
	Attached map[string]string
	// UnknownUsers — programs/<name>.json files with no user of that name.
	UnknownUsers []string
	// UsersWithoutProgram — users for whom no file exists.
	UsersWithoutProgram []string
}

// SyncPrograms puts snapshots into the programs table and attaches them to users.
//
// Snapshots are immutable and content-addressed, so rewriting the same content is a
// no-op, and old snapshots stay forever: history references them.
func (s *Store) SyncPrograms(ctx context.Context, snapshots map[string]*program.Snapshot) (*ProgramSyncReport, error) {
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("начать транзакцию: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UnixMilli()
	report := &ProgramSyncReport{Attached: map[string]string{}}

	for username, snapshot := range snapshots {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO programs(hash, json, created_at) VALUES (?, ?, ?)
			 ON CONFLICT(hash) DO NOTHING`,
			snapshot.Hash, string(snapshot.Canonical), now,
		); err != nil {
			return nil, fmt.Errorf("сохранить снапшот программы %s: %w", username, err)
		}

		var userID int64
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM users WHERE username = ?`, username).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			report.UnknownUsers = append(report.UnknownUsers, username)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("найти пользователя %s: %w", username, err)
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET current_program_hash = ? WHERE id = ?`,
			snapshot.Hash, userID,
		); err != nil {
			return nil, fmt.Errorf("привязать программу к пользователю %s: %w", username, err)
		}
		report.Attached[username] = snapshot.Hash
	}

	rows, err := tx.QueryContext(ctx, `SELECT username FROM users WHERE disabled = 0`)
	if err != nil {
		return nil, fmt.Errorf("перечислить пользователей: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return nil, fmt.Errorf("прочитать пользователя: %w", err)
		}
		if _, ok := snapshots[username]; !ok {
			report.UsersWithoutProgram = append(report.UsersWithoutProgram, username)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("перечислить пользователей: %w", err)
	}

	sort.Strings(report.UnknownUsers)
	sort.Strings(report.UsersWithoutProgram)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("зафиксировать программы: %w", err)
	}
	return report, nil
}

// ProgramByHash returns a program snapshot by hash. Needed to render history with the
// program that was in force at workout time rather than the current one.
func (s *Store) ProgramByHash(ctx context.Context, hash string) (*program.Snapshot, error) {
	var raw string
	err := s.db.R.QueryRowContext(ctx, `SELECT json FROM programs WHERE hash = ?`, hash).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("прочитать программу %s: %w", hash, err)
	}
	return program.Parse("programs."+hash, []byte(raw))
}

// CurrentProgram returns the user's current program.
// Having no program is a normal situation (the user exists, the file is not written yet).
func (s *Store) CurrentProgram(ctx context.Context, userID int64) (*program.Snapshot, error) {
	var hash sql.NullString
	err := s.db.R.QueryRowContext(ctx,
		`SELECT current_program_hash FROM users WHERE id = ?`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("прочитать текущую программу: %w", err)
	}
	if !hash.Valid {
		return nil, ErrNoProgram
	}
	return s.ProgramByHash(ctx, hash.String)
}
