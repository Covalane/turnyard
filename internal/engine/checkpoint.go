package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
	"github.com/Covalane/turnyard/internal/store"
)

func (s *Service) verifyCheckpointHead(ctx context.Context, sid string, session contracts.SessionSpec, workspace string) error {
	metadata, found, err := s.Store.LatestCheckpointMetadata(ctx, sid)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	var meta struct {
		Repositories map[string]gitstate.RepoStatus `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(metadata), &meta); err != nil {
		return err
	}
	actual, err := gitstate.WorkspaceStatus(ctx, workspace, session.Repositories)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, meta.Repositories) {
		return fault.New(fault.CodeWorkspaceDrift, "workspace differs from last checkpoint")
	}
	return nil
}
func (s *Service) saveCheckpoint(ctx context.Context, sid, tid string, session contracts.SessionSpec, workspace, state, native string) (string, error) {
	if native != "" {
		if info, err := os.Lstat(state); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fault.New(fault.CodeNativeStateMissing, "native agent state is unavailable for checkpoint")
		}
	} else if err := agents.EnsureStateDirectory(state, state); err != nil {
		return "", err
	}
	id, err := contracts.NewID("cp")
	if err != nil {
		return "", err
	}
	archive := filepath.Join(s.Store.Root, "checkpoints", id+".tar.gz")
	hash, err := gitstate.CheckpointArchive(ctx, workspace, state, archive)
	if err != nil {
		return "", err
	}
	status, err := gitstate.WorkspaceStatus(ctx, workspace, session.Repositories)
	if err != nil {
		return "", err
	}
	meta := map[string]any{"sha256": hash, "repositories": status, "native_id": native}
	metadata, err := contracts.JSONText(meta)
	if err != nil {
		return "", err
	}
	err = s.Store.SaveCheckpoint(ctx, store.CheckpointRecord{ID: id, SessionID: sid, TaskID: tid, Archive: archive, Metadata: metadata, SHA256: hash})
	if err == nil {
		observe.Log.InfoContext(ctx, "checkpoint saved", "session_id", sid, "task_id", tid, "checkpoint_id", id, "sha256", hash)
	}
	return id, err
}
func (s *Service) Restore(ctx context.Context, id string) (RestoreResult, error) {
	checkpoint, err := s.Store.Checkpoint(ctx, id)
	if err != nil {
		return RestoreResult{}, err
	}
	row, err := s.Store.Session(ctx, checkpoint.SessionID)
	if err != nil {
		return RestoreResult{}, err
	}
	if row.Status != lifecycle.Ready && row.Status != lifecycle.Paused {
		return RestoreResult{}, fault.New(fault.CodeInvalidTransition, "session in %s state cannot restore checkpoints", row.Status)
	}
	session, _, err := parseSessionRow(row)
	if err != nil {
		return RestoreResult{}, err
	}
	var meta struct {
		SHA256       string                         `json:"sha256"`
		Repositories map[string]gitstate.RepoStatus `json:"repositories"`
	}
	if err := json.Unmarshal([]byte(checkpoint.Metadata), &meta); err != nil {
		return RestoreResult{}, err
	}
	state := filepath.Join(filepath.Dir(row.Workspace), gitstate.AgentStateDirName)
	if err := gitstate.RestoreArchive(ctx, checkpoint.Archive, meta.SHA256, row.Workspace, state); err != nil {
		return RestoreResult{}, err
	}
	actual, err := gitstate.WorkspaceStatus(ctx, row.Workspace, session.Repositories)
	if err != nil {
		return RestoreResult{}, err
	}
	if !reflect.DeepEqual(actual, meta.Repositories) {
		return RestoreResult{}, fault.New(fault.CodeCheckpointCorrupt, "restored Git state differs")
	}
	err = s.Store.AppendEvent(ctx, checkpoint.SessionID, checkpoint.TaskID, lifecycle.EventCheckpointRestored, map[string]any{"checkpoint_id": id})
	if err != nil {
		return RestoreResult{}, err
	}
	observe.Log.InfoContext(ctx, "checkpoint restored", "session_id", checkpoint.SessionID, "task_id", checkpoint.TaskID, "checkpoint_id", id)
	return RestoreResult{CheckpointID: id, Workspace: row.Workspace, Restored: true}, nil
}
