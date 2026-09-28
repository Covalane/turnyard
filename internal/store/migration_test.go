package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Covalane/turnyard/internal/store/ent"
)

// A workspace created before the Ent migration must preserve task lineage and
// repository bases. The legacy SQL appears only in this compatibility fixture.
func TestMigrateLegacyTaskBases(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "turnyard.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
        CREATE TABLE sessions (id TEXT PRIMARY KEY,spec TEXT NOT NULL,environment TEXT NOT NULL,
            environment_digest TEXT NOT NULL,workspace TEXT NOT NULL,status TEXT NOT NULL,
            native_id TEXT,created_at REAL NOT NULL);
        CREATE TABLE tasks (id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id),
            sequence INTEGER NOT NULL,idempotency_key TEXT NOT NULL,spec TEXT NOT NULL,
            spec_digest TEXT NOT NULL,status TEXT NOT NULL,candidate_id TEXT,error_code TEXT,
            created_at REAL NOT NULL,UNIQUE(session_id,idempotency_key),UNIQUE(session_id,sequence));
        CREATE TABLE task_repositories (task_id TEXT NOT NULL REFERENCES tasks(id),repo_id TEXT NOT NULL,
            base_commit TEXT NOT NULL,PRIMARY KEY(task_id,repo_id));
        INSERT INTO sessions VALUES('ses_old','{}','{}','sha','/tmp/work','ready',NULL,1.0);
        INSERT INTO tasks VALUES('work_old','ses_old',1,'key','{}','sha','verified',NULL,NULL,2.0);
        INSERT INTO task_repositories VALUES('work_old','api','old-head');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		store, err := OpenStore(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		bases, err := store.TaskBases(context.Background(), "work_old")
		if err != nil {
			t.Fatal(err)
		}
		if got := bases["api"]; got != "old-head" {
			t.Fatalf("migration round %d: base=%q", i, got)
		}
		tasks, err := store.Tasks(context.Background(), "ses_old")
		if err != nil || len(tasks) != 1 || tasks[0].ID != "work_old" {
			t.Fatalf("tasks=%v err=%v", tasks, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationVersionAndBackup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	first, err := OpenStore(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, DatabaseFileName))
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("SELECT version FROM " + migrationTable).Scan(&version); err != nil || version != 1 {
		t.Fatalf("fresh database version=%d err=%v", version, err)
	}
	if _, err := db.Exec("DROP TABLE " + migrationTable); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	adopted, err := OpenStore(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := adopted.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	backups := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bak") {
			backups++
			info, err := entry.Info()
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("unsafe database backup: %s %v", entry.Name(), err)
			}
		}
	}
	if backups != 1 {
		t.Fatalf("unversioned adoption should create one backup, got %d", backups)
	}
	reopened, err := OpenStore(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	backups = 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bak") {
			backups++
		}
	}
	if backups != 1 {
		t.Fatalf("versioned reopen created extra backup: %d", backups)
	}
}

func TestMigrationRejectsNewerAndModifiedVersions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := OpenStore(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, DatabaseFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, change := range []string{
		"UPDATE " + migrationTable + " SET checksum='tampered' WHERE version=1",
		"UPDATE " + migrationTable + " SET version=2 WHERE version=1",
	} {
		if _, err := db.Exec(change); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenStore(ctx, root); err == nil {
			t.Fatalf("invalid version was accepted after %q", change)
		}
		if _, err := db.Exec("UPDATE "+migrationTable+" SET version=1, checksum=?", mustBaselineChecksum(t)); err != nil {
			t.Fatal(err)
		}
	}
}

func mustBaselineChecksum(t *testing.T) string {
	t.Helper()
	steps, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return steps[0].checksum
}

func TestFailedMigrationLeavesUnversionedDataAndBackup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, DatabaseFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE unrelated(id TEXT PRIMARY KEY); INSERT INTO unrelated VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(ctx, root); err == nil {
		t.Fatal("unknown legacy schema was adopted")
	}
	db, err = sql.Open("sqlite", filepath.Join(root, DatabaseFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT id FROM unrelated").Scan(&value); err != nil || value != "keep" {
		t.Fatalf("legacy row was lost: %q %v", value, err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name=?", migrationTable).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration left metadata: %d %v", count, err)
	}
	backups, err := filepath.Glob(filepath.Join(root, "*.bak"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("failed migration backup=%v err=%v", backups, err)
	}
}

func TestEntSchemaMatchesVersionedMigrations(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	defer client.Close()
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	steps, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySchemaColumns(ctx, db, steps); err != nil {
		t.Fatal(fmt.Errorf("Ent schema and reviewed migrations differ: %w", err))
	}
	reference, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	reference.SetMaxOpenConns(1)
	defer reference.Close()
	for _, step := range steps {
		if _, err := reference.ExecContext(ctx, step.sql); err != nil {
			t.Fatal(err)
		}
	}
	want := schemaDefinitions(t, reference)
	got := schemaDefinitions(t, db)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("Ent tables or indexes differ from reviewed migration SQL")
	}
}

func schemaDefinitions(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT type || ':' || name, sql FROM sqlite_master
        WHERE type IN ('table', 'index') AND name NOT LIKE 'sqlite_%' AND sql IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	definitions := make(map[string]string)
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		definitions[name] = ddl
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return definitions
}

func TestEventEncodingFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateSession(ctx, CreateSessionInput{
		ID: "ses_test", SpecJSON: "{}", EnvironmentJSON: "{}", EnvironmentDigest: "digest", Workspace: "workspace",
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Events(ctx, "ses_test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "ses_test", "", "invalid", make(chan int)); err == nil {
		t.Fatal("unencodable payload should return an error")
	}
	after, err := store.Events(ctx, "ses_test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("failed transaction changed event count: before=%d after=%d", len(before), len(after))
	}
}
