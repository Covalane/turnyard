package engine

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/Covalane/turnyard/internal/artifacts"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/inputs"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
	"github.com/Covalane/turnyard/internal/sandbox"
	"github.com/Covalane/turnyard/internal/store"
)

type CheckResult struct {
	ID           string   `json:"id"`
	SpecDigest   string   `json:"spec_digest"`
	Repositories []string `json:"repositories"`
	ExitCode     int      `json:"exit_code"`
	TimedOut     bool     `json:"timed_out"`
	Passed       bool     `json:"passed"`
	LogPath      string   `json:"log_path"`
}

func (s *Service) runChecks(ctx context.Context, tid, sid, candidateID string, work contracts.WorkSpec, env contracts.EnvironmentSpec, workspace string, backend sandbox.SandboxBackend) ([]CheckResult, error) {
	catalog := map[string]contracts.CheckSpec{}
	for _, c := range env.Checks {
		catalog[c.ID] = c
	}
	out := []CheckResult{}
	for _, id := range work.Checks {
		def := catalog[id]
		readonly := make([]contracts.ScopeRepo, 0, len(def.Repositories))
		for _, repositoryID := range def.Repositories {
			readonly = append(readonly, contracts.ScopeRepo{ID: repositoryID, Mode: contracts.ScopeRead})
		}
		timeout := def.TimeoutSeconds
		if timeout == 0 {
			timeout = 120
		}
		name := "check-" + tid + "-" + contracts.NewID("run")
		logPath := filepath.Join(filepath.Dir(workspace), "logs", name+".txt")
		result, err := backend.Run(ctx, sandbox.SandboxRun{Name: "ty-" + name, Sandbox: env.Sandbox, Workspace: workspace, Scope: readonly,
			State: filepath.Join(filepath.Dir(workspace), "check-state"), ArtifactDir: filepath.Join(filepath.Dir(workspace), "outputs", candidateID), ArtifactReadOnly: true,
			EntryPoint: def.Argv[0], Command: def.Argv[1:], Timeout: time.Duration(timeout) * time.Second, LogPath: logPath})
		if err != nil {
			return out, err
		}
		record := CheckResult{
			ID: id, SpecDigest: contracts.Digest(def), Repositories: def.Repositories,
			ExitCode: result.ExitCode, TimedOut: result.TimedOut,
			Passed: result.ExitCode == 0 && !result.TimedOut, LogPath: logPath,
		}
		out = append(out, record)
		err = s.Store.AppendEvent(ctx, sid, tid, lifecycle.EventCheckCompleted, record)
		if err != nil {
			return out, err
		}
		observe.Log.InfoContext(ctx, "check completed", "session_id", sid, "task_id", tid, "check_id", id, "passed", record.Passed, "exit_code", record.ExitCode, "timed_out", record.TimedOut, "log_path", logPath)
	}
	return out, nil
}
func (s *Service) VerifyCandidate(ctx context.Context, tid string) (CandidateVerificationResult, error) {
	task, err := s.Store.Task(ctx, tid)
	if err != nil {
		return CandidateVerificationResult{}, err
	}
	release, err := s.reserveSession(task.SessionID)
	if err != nil {
		return CandidateVerificationResult{}, err
	}
	defer release()
	if task.Status != lifecycle.Failed || !task.CandidateID.Present {
		return CandidateVerificationResult{}, fault.New(fault.CodeInvalidTransition, "verification requires failed task with candidate")
	}
	if task.ErrorCode.Present && task.ErrorCode.Value == string(fault.CodeHandoffUnavailable) {
		return CandidateVerificationResult{}, fault.New(fault.CodeInvalidTransition, "handoff failure requires an agent retry")
	}
	row, err := s.Store.Session(ctx, task.SessionID)
	if err != nil {
		return CandidateVerificationResult{}, err
	}
	session, env, err := parseSessionRow(row)
	if err != nil {
		return CandidateVerificationResult{}, err
	}
	releaseCapacity, err := s.Capacity.Reserve(ctx, env.Sandbox, CapacityRegular)
	if err != nil {
		return CandidateVerificationResult{}, err
	}
	defer releaseCapacity()
	var work contracts.WorkSpec
	if err := json.Unmarshal([]byte(task.Spec), &work); err != nil {
		return CandidateVerificationResult{}, err
	}
	candidate, err := s.Store.Candidate(ctx, task.CandidateID.Value)
	if err != nil {
		return CandidateVerificationResult{}, err
	}
	var vector map[string]gitstate.RepoVersion
	if err := json.Unmarshal([]byte(candidate.Vector), &vector); err != nil {
		return CandidateVerificationResult{}, err
	}
	actual, err := gitstate.WorkspaceStatus(ctx, row.Workspace, session.Repositories)
	if err != nil {
		return CandidateVerificationResult{}, err
	}
	for id, version := range vector {
		status := actual[id]
		if status.Head != version.Head || status.Branch != version.Branch || status.Dirty {
			return CandidateVerificationResult{}, fault.New(fault.CodeCandidateStale, "repository %s differs from candidate", id)
		}
		repo, _, err := gitstate.OpenRepo(filepath.Join(row.Workspace, id))
		if err != nil {
			return CandidateVerificationResult{}, err
		}
		head, err := repo.Head()
		if err != nil {
			return CandidateVerificationResult{}, err
		}
		commit, err := repo.CommitObject(head.Hash())
		if err != nil {
			return CandidateVerificationResult{}, err
		}
		if commit.TreeHash.String() != version.Tree {
			return CandidateVerificationResult{}, fault.New(fault.CodeCandidateStale, "repository %s tree differs from candidate", id)
		}
	}
	if err := s.verifyCheckpointHead(ctx, task.SessionID, session, row.Workspace); err != nil {
		return CandidateVerificationResult{}, err
	}
	if err := inputs.Verify(ctx, filepath.Dir(row.Workspace), work); err != nil {
		return CandidateVerificationResult{}, err
	}
	var prior []artifacts.Result
	if err := json.Unmarshal([]byte(candidate.Deliverables), &prior); err != nil {
		return CandidateVerificationResult{}, fault.Wrap(fault.CodeCandidateCorrupt, "decode deliverables", err, "candidate deliverables are invalid")
	}
	legacy := legacyCandidateDigest(vector, candidate.Digest)
	if !legacy && candidateDigest(vector, work, prior) != candidate.Digest {
		return CandidateVerificationResult{}, fault.New(fault.CodeCandidateCorrupt, "candidate digest mismatch")
	}
	localPaths := artifacts.SealedPaths(filepath.Dir(row.Workspace), candidate.ID, work.Deliverables)
	deliverables, err := artifacts.Inspect(ctx, row.Workspace, session.Repositories, vector, work.Deliverables, artifacts.ClaimsFromResults(prior), localPaths, s.ArtifactVerifier)
	if err != nil {
		return CandidateVerificationResult{}, err
	}
	if !legacy && candidateDigest(vector, work, deliverables) != candidate.Digest {
		return CandidateVerificationResult{}, fault.New(fault.CodeCandidateStale, "sealed outputs differ from candidate")
	}
	sandbox, err := s.BackendFactory(env.Sandbox.Backend)
	if err != nil {
		return CandidateVerificationResult{}, err
	}
	if err := s.Store.StartReverify(ctx, task.SessionID, tid, candidate.ID, candidate.Digest); err != nil {
		return CandidateVerificationResult{}, err
	}
	checks, err := s.runChecks(ctx, tid, task.SessionID, candidate.ID, work, env, row.Workspace, sandbox)
	if err != nil {
		return CandidateVerificationResult{}, s.recordReverifyError(ctx, task.SessionID, tid, candidate.ID, err)
	}
	if checksPassed(checks) {
		deliverables, err = s.deliverOutputs(ctx, task.SessionID, tid, env, work, deliverables)
		if err != nil {
			return CandidateVerificationResult{}, s.recordReverifyError(ctx, task.SessionID, tid, candidate.ID, err)
		}
	}
	final := lifecycle.Verified
	for _, c := range checks {
		if !c.Passed {
			final = lifecycle.Failed
		}
	}
	if !artifacts.Pass(deliverables) {
		final = lifecycle.Failed
	}
	var code fault.Code
	if final == lifecycle.Failed {
		code = fault.CodeCheckFailed
		for _, item := range deliverables {
			if item.Status != artifacts.StatusPresent {
				code = fault.Code(item.ErrorCode)
				break
			}
		}
	}
	err = s.Store.FinishCandidate(ctx, store.CandidateOutcome{SessionID: task.SessionID, TaskID: tid, CandidateID: candidate.ID, Status: final, ErrorCode: code, Checks: checks, Deliverables: deliverables, Reverify: true})
	return CandidateVerificationResult{TaskID: tid, CandidateID: candidate.ID, Status: final, Checks: checks, Deliverables: deliverables}, err
}

func (s *Service) recordReverifyError(ctx context.Context, sessionID, taskID, candidateID string, cause error) error {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := s.Store.ErrorReverify(persistCtx, sessionID, taskID, candidateID, cause); err != nil {
		combined := fault.Wrap(fault.CodeInternalError, "persist reverify outcome", errors.Join(err, cause), "candidate verification failure could not be recorded")
		observe.LogFailure(ctx, "candidate verification outcome persistence failed", combined, "candidate_id", candidateID)
		return combined
	}
	return cause
}
