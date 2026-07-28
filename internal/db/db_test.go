package db

import (
	"context"
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()

	d, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("миграции: %v", err)
	}
	return d
}

// seed creates a user and a program — the minimum without which a session cannot be inserted.
func seed(t *testing.T, d *DB) int64 {
	t.Helper()
	ctx := context.Background()

	if _, err := d.W.ExecContext(ctx,
		`INSERT INTO programs(hash, json, created_at) VALUES ('h1', '{}', 0)`); err != nil {
		t.Fatalf("вставить программу: %v", err)
	}
	res, err := d.W.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, created_at, current_program_hash)
		 VALUES ('igor', 'x', 0, 'h1')`)
	if err != nil {
		t.Fatalf("вставить пользователя: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("id пользователя: %v", err)
	}
	return id
}

func insertSession(t *testing.T, d *DB, id string, userID int64, finishedAt any) error {
	t.Helper()
	_, err := d.W.ExecContext(context.Background(),
		`INSERT INTO sessions(id, user_id, date, day_id, program_hash, started_at,
		                      finished_at, updated_ts, updated_by, rev)
		 VALUES (?, ?, '2026-07-28', 'd1', 'h1', 1, ?, 1, 'dev', 1)`,
		id, userID, finishedAt)
	return err
}

func TestMigrateSetsVersionAndIsRepeatable(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)

	version, err := d.schemaVersion(ctx)
	if err != nil {
		t.Fatalf("номер схемы: %v", err)
	}
	if version != 1 {
		t.Fatalf("номер схемы = %d, ожидалось 1", version)
	}

	// A second run should do nothing and should not fail.
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("повторные миграции: %v", err)
	}

	for _, table := range []string{
		"meta", "programs", "users", "auth_tokens", "login_attempts",
		"sessions", "sets", "applied_ops",
	} {
		var count int
		if err := d.R.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table).Scan(&count); err != nil {
			t.Fatalf("проверить таблицу %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("таблица %s не создана", table)
		}
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	d := testDB(t)
	seed(t, d)

	// user_id=999 does not exist. Without foreign_keys(1) this insert would pass silently.
	if err := insertSession(t, d, "s1", 999, nil); err == nil {
		t.Fatal("вставка сессии с несуществующим пользователем прошла успешно")
	}
}

func TestStrictTablesRejectWrongTypes(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	userID := seed(t, d)

	if err := insertSession(t, d, "s1", userID, nil); err != nil {
		t.Fatalf("вставить сессию: %v", err)
	}

	_, err := d.W.ExecContext(ctx,
		`INSERT INTO sets(session_id, exercise_id, idx, done, weight, reps, updated_ts, updated_by, rev)
		 VALUES ('s1', 'bench_bb', 0, 1, 'много', '5', 1, 'dev', 1)`)
	if err == nil {
		t.Fatal("STRICT пропустил текст в колонку REAL")
	}
}

// The most important schema invariant: there can be only one unfinished workout.
// It lives in a partial unique index rather than in application code.
func TestOneOpenSessionPerUser(t *testing.T) {
	d := testDB(t)
	userID := seed(t, d)

	if err := insertSession(t, d, "s1", userID, nil); err != nil {
		t.Fatalf("первая незавершённая сессия: %v", err)
	}
	if err := insertSession(t, d, "s2", userID, nil); err == nil {
		t.Fatal("вторая незавершённая сессия того же пользователя прошла успешно")
	}

	// A finished one does not take the slot: there can be any number of those.
	if err := insertSession(t, d, "s3", userID, 123); err != nil {
		t.Fatalf("завершённая сессия: %v", err)
	}
	if err := insertSession(t, d, "s4", userID, 456); err != nil {
		t.Fatalf("вторая завершённая сессия: %v", err)
	}

	// The index is partial on user_id — a second user does not get in the first one's way.
	res, err := d.W.ExecContext(context.Background(),
		`INSERT INTO users(username, password_hash, created_at) VALUES ('lena', 'x', 0)`)
	if err != nil {
		t.Fatalf("второй пользователь: %v", err)
	}
	otherID, _ := res.LastInsertId()
	if err := insertSession(t, d, "s5", otherID, nil); err != nil {
		t.Fatalf("незавершённая сессия второго пользователя: %v", err)
	}
}

func TestDeletedSessionFreesTheOpenSlot(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	userID := seed(t, d)

	if err := insertSession(t, d, "s1", userID, nil); err != nil {
		t.Fatalf("первая сессия: %v", err)
	}
	if _, err := d.W.ExecContext(ctx, `UPDATE sessions SET deleted = 1 WHERE id = 's1'`); err != nil {
		t.Fatalf("пометить удалённой: %v", err)
	}
	if err := insertSession(t, d, "s2", userID, nil); err != nil {
		t.Fatalf("новая сессия после удаления предыдущей: %v", err)
	}
}

func TestSetsCascadeWithSession(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)
	userID := seed(t, d)

	if err := insertSession(t, d, "s1", userID, nil); err != nil {
		t.Fatalf("сессия: %v", err)
	}
	if _, err := d.W.ExecContext(ctx,
		`INSERT INTO sets(session_id, exercise_id, idx, done, weight, reps, updated_ts, updated_by, rev)
		 VALUES ('s1', 'bench_bb', 0, 1, 80.5, '5', 1, 'dev', 1)`); err != nil {
		t.Fatalf("подход: %v", err)
	}
	if _, err := d.W.ExecContext(ctx, `DELETE FROM sessions WHERE id = 's1'`); err != nil {
		t.Fatalf("удалить сессию: %v", err)
	}

	var count int
	if err := d.R.QueryRowContext(ctx, `SELECT count(*) FROM sets`).Scan(&count); err != nil {
		t.Fatalf("посчитать подходы: %v", err)
	}
	if count != 0 {
		t.Fatalf("после удаления сессии осталось подходов: %d", count)
	}
}

func TestNextRevIsMonotonic(t *testing.T) {
	ctx := context.Background()
	d := testDB(t)

	start, err := CurrentRev(ctx, d.R)
	if err != nil {
		t.Fatalf("текущий rev: %v", err)
	}
	if start != 0 {
		t.Fatalf("начальный rev = %d, ожидался 0", start)
	}

	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("транзакция: %v", err)
	}
	defer tx.Rollback()

	for want := int64(1); want <= 3; want++ {
		got, err := NextRev(ctx, tx)
		if err != nil {
			t.Fatalf("выдать rev: %v", err)
		}
		if got != want {
			t.Fatalf("rev = %d, ожидался %d", got, want)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("зафиксировать: %v", err)
	}

	after, err := CurrentRev(ctx, d.R)
	if err != nil {
		t.Fatalf("текущий rev: %v", err)
	}
	if after != 3 {
		t.Fatalf("rev после трёх выдач = %d, ожидался 3", after)
	}
}
