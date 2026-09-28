package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const migrationTable = "turnyard_schema_migrations"

type schemaMigration struct {
	version  int
	name     string
	sql      string
	checksum string
}

func loadMigrations() ([]schemaMigration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	steps := make([]schemaMigration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			return nil, fmt.Errorf("invalid schema migration file %q", entry.Name())
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		version, parseErr := strconv.Atoi(prefix)
		if !ok || parseErr != nil || version < 1 {
			return nil, fmt.Errorf("invalid schema migration version in %q", entry.Name())
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		steps = append(steps, schemaMigration{version: version, name: entry.Name(), sql: string(body), checksum: hex.EncodeToString(sum[:])})
	}
	slices.SortFunc(steps, func(a, b schemaMigration) int { return a.version - b.version })
	if len(steps) == 0 {
		return nil, fmt.Errorf("schema migration baseline is missing")
	}
	for i, step := range steps {
		if step.version != i+1 {
			return nil, fmt.Errorf("schema migrations must be consecutive from version 1")
		}
	}
	return steps, nil
}

// migrateSchema applies reviewed SQL in SQLite transactions. Unversioned state
// is adopted once into the frozen v1 baseline; later opens never auto-migrate.
func migrateSchema(ctx context.Context, db *sql.DB, databasePath string) error {
	steps, err := loadMigrations()
	if err != nil {
		return err
	}
	marked, err := migrationTableExists(ctx, db)
	if err != nil {
		return err
	}
	current := 0
	if marked {
		rows, err := db.QueryContext(ctx, "SELECT version, checksum FROM "+migrationTable+" ORDER BY version")
		if err != nil {
			return err
		}
		for rows.Next() {
			var version int
			var checksum string
			if err := rows.Scan(&version, &checksum); err != nil {
				return errors.Join(err, rows.Close())
			}
			if version != current+1 || version > len(steps) || checksum != steps[version-1].checksum {
				_ = rows.Close()
				return fmt.Errorf("unknown or modified schema migration version %d", version)
			}
			current = version
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if current == 0 {
			return fmt.Errorf("schema migration table has no version; inspect state before retrying")
		}
	}
	if current == len(steps) {
		return verifySchemaColumns(ctx, db, steps)
	}
	var userTables int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name != ?", migrationTable).Scan(&userTables); err != nil {
		return err
	}
	legacy := !marked && userTables > 0
	if legacy || current > 0 {
		if err := backupBeforeMigration(ctx, db, databasePath, current); err != nil {
			return fmt.Errorf("back up state database before migration: %w", err)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+migrationTable+" (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)"); err != nil {
		return err
	}
	if legacy {
		if err := adoptUnversioned(ctx, tx, steps[0].sql); err != nil {
			return fmt.Errorf("adopt unversioned schema: %w", err)
		}
		if err := recordMigration(ctx, tx, steps[0]); err != nil {
			return err
		}
		current = 1
	}
	for _, step := range steps[current:] {
		if _, err := tx.ExecContext(ctx, step.sql); err != nil {
			return fmt.Errorf("apply schema migration %s: %w", step.name, err)
		}
		if err := recordMigration(ctx, tx, step); err != nil {
			return err
		}
	}
	if err := verifySchemaColumns(ctx, tx, steps); err != nil {
		return err
	}
	violations, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	if violations.Next() {
		_ = violations.Close()
		return fmt.Errorf("state database has foreign-key violations after migration")
	}
	if err := errors.Join(violations.Err(), violations.Close()); err != nil {
		return err
	}
	return tx.Commit()
}

func migrationTableExists(ctx context.Context, db *sql.DB) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", migrationTable).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func recordMigration(ctx context.Context, tx *sql.Tx, step schemaMigration) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO "+migrationTable+"(version,checksum,applied_at) VALUES(?,?,?)",
		step.version, step.checksum, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func backupBeforeMigration(ctx context.Context, db *sql.DB, databasePath string, version int) error {
	backup := fmt.Sprintf("%s.pre-v%03d-%d.bak", databasePath, version, time.Now().UnixNano())
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", backup); err != nil {
		return err
	}
	if err := os.Chmod(backup, 0o600); err != nil {
		return err
	}
	copy, err := sql.Open("sqlite", backup)
	if err != nil {
		return err
	}
	copy.SetMaxOpenConns(1)
	var check string
	checkErr := copy.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check)
	if err := errors.Join(checkErr, copy.Close()); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("schema migration backup failed integrity check: %s", check)
	}
	return nil
}

