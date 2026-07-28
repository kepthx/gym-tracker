package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate runs the pending migrations in ascending order of version.
// Each migration and the recording of its version go in one transaction: an interruption
// mid-run cannot leave the database in a "new schema, old version" state.
func (d *DB) Migrate(ctx context.Context) error {
	current, err := d.schemaVersion(ctx)
	if err != nil {
		return err
	}

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("перечислить миграции: %w", err)
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := versionOf(name)
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}

		body, err := migrationsFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("прочитать миграцию %s: %w", name, err)
		}

		if err := d.applyMigration(ctx, version, string(body)); err != nil {
			return fmt.Errorf("миграция %s: %w", path.Base(name), err)
		}
		current = version
	}
	return nil
}

func (d *DB) applyMigration(ctx context.Context, version int, body string) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("начать транзакцию: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta(k, v) VALUES ('schema_version', ?)
		 ON CONFLICT(k) DO UPDATE SET v = excluded.v`,
		strconv.Itoa(version),
	); err != nil {
		return fmt.Errorf("записать номер схемы: %w", err)
	}
	return tx.Commit()
}

// schemaVersion returns 0 for an empty database: the meta table does not exist there yet.
func (d *DB) schemaVersion(ctx context.Context) (int, error) {
	var exists int
	err := d.W.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'`).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("проверить наличие meta: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}

	var raw string
	err = d.W.QueryRowContext(ctx, `SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("прочитать номер схемы: %w", err)
	}

	version, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("номер схемы %q не число: %w", raw, err)
	}
	return version, nil
}

// versionOf parses "migrations/0001_init.sql" into 1.
func versionOf(name string) (int, error) {
	base := path.Base(name)
	prefix, _, found := strings.Cut(base, "_")
	if !found {
		return 0, fmt.Errorf("имя миграции %q без префикса номера", base)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("номер миграции в %q не число: %w", base, err)
	}
	return version, nil
}
