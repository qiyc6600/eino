package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const migrationLockName = "agent-eino-demo-schema-migrations"

var migrationFilename = regexp.MustCompile(`^(\d+)_([a-z0-9][a-z0-9_-]*)\.sql$`)

// migrationFiles is compiled into the server binary so every instance applies
// exactly the schema that its application code expects.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationFilename.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		data, err := migrationFiles.ReadFile(filepath.ToSlash("migrations/" + entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		script := strings.TrimSpace(string(data))
		if script == "" {
			return nil, fmt.Errorf("migration %q is empty", entry.Name())
		}
		digest := sha256.Sum256([]byte(script))
		migrations = append(migrations, migration{
			Version:  version,
			Name:     match[2],
			SQL:      script,
			Checksum: hex.EncodeToString(digest[:]),
		})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no embedded database migrations")
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].Version == migrations[i].Version {
			return nil, fmt.Errorf("duplicate migration version %d", migrations[i].Version)
		}
	}
	return migrations, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	// A session advisory lock serializes schema changes across every application
	// instance. All work stays on this connection so the lock cannot be lost to
	// pool scheduling between versions.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, migrationLockName); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(releaseCtx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, migrationLockName)
	}()

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS agent_schema_migrations (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("create schema migration table: %w", err)
	}

	applied, err := readAppliedMigrations(ctx, conn)
	if err != nil {
		return err
	}
	if err := validateAppliedMigrations(migrations, applied, false); err != nil {
		return err
	}

	for _, item := range migrations {
		if _, ok := applied[item.Version]; ok {
			continue
		}
		if err := applyMigration(ctx, conn, item); err != nil {
			return err
		}
	}
	return nil
}

type appliedMigration struct {
	Name     string
	Checksum string
}

type migrationQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readAppliedMigrations(ctx context.Context, querier migrationQuerier) (map[int64]appliedMigration, error) {
	rows, err := querier.QueryContext(ctx, `SELECT version,name,checksum FROM agent_schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64]appliedMigration)
	for rows.Next() {
		var version int64
		var item appliedMigration
		if err := rows.Scan(&version, &item.Name, &item.Checksum); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	return applied, nil
}

func validateAppliedMigrations(migrations []migration, applied map[int64]appliedMigration, requireAll bool) error {
	known := make(map[int64]migration, len(migrations))
	for _, item := range migrations {
		known[item.Version] = item
	}
	for version, recorded := range applied {
		expected, ok := known[version]
		if !ok {
			return fmt.Errorf("database schema version %d is newer than this application", version)
		}
		if recorded.Name != expected.Name || recorded.Checksum != expected.Checksum {
			return fmt.Errorf("migration %d integrity check failed", version)
		}
	}
	if requireAll {
		for _, item := range migrations {
			if _, ok := applied[item.Version]; !ok {
				return fmt.Errorf("database migration %d is not applied", item.Version)
			}
		}
	}
	return nil
}

// Ready verifies that PostgreSQL is reachable and has exactly the migration
// history expected by this binary. The schema query itself is the connectivity
// check, avoiding an additional round trip for Ping.
func (b *Backend) Ready(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	applied, err := readAppliedMigrations(ctx, b.DB)
	if err != nil {
		return err
	}
	return validateAppliedMigrations(migrations, applied, true)
}

func applyMigration(ctx context.Context, conn *sql.Conn, item migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", item.Version, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, item.SQL); err != nil {
		return fmt.Errorf("apply migration %d (%s): %w", item.Version, item.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agent_schema_migrations(version,name,checksum) VALUES($1,$2,$3)`,
		item.Version, item.Name, item.Checksum,
	); err != nil {
		return fmt.Errorf("record migration %d: %w", item.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", item.Version, err)
	}
	return nil
}