// v1 adoption covers the additive columns used by previous Turnyard builds.
// Its target is the frozen SQL baseline, independent of future Ent schemas.
func adoptUnversioned(ctx context.Context, tx *sql.Tx, baseline string) error {
	for _, required := range []string{"sessions", "tasks"} {
		exists, err := tableExists(ctx, tx, required)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("unversioned database is not a known Turnyard schema: missing %s", required)
		}
	}
	additions := map[string][]struct{ name, ddl string }{
		"sessions": {
			{"idempotency_key", "text NULL"}, {"creation_digest", "text NULL"},
			{"completed_at", "real NULL"}, {"cancelled_at", "real NULL"}, {"cancel_reason", "text NULL"},
			{"parent_session_id", "text NULL"}, {"parent_task_id", "text NULL"},
			{"parent_invocation_id", "text NULL"}, {"delegate_agent_id", "text NULL"},
			{"delegation_request_digest", "text NULL"},
		},
		"candidates": {{"deliverables", "text NOT NULL DEFAULT ('[]')"}},
	}
	for table, columns := range additions {
		exists, err := tableExists(ctx, tx, table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		have, err := tableColumns(ctx, tx, table)
		if err != nil {
			return err
		}
		for _, column := range columns {
			if _, ok := have[column.name]; ok {
				continue
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %q ADD COLUMN %q %s", table, column.name, column.ddl)); err != nil {
				return err
			}
		}
	}
	create := strings.ReplaceAll(baseline, "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS ")
	create = strings.ReplaceAll(create, "CREATE UNIQUE INDEX ", "CREATE UNIQUE INDEX IF NOT EXISTS ")
	create = strings.ReplaceAll(create, "CREATE INDEX ", "CREATE INDEX IF NOT EXISTS ")
	if _, err := tx.ExecContext(ctx, create); err != nil {
		return err
	}
	old, err := tableExists(ctx, tx, "task_repositories")
	if err != nil {
		return err
	}
	if old {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_bases(id,task_id,repo_id,base_commit)
          SELECT task_id || ':' || repo_id,task_id,repo_id,base_commit FROM task_repositories`); err != nil {
			return err
		}
	}
	return nil
}

type schemaQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func tableExists(ctx context.Context, q schemaQueryer, table string) (bool, error) {
	var name string
	err := q.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func tableColumns(ctx context.Context, q schemaQueryer, table string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]string{}
	for rows.Next() {
		var cid, notNull, primary int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			return nil, err
		}
		columns[name] = strings.ToLower(kind)
	}
	return columns, rows.Err()
}

func verifySchemaColumns(ctx context.Context, target schemaQueryer, steps []schemaMigration) error {
	reference, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer reference.Close()
	reference.SetMaxOpenConns(1)
	for _, step := range steps {
		if _, err := reference.ExecContext(ctx, step.sql); err != nil {
			return fmt.Errorf("reference schema migration %s: %w", step.name, err)
		}
	}
	rows, err := reference.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return errors.Join(err, rows.Close())
		}
		tables = append(tables, table)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, table := range tables {
		want, err := tableColumns(ctx, reference, table)
		if err != nil {
			return err
		}
		got, err := tableColumns(ctx, target, table)
		if err != nil {
			return err
		}
		if len(want) != len(got) {
			return fmt.Errorf("schema table %s has unexpected columns", table)
		}
		for name, kind := range want {
			if got[name] != kind {
				return fmt.Errorf("schema table %s column %s differs from migration baseline", table, name)
			}
		}
	}
	return nil
}
