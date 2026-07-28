// Package db opens SQLite and holds two connection pools: a writer and a reader.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const driverName = "sqlite"

// The pragmas are applied to every connection in both pools.
//
//   - foreign_keys are off by default in SQLite — without this line, cascading deletes
//     and referential integrity silently do not work;
//   - synchronous=FULL rather than NORMAL: in WAL mode NORMAL survives the process being
//     killed, but not a power loss. The app writes ~30 rows per workout, so FULL is free,
//     and it fully covers the "not a single lost set" requirement.
var pragmas = []string{
	"journal_mode(WAL)",
	"synchronous(FULL)",
	"busy_timeout(5000)",
	"foreign_keys(1)",
}

// DB is a pair of pools onto one database file.
//
// The writer is limited to a single connection, so writes serialise in Go and SQLITE_BUSY
// on a write becomes structurally impossible. Readers run in parallel; WAL allows that.
type DB struct {
	W    *sql.DB // all writes; _txlock=immediate
	R    *sql.DB // all reads outside a write transaction
	Path string
}

// dsn assembles the connection string by hand, without url.Values: parentheses in pragma
// values are legal in a query string, and building it manually keeps the DSN readable in
// logs and errors.
func dsn(path, txlock string) string {
	var b strings.Builder
	b.WriteString("file:")
	b.WriteString(path)
	b.WriteString("?_txlock=")
	b.WriteString(txlock)
	for _, p := range pragmas {
		b.WriteString("&_pragma=")
		b.WriteString(p)
	}
	return b.String()
}

// Open opens the database at the given file path and verifies that the pragmas actually
// took effect. The directory is created here rather than in the caller: there are several
// entry points (the service, adduser, import), and forgetting it in one of them is too easy.
func Open(ctx context.Context, path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("создать каталог для базы: %w", err)
		}
	}

	// _txlock=immediate is the single most important correctness setting for writes.
	// A deferred transaction that starts by reading and then writes has to escalate its
	// lock, and on escalation the busy handler is not called: SQLITE_BUSY arrives instantly
	// regardless of busy_timeout. Taking the write lock up front is the only cure.
	w, err := sql.Open(driverName, dsn(path, "immediate"))
	if err != nil {
		return nil, fmt.Errorf("открыть базу на запись: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)

	if err := w.PingContext(ctx); err != nil {
		w.Close()
		return nil, fmt.Errorf("подключиться к базе %s: %w", path, err)
	}

	r, err := sql.Open(driverName, dsn(path, "deferred"))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("открыть базу на чтение: %w", err)
	}
	r.SetMaxOpenConns(4)
	r.SetMaxIdleConns(4)

	d := &DB{W: w, R: r, Path: path}
	if err := d.verifyPragmas(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// verifyPragmas catches a typo in the DSN: without this check a pragma that failed to
// apply (foreign_keys, say) would only surface as corrupted data much later.
func (d *DB) verifyPragmas(ctx context.Context) error {
	for _, pool := range []struct {
		name string
		sql  *sql.DB
	}{{"писатель", d.W}, {"читатель", d.R}} {
		var journal string
		if err := pool.sql.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			return fmt.Errorf("прочитать journal_mode (%s): %w", pool.name, err)
		}
		if !strings.EqualFold(journal, "wal") {
			return fmt.Errorf("journal_mode=%s вместо wal (%s)", journal, pool.name)
		}

		var fk int
		if err := pool.sql.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			return fmt.Errorf("прочитать foreign_keys (%s): %w", pool.name, err)
		}
		if fk != 1 {
			return fmt.Errorf("foreign_keys выключены (%s)", pool.name)
		}
	}
	return nil
}

func (d *DB) Close() error {
	var firstErr error
	for _, pool := range []*sql.DB{d.W, d.R} {
		if pool == nil {
			continue
		}
		if err := pool.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// NextRev issues the next value of the global change counter.
// It is only called inside a write transaction, so there is no race: there is one writer.
func NextRev(ctx context.Context, tx *sql.Tx) (int64, error) {
	var rev int64
	err := tx.QueryRowContext(ctx,
		`UPDATE meta SET v = CAST(CAST(v AS INTEGER) + 1 AS TEXT) WHERE k = 'rev'
		 RETURNING CAST(v AS INTEGER)`).Scan(&rev)
	if err != nil {
		return 0, fmt.Errorf("выдать rev: %w", err)
	}
	return rev, nil
}

// CurrentRev returns the current value of the change counter without incrementing it.
func CurrentRev(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var rev int64
	err := q.QueryRowContext(ctx, `SELECT CAST(v AS INTEGER) FROM meta WHERE k = 'rev'`).Scan(&rev)
	if err != nil {
		return 0, fmt.Errorf("прочитать rev: %w", err)
	}
	return rev, nil
}
