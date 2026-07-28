package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type User struct {
	ID          int64
	Username    string
	DisplayName string
	IsAdmin     bool
	Disabled    bool
}

// ErrUserExists — the username is already taken.
var ErrUserExists = errors.New("пользователь уже существует")

// CreateUser creates a user. Users are created by hand from the command line: the app has
// no open web registration and none is planned.
func (s *Store) CreateUser(ctx context.Context, username, displayName, passwordHash string, isAdmin bool) (int64, error) {
	res, err := s.db.W.ExecContext(ctx,
		`INSERT INTO users(username, display_name, password_hash, created_at, is_admin)
		 VALUES (?, ?, ?, ?, ?)`,
		username, displayName, passwordHash, time.Now().UnixMilli(), boolToInt(isAdmin))
	if err != nil {
		// UNIQUE on username: without this branch the caller would see an opaque driver error.
		var exists bool
		if probe := s.db.R.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM users WHERE username = ?)`, username).Scan(&exists); probe == nil && exists {
			return 0, ErrUserExists
		}
		return 0, fmt.Errorf("создать пользователя: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("получить id пользователя: %w", err)
	}
	return id, nil
}

// UserByName looks a user up by name, case-insensitively.
func (s *Store) UserByName(ctx context.Context, username string) (*User, string, error) {
	var u User
	var passwordHash string
	var isAdmin, disabled int
	err := s.db.R.QueryRowContext(ctx,
		`SELECT id, username, display_name, password_hash, is_admin, disabled
		 FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.DisplayName, &passwordHash, &isAdmin, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("прочитать пользователя: %w", err)
	}
	u.IsAdmin = isAdmin == 1
	u.Disabled = disabled == 1
	return &u, passwordHash, nil
}

// CountUsers is for the login screen: while there is only one user the name field is not
// shown, and the password alone is enough in the request.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.R.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE disabled = 0`).Scan(&n); err != nil {
		return 0, fmt.Errorf("посчитать пользователей: %w", err)
	}
	return n, nil
}

// OnlyUser returns the sole user, if there is exactly one.
func (s *Store) OnlyUser(ctx context.Context) (*User, error) {
	var u User
	var isAdmin int
	err := s.db.R.QueryRowContext(ctx,
		`SELECT id, username, display_name, is_admin FROM users WHERE disabled = 0`).
		Scan(&u.ID, &u.Username, &u.DisplayName, &isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("прочитать единственного пользователя: %w", err)
	}
	u.IsAdmin = isAdmin == 1
	return &u, nil
}
