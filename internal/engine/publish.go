package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Covalane/turnyard/internal/artifacts"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
)

type publishTarget struct {
	taskID  string
	repoID  string
	url     string
	version gitstate.RepoVersion
}

// PublishSession publishes verified feature branches after the finite session
// is completed. It never pushes the source branch or accepts a force update.
func (s *Service) PublishSession(ctx context.Context, sid string) (SessionPublicationResult, error) {
	ctx = observe.WithIDs(ctx, observe.IDs{SessionID: sid})
	row, err := s.Store.Session(ctx, sid)
	if err != nil {
		return SessionPublicationResult{}, err
	}
	if row.Status != lifecycle.Completed {
		return SessionPublicationResult{}, fault.New(fault.CodeInvalidTransition, "session must be completed before publication")
	}
	var session contracts.SessionSpec
	var env contracts.EnvironmentSpec
	if err := json.Unmarshal([]byte(row.Spec), &session); err != nil {
		return SessionPublicationResult{}, fault.Wrap(fault.CodeSessionCorrupt, "decode session", err, "stored session is invalid")
	}
	if err := json.Unmarshal([]byte(row.Environment), &env); err != nil {
		return SessionPublicationResult{}, fault.Wrap(fault.CodeSessionCorrupt, "decode environment", err, "stored environment is invalid")
	}
	if env.Git.RemoteWrites != contracts.GitRemoteWritesPush {
		return SessionPublicationResult{}, fault.New(fault.CodeInvalidTransition, "remote writes are disabled for this session")
	}
	sources := make(map[string]contracts.RepositorySpec, len(session.Repositories))
	for _, source := range session.Repositories {
		sources[source.ID] = source
	}
	tasks, err := s.Store.Tasks(ctx, sid)
	if err != nil {
		return SessionPublicationResult{}, err
	}
	targets := []publishTarget{}
	for _, task := range tasks {
		if task.Status != lifecycle.Verified || !task.CandidateID.Present {
			return SessionPublicationResult{}, fault.New(fault.CodeInvalidTransition, "task %s has no verified candidate", task.ID)
		}
		candidate, err := s.Store.Candidate(ctx, task.CandidateID.Value)
		if err != nil {
			return SessionPublicationResult{}, err
		}
		if candidate.TaskID != task.ID || candidate.Status != lifecycle.Verified {
			return SessionPublicationResult{}, fault.New(fault.CodeCandidateStale, "task %s candidate is not verified", task.ID)
		}
		var work contracts.WorkSpec
		var vector map[string]gitstate.RepoVersion
		if err := json.Unmarshal([]byte(task.Spec), &work); err != nil {
			return SessionPublicationResult{}, fault.Wrap(fault.CodeSessionCorrupt, "decode work", err, "task %s specification is invalid", task.ID)
		}
		if err := json.Unmarshal([]byte(candidate.Vector), &vector); err != nil {
			return SessionPublicationResult{}, fault.Wrap(fault.CodeCandidateCorrupt, "decode vector", err, "task %s candidate is invalid", task.ID)
		}
		var prior []artifacts.Result
		if err := json.Unmarshal([]byte(candidate.Deliverables), &prior); err != nil {
			return SessionPublicationResult{}, fault.Wrap(fault.CodeCandidateCorrupt, "decode deliverables", err, "task %s candidate is invalid", task.ID)
		}
		if !legacyCandidateDigest(vector, candidate.Digest) && candidateDigest(vector, work, prior) != candidate.Digest {
			return SessionPublicationResult{}, fault.New(fault.CodeCandidateCorrupt, "task %s candidate digest is invalid", task.ID)
		}
		for _, scope := range work.Scope.Repositories {
			if scope.Mode != contracts.ScopeWrite {
				continue
			}
			source, exists := sources[scope.ID]
			version, hasVersion := vector[scope.ID]
			if !exists || !hasVersion || version.Branch != "feature/"+task.ID {
				return SessionPublicationResult{}, fault.New(fault.CodeCandidateCorrupt, "task %s repository %s has no matching feature version", task.ID, scope.ID)
			}
			target := source.PushURL
			if target == "" && source.Type == contracts.RepositorySourceRemoteGit {
				target = source.URL
			}
			if target == "" {
				return SessionPublicationResult{}, fault.New(fault.CodeInvalidSpec, "repository %s needs pushUrl to publish", scope.ID)
			}
			targets = append(targets, publishTarget{task.ID, scope.ID, target, version})
		}
	}
	result := SessionPublicationResult{SchemaVersion: contracts.SessionPublicationVersion, SessionID: sid, Status: SessionPublicationPublished, Features: make([]PublishedFeature, 0, len(targets))}
	record := func(taskID, event string, payload map[string]any) error {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return s.Store.AppendEvent(writeCtx, sid, taskID, event, payload)
	}
	for _, target := range targets {
		item := PublishedFeature{TaskID: target.taskID, Repository: target.repoID, Branch: target.version.Branch, Head: target.version.Head}
		pushed, err := gitstate.PublishFeature(ctx, row.Workspace, target.repoID, target.url, target.version)
		if err != nil {
			item.Status = FeaturePublicationFailed
			item.ErrorCode = string(fault.CodeOf(err))
			result.Status = SessionPublicationPartial
			if eventErr := record(target.taskID, lifecycle.EventFeaturePublishFailed, map[string]any{"repository": target.repoID, "branch": item.Branch, "head": item.Head, "code": item.ErrorCode}); eventErr != nil {
				return SessionPublicationResult{}, eventErr
			}
			observe.Log.ErrorContext(ctx, "feature publication failed", "taskId", target.taskID, "repository", target.repoID, "code", item.ErrorCode, "origin", fault.Origin(err), "stack", fault.Stack(err))
		} else if pushed {
			item.Status = FeaturePublicationPublished
			if err := record(target.taskID, lifecycle.EventFeaturePublished, map[string]any{"repository": target.repoID, "branch": item.Branch, "head": item.Head}); err != nil {
				return SessionPublicationResult{}, err
			}
			observe.Log.InfoContext(ctx, "feature published", "taskId", target.taskID, "repository", target.repoID, "head", item.Head)
		} else {
			item.Status = FeaturePublicationAlreadyPublished
		}
		result.Features = append(result.Features, item)
	}
	return result, nil
}
