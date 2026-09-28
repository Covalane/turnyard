package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
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
