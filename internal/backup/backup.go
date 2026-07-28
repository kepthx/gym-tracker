// Package backup takes database backups from inside the running process.
package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kepthx/gym-tracker/internal/db"
)

const (
	// A backup runs at night, plus right after a finished workout if the previous one is
	// older than this: a fresh workout should always be on disk.
	opportunisticAge = 6 * time.Hour
	keepDaily        = 14
)

type Manager struct {
	db  *db.DB
	dir string

	mu   sync.Mutex
	last time.Time
}

func New(d *db.DB, dir string) *Manager {
	return &Manager{db: d, dir: dir}
}

func (m *Manager) LastRun() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

// Run takes a backup, verifies it and prunes the old ones.
//
// VACUUM INTO works on a live database in WAL mode and yields a consistent snapshot as one
// compact file. Copying the file cannot do that, and the sqlite3 utility is deliberately
// not installed on the server — the binary has to be self-sufficient.
func (m *Manager) Run(ctx context.Context, now time.Time) (string, error) {
	if err := os.MkdirAll(m.dir, 0o750); err != nil {
		return "", fmt.Errorf("создать каталог копий: %w", err)
	}

	// VACUUM INTO refuses to write into an existing file, so the name is always new.
	path := filepath.Join(m.dir, fmt.Sprintf("db-%s.db", now.UTC().Format("20060102T150405Z")))

	if _, err := m.db.W.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return "", fmt.Errorf("сделать копию: %w", err)
	}

	if err := verify(ctx, path); err != nil {
		// A corrupt backup is worse than none: it creates a false sense of safety.
		os.Remove(path)
		return "", err
	}

	// Keeps the WAL from growing without bound on a rarely restarted process.
	if _, err := m.db.W.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		slog.Warn("не удалось усечь журнал", "ошибка", err)
	}

	if err := m.prune(); err != nil {
		slog.Warn("не удалось убрать старые копии", "ошибка", err)
	}

	m.mu.Lock()
	m.last = now
	m.mu.Unlock()

	return path, nil
}

// verify opens the backup on a separate connection and checks its integrity.
func verify(ctx context.Context, path string) error {
	probe, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("открыть копию для проверки: %w", err)
	}
	defer probe.Close()

	var result string
	if err := probe.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("проверить копию: %w", err)
	}
	if !strings.EqualFold(result, "ok") {
		return fmt.Errorf("копия не прошла проверку целостности: %s", result)
	}

	// An empty database also passes integrity_check, so the contents are checked too.
	var tables int
	if err := probe.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&tables); err != nil {
		return fmt.Errorf("проверить схему копии: %w", err)
	}
	if tables != 1 {
		return fmt.Errorf("в копии нет таблицы тренировок")
	}
	return nil
}

func (m *Manager) prune() error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}

	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "db-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	// The names carry the date in sortable form, so there is no need to read file times.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	for _, name := range names[min(keepDaily, len(names)):] {
		if err := os.Remove(filepath.Join(m.dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// Latest returns the path to the most recent backup.
func (m *Manager) Latest() (string, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return "", err
	}
	newest := ""
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "db-") {
			continue
		}
		if e.Name() > newest {
			newest = e.Name()
		}
	}
	if newest == "" {
		return "", fmt.Errorf("копий пока нет")
	}
	return filepath.Join(m.dir, newest), nil
}

// MaybeRun takes a backup if the last one is older than six hours.
// Called after a workout is finished so that it lands in a backup right away.
func (m *Manager) MaybeRun(ctx context.Context, now time.Time) {
	m.mu.Lock()
	fresh := !m.last.IsZero() && now.Sub(m.last) < opportunisticAge
	m.mu.Unlock()
	if fresh {
		return
	}
	if _, err := m.Run(ctx, now); err != nil {
		slog.Error("не удалось сделать копию", "ошибка", err)
	}
}

// Loop takes a backup at startup and once a day thereafter.
//
// No separate service is set up for this: the process already holds a database connection,
// can verify the result, and can report a failure in the same place as everything else.
func (m *Manager) Loop(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		path, err := m.Run(ctx, time.Now())
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("не удалось сделать копию", "ошибка", err)
			}
		} else {
			slog.Info("копия готова", "файл", filepath.Base(path))
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
