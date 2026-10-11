package stateops

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelocationPreservesJSONAndPathBoundaries(t *testing.T) {
	for _, roots := range []struct{ from, to string }{
		{"/tmp/状态", "/tmp/恢复"},
		{"/tmp/old state", "/tmp/new\"state"},
		{"/tmp/old\"state", "/tmp/new\\state"},
	} {
		t.Run(roots.from, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, stmt := range []string{
				"CREATE TABLE sessions (id TEXT, workspace TEXT)",
				"CREATE TABLE checkpoints (id TEXT, archive_path TEXT)",
				"CREATE TABLE invocations (id TEXT, log_path TEXT)",
				"CREATE TABLE candidates (id TEXT, checks TEXT, deliverables TEXT)",
				"CREATE TABLE events (sequence INTEGER, payload TEXT)",
			} {
				if _, err := db.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
			oldPath := filepath.Join(roots.from, "sessions", "one", "log")
			newPath := filepath.Join(roots.to, "sessions", "one", "log")
			sibling := roots.from + "-sibling/log"
			body, err := json.Marshal(map[string]any{
				"checks":       []any{map[string]any{"log_path": oldPath}},
				"deliverables": []any{map[string]any{"local_path": oldPath}},
				"objective":    "Read " + oldPath, "url": "https://example.test" + oldPath,
				"sibling": map[string]any{"log_path": sibling},
				"count":   json.Number("9007199254740993"),
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, stmt := range []string{
				"INSERT INTO sessions VALUES ('one', ?)",
				"INSERT INTO checkpoints VALUES ('one', ?)",
				"INSERT INTO invocations VALUES ('one', ?)",
			} {
				if _, err := db.Exec(stmt, oldPath); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec("INSERT INTO candidates VALUES ('one', ?, ?)", string(body), string(body)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO events VALUES (1, ?)", string(body)); err != nil {
				t.Fatal(err)
			}
			if err := relocateDatabase(context.Background(), path, roots.from, roots.to); err != nil {
				t.Fatal(err)
			}
			for _, stmt := range []string{
				"SELECT workspace FROM sessions", "SELECT archive_path FROM checkpoints", "SELECT log_path FROM invocations",
			} {
				var got string
				if err := db.QueryRow(stmt).Scan(&got); err != nil || got != newPath {
					t.Fatalf("path=%q, err=%v", got, err)
				}
			}
			for _, stmt := range []string{"SELECT checks FROM candidates", "SELECT deliverables FROM candidates", "SELECT payload FROM events"} {
				var got string
				if err := db.QueryRow(stmt).Scan(&got); err != nil {
					t.Fatal(err)
				}
				var doc struct {
					Checks []struct {
						LogPath string `json:"log_path"`
					} `json:"checks"`
					Deliverables []struct {
						LocalPath string `json:"local_path"`
					} `json:"deliverables"`
					Objective string `json:"objective"`
					URL       string `json:"url"`
					Sibling   struct {
						LogPath string `json:"log_path"`
					} `json:"sibling"`
					Count json.Number `json:"count"`
				}
				if err := json.Unmarshal([]byte(got), &doc); err != nil {
					t.Fatal(err)
				}
				if doc.Checks[0].LogPath != newPath || doc.Deliverables[0].LocalPath != newPath ||
					doc.Objective != "Read "+oldPath || doc.URL != "https://example.test"+oldPath ||
					doc.Sibling.LogPath != sibling || doc.Count.String() != "9007199254740993" {
					t.Fatalf("relocation changed unrelated content: %s", got)
				}
			}
			// A later invalid JSON row must roll back earlier path updates.
			if _, err := db.Exec("UPDATE events SET payload = '{'"); err != nil {
				t.Fatal(err)
			}
			if err := relocateDatabase(context.Background(), path, roots.to, roots.to+"-again"); err == nil {
				t.Fatal("corrupt JSON accepted")
			}
			var workspace string
			if err := db.QueryRow("SELECT workspace FROM sessions").Scan(&workspace); err != nil || workspace != newPath {
				t.Fatalf("partial relocation committed: %q %v", workspace, err)
			}
		})
	}
}

func TestRelocationRejectsOutsideRuntimePath(t *testing.T) {
	moved, owned := relocatePath("/tmp/state-sibling/log", "/tmp/state", "/tmp/restored")
	if owned || !strings.Contains(moved, "state-sibling") {
		t.Fatalf("sibling was relocated: %s", moved)
	}
}
