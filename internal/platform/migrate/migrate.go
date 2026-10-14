// Package migrate applies the SQL files in the migrations directory in order,
// tracking applied versions in a schema_migrations table. It intentionally has
// no external dependency so `openpay migrate` works out of the box.
package migrate

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const createTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    BIGINT PRIMARY KEY,
    name       TEXT        NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`

type migration struct {
	version int64
	name    string
	path    string
}

// Run applies every pending migration found in dir. It is safe to call
// repeatedly: already-applied versions are skipped.
func Run(ctx context.Context, pool *pgxpool.Pool, dir string, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if _, err := pool.Exec(ctx, createTableSQL); err != nil {
		return fmt.Errorf("migrate: creating schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	migrations, err := load(dir)
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		log.Warn("migrate: no migration files found", "dir", dir)
		return nil
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := applyOne(ctx, pool, m); err != nil {
			return err
		}
		log.Info("migrate: applied", "version", m.version, "name", m.name)
	}
	return nil
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[int64]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrate: reading schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64]bool)
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("migrate: scanning version: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func load(dir string) ([]migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: reading dir %s: %w", dir, err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, err := parseName(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: name, path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// parseName extracts the numeric version and descriptive name from a filename
// such as "0001_init.sql" => (1, "init").
func parseName(filename string) (int64, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	prefix, rest, ok := strings.Cut(base, "_")
	if !ok {
		return 0, "", fmt.Errorf("migrate: %s must be named <version>_<name>.sql", filename)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("migrate: %s has a non-numeric version prefix: %w", filename, err)
	}
	return version, rest, nil
}

func applyOne(ctx context.Context, pool *pgxpool.Pool, m migration) error {
	body, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("migrate: reading %s: %w", m.path, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, string(body)); err != nil {
		return fmt.Errorf("migrate: applying %s: %w", m.name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.version, m.name); err != nil {
		return fmt.Errorf("migrate: recording %s: %w", m.name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: committing %s: %w", m.name, err)
	}
	return nil
}
