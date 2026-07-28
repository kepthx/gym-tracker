package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kepthx/gym-tracker/internal/db"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("миграции: %v", err)
	}
	return d
}

func seed(t *testing.T, d *db.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := d.W.ExecContext(ctx,
		`INSERT INTO programs(hash, json, created_at) VALUES ('h1', '{}', 0)`); err != nil {
		t.Fatalf("вставить программу: %v", err)
	}
	if _, err := d.W.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, created_at, current_program_hash)
		 VALUES ('igor', 'x', 0, 'h1')`); err != nil {
		t.Fatalf("вставить пользователя: %v", err)
	}
	if _, err := d.W.ExecContext(ctx,
		`INSERT INTO sessions(id, user_id, date, day_id, program_hash, started_at,
		                      updated_ts, updated_by, rev)
		 VALUES ('s1', 1, '2026-07-28', 'd1', 'h1', 1, 1, 'dev', 1)`); err != nil {
		t.Fatalf("вставить тренировку: %v", err)
	}
}

// VACUUM INTO takes a consistent snapshot of a live database in WAL mode — something
// copying the file cannot do.
func TestBackupOnLiveDatabase(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	seed(t, d)

	dir := filepath.Join(t.TempDir(), "backups")
	path, err := New(d, dir).Run(ctx, time.Now())
	if err != nil {
		t.Fatalf("сделать копию: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("копия не создана: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("копия пустая")
	}

	// The backup has to open and contain data — otherwise it is not a backup.
	restored, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatalf("открыть копию: %v", err)
	}
	defer restored.Close()

	var sessions int
	if err := restored.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatalf("прочитать копию: %v", err)
	}
	if sessions != 1 {
		t.Fatalf("тренировок в копии: %d, ожидалась 1", sessions)
	}
}

func TestBackupKeepsWritingDuringWork(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	seed(t, d)
	manager := New(d, filepath.Join(t.TempDir(), "backups"))

	if _, err := manager.Run(ctx, time.Now()); err != nil {
		t.Fatalf("копия: %v", err)
	}
	// After a backup and a journal truncation the database has to remain usable.
	if _, err := d.W.ExecContext(ctx,
		`INSERT INTO sessions(id, user_id, date, day_id, program_hash, started_at,
		                      finished_at, updated_ts, updated_by, rev)
		 VALUES ('s2', 1, '2026-07-29', 'd2', 'h1', 2, 3, 2, 'dev', 2)`); err != nil {
		t.Fatalf("запись после копии: %v", err)
	}
}

func TestBackupRetention(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	seed(t, d)

	dir := filepath.Join(t.TempDir(), "backups")
	manager := New(d, dir)

	base := time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC)
	for i := 0; i < keepDaily+5; i++ {
		if _, err := manager.Run(ctx, base.AddDate(0, 0, i)); err != nil {
			t.Fatalf("копия %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("прочитать каталог: %v", err)
	}
	if len(entries) != keepDaily {
		t.Fatalf("копий осталось %d, ожидалось %d", len(entries), keepDaily)
	}

	// The most recent ones are the ones that should remain.
	latest, err := manager.Latest()
	if err != nil {
		t.Fatalf("свежая копия: %v", err)
	}
	last := base.AddDate(0, 0, keepDaily+4).Format("20060102")
	if !strings.Contains(filepath.Base(latest), last) {
		t.Fatalf("свежая копия %s не от %s", filepath.Base(latest), last)
	}
}

func TestMaybeRunSkipsWhenFresh(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	seed(t, d)

	dir := filepath.Join(t.TempDir(), "backups")
	manager := New(d, dir)

	now := time.Now()
	manager.MaybeRun(ctx, now)
	manager.MaybeRun(ctx, now.Add(time.Hour))

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("копий: %d, ожидалась 1 — свежая копия не должна пересоздаваться", len(entries))
	}

	// Six hours after a finished workout, though, a backup is needed.
	manager.MaybeRun(ctx, now.Add(opportunisticAge+time.Minute))
	entries, _ = os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("копий: %d, ожидалось 2", len(entries))
	}
}
