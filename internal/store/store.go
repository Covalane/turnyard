package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Covalane/turnyard/internal/store/ent"
	_ "modernc.org/sqlite"
)

// Store is the only persistence boundary visible to the engine. Its Ent client
// and migration SQL stay private to this package.
type Store struct {
	Root   string
	client *ent.Client
}

// DatabaseFileName is the on-disk state database used by the store and offline backups.
const DatabaseFileName = "turnyard.sqlite3"

func OpenStore(ctx context.Context, root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(abs, DatabaseFileName))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON; PRAGMA busy_timeout=30000; PRAGMA journal_mode=WAL"); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := migrateSchema(ctx, db, filepath.Join(abs, DatabaseFileName)); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	return &Store{Root: abs, client: client}, nil
}

func (s *Store) Close() error { return s.client.Close() }
func now() float64            { return float64(time.Now().UnixNano()) / 1e9 }
func jsonText(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type SessionRow struct {
	ID                      string         `json:"id"`
	Spec                    string         `json:"spec"`
	Environment             string         `json:"environment"`
	EnvironmentDigest       string         `json:"environment_digest"`
	Workspace               string         `json:"workspace"`
	Status                  string         `json:"status"`
	NativeID                OptionalString `json:"native_id"`
	CreatedAt               float64        `json:"created_at"`
	CompletedAt             OptionalFloat  `json:"completed_at"`
	CancelledAt             OptionalFloat  `json:"cancelled_at"`
	CancelReason            OptionalString `json:"cancel_reason"`
	ParentSessionID         OptionalString `json:"parent_session_id"`
	ParentTaskID            OptionalString `json:"parent_task_id"`
	ParentInvocationID      OptionalString `json:"parent_invocation_id"`
	DelegateAgentID         OptionalString `json:"delegate_agent_id"`
	DelegationRequestDigest OptionalString `json:"delegation_request_digest"`
}

// TurnRow is a persisted user or agent turn exposed without Ent types.
type TurnRow struct {
	ID           string  `json:"id"`
	TaskID       string  `json:"task_id"`
	Sequence     int     `json:"sequence"`
	Input        string  `json:"input"`
	Status       string  `json:"status"`
	InvocationID string  `json:"invocation_id"`
	CreatedAt    float64 `json:"created_at"`
}

// EventRow is an ordered state change in a session.
type EventRow struct {
	Sequence  int64   `json:"sequence"`
	SessionID string  `json:"session_id"`
	TaskID    *string `json:"task_id"`
	Type      string  `json:"type"`
	Payload   string  `json:"payload"`
	CreatedAt float64 `json:"created_at"`
}

type TaskRow struct {
	ID             string         `json:"id"`
	SessionID      string         `json:"session_id"`
	Sequence       int            `json:"sequence"`
	IdempotencyKey string         `json:"idempotency_key"`
	Spec           string         `json:"spec"`
	SpecDigest     string         `json:"spec_digest"`
	Status         string         `json:"status"`
	CandidateID    OptionalString `json:"candidate_id"`
	ErrorCode      OptionalString `json:"error_code"`
	CreatedAt      float64        `json:"created_at"`
}

type CandidateRow struct {
	ID           string  `json:"id"`
	TaskID       string  `json:"task_id"`
	Vector       string  `json:"vector"`
	Digest       string  `json:"digest"`
	Checks       string  `json:"checks"`
	Deliverables string  `json:"deliverables"`
	Status       string  `json:"status"`
	CreatedAt    float64 `json:"created_at"`
}
type InvocationRow struct {
	ID             string         `json:"id"`
	TurnID         string         `json:"turn_id"`
	Runtime        string         `json:"runtime"`
	Model          string         `json:"model"`
	SandboxBackend string         `json:"sandbox_backend"`
	Status         string         `json:"status"`
	ExitCode       OptionalInt    `json:"exit_code"`
	NativeID       OptionalString `json:"native_id"`
	LogPath        OptionalString `json:"log_path"`
	StartedAt      float64        `json:"started_at"`
	EndedAt        OptionalFloat  `json:"ended_at"`
}
