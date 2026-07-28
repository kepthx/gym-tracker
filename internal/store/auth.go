package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kepthx/gym-tracker/internal/auth"
)

// Lockout parameters applied after failed login attempts.
//
// The counter lives in the database rather than in memory, so restarting the service does
// not lift a lockout. The window doubles with every further five failures and caps at
// an hour.
const (
	MaxFailuresBeforeLock = 5
	baseLockWindow        = 15 * time.Minute
	maxLockWindow         = time.Hour
)

// CreateToken issues a new login token.
func (s *Store) CreateToken(ctx context.Context, userID int64, ttl time.Duration, userAgent string, now time.Time) (string, time.Time, error) {
	raw, hash, err := auth.NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(ttl)

	if _, err := s.db.W.ExecContext(ctx,
		`INSERT INTO auth_tokens(token_hash, user_id, created_at, last_seen_at, expires_at, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		hash, userID, now.UnixMilli(), now.UnixMilli(), expires.UnixMilli(), truncate(userAgent, 200),
	); err != nil {
		return "", time.Time{}, fmt.Errorf("сохранить токен: %w", err)
	}
	return raw, expires, nil
}

// Session is a recognised token together with its owner.
type Session struct {
	User      *User
	CreatedAt time.Time
	ExpiresAt time.Time
}

// ErrTokenInvalid — the token does not exist, has expired, or its owner is disabled.
var ErrTokenInvalid = errors.New("токен недействителен")

// LookupToken finds the owner of a token.
func (s *Store) LookupToken(ctx context.Context, raw string, now time.Time) (*Session, error) {
	var (
		u                  User
		createdAt, expires int64
		isAdmin, disabled  int
	)
	err := s.db.R.QueryRowContext(ctx,
		`SELECT t.created_at, t.expires_at, u.id, u.username, u.display_name, u.is_admin, u.disabled
		 FROM auth_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = ?`, auth.HashToken(raw)).
		Scan(&createdAt, &expires, &u.ID, &u.Username, &u.DisplayName, &isAdmin, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("прочитать токен: %w", err)
	}
	if disabled == 1 || expires <= now.UnixMilli() {
		return nil, ErrTokenInvalid
	}
	u.IsAdmin = isAdmin == 1

	return &Session{
		User:      &u,
		CreatedAt: time.UnixMilli(createdAt),
		ExpiresAt: time.UnixMilli(expires),
	}, nil
}

// SlideToken extends a token's lifetime.
//
// It is called only once a token has aged noticeably: for an active user the window
// effectively never expires, so the password is never asked for at the gym.
func (s *Store) SlideToken(ctx context.Context, raw string, ttl time.Duration, now time.Time) (time.Time, error) {
	expires := now.Add(ttl)
	if _, err := s.db.W.ExecContext(ctx,
		`UPDATE auth_tokens SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?`,
		now.UnixMilli(), expires.UnixMilli(), auth.HashToken(raw)); err != nil {
		return time.Time{}, fmt.Errorf("продлить токен: %w", err)
	}
	return expires, nil
}

// DeleteToken revokes a token — logging out on this device.
func (s *Store) DeleteToken(ctx context.Context, raw string) error {
	if _, err := s.db.W.ExecContext(ctx,
		`DELETE FROM auth_tokens WHERE token_hash = ?`, auth.HashToken(raw)); err != nil {
		return fmt.Errorf("удалить токен: %w", err)
	}
	return nil
}

// RecordLoginAttempt records a login attempt. A successful attempt resets the lockout
// counter, because every check counts only the failures that follow the last success.
func (s *Store) RecordLoginAttempt(ctx context.Context, ip, username string, ok bool, now time.Time) error {
	if _, err := s.db.W.ExecContext(ctx,
		`INSERT INTO login_attempts(at, ip, username, ok) VALUES (?, ?, ?, ?)`,
		now.UnixMilli(), truncate(ip, 64), truncate(username, 64), boolToInt(ok)); err != nil {
		return fmt.Errorf("записать попытку входа: %w", err)
	}
	return nil
}

// LockoutFor returns how much longer this username has to wait. Zero means "go ahead".
func (s *Store) LockoutFor(ctx context.Context, username string, now time.Time) (time.Duration, error) {
	var lastSuccess sql.NullInt64
	if err := s.db.R.QueryRowContext(ctx,
		`SELECT MAX(at) FROM login_attempts WHERE username = ? AND ok = 1`,
		username).Scan(&lastSuccess); err != nil {
		return 0, fmt.Errorf("прочитать последний успешный вход: %w", err)
	}
	since := int64(0)
	if lastSuccess.Valid {
		since = lastSuccess.Int64
	}

	var failures int
	var lastFailure sql.NullInt64
	if err := s.db.R.QueryRowContext(ctx,
		`SELECT count(*), MAX(at) FROM login_attempts WHERE username = ? AND ok = 0 AND at > ?`,
		username, since).Scan(&failures, &lastFailure); err != nil {
		return 0, fmt.Errorf("посчитать неудачные попытки: %w", err)
	}
	if failures < MaxFailuresBeforeLock || !lastFailure.Valid {
		return 0, nil
	}

	window := baseLockWindow << ((failures - MaxFailuresBeforeLock) / MaxFailuresBeforeLock)
	if window > maxLockWindow || window <= 0 {
		window = maxLockWindow
	}

	unlockAt := time.UnixMilli(lastFailure.Int64).Add(window)
	if remaining := unlockAt.Sub(now); remaining > 0 {
		return remaining, nil
	}
	return 0, nil
}

// PruneAuth clears out expired tokens, old login attempts and the operation ledger.
func (s *Store) PruneAuth(ctx context.Context, now time.Time) error {
	cutoff := now.AddDate(0, 0, -30).UnixMilli()
	statements := []struct {
		sql  string
		arg  int64
		what string
	}{
		{`DELETE FROM auth_tokens WHERE expires_at <= ?`, now.UnixMilli(), "истёкшие токены"},
		{`DELETE FROM login_attempts WHERE at < ?`, cutoff, "старые попытки входа"},
		{`DELETE FROM applied_ops WHERE applied_at < ?`, cutoff, "старые записи журнала"},
	}
	for _, st := range statements {
		if _, err := s.db.W.ExecContext(ctx, st.sql, st.arg); err != nil {
			return fmt.Errorf("почистить %s: %w", st.what, err)
		}
	}
	return nil
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
