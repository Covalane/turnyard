package engine

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/artifacts"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
	"github.com/Covalane/turnyard/internal/sandbox"
	"github.com/Covalane/turnyard/internal/store"
)

// turnExecution keeps one invocation's immutable inputs together. Each stage
// owns one effect: prepare Git, invoke the agent, then record the candidate.
type turnExecution struct {
	task         store.TaskRow
	row          store.SessionRow
	session      contracts.SessionSpec
	env          contracts.EnvironmentSpec
	work         contracts.WorkSpec
	agent        contracts.AgentSpec
	backend      sandbox.SandboxBackend
	workspace    string
	state        string
	turnID       string
	invocationID string
	logPath      string
	reply        string
	timeout      time.Duration
}

func (s *Service) performTurn(ctx context.Context, input turnExecution, agentAttempted *bool) (TaskRunResult, error) {
	bases, heads, err := s.prepareFeature(ctx, input)
	if err != nil {
		return TaskRunResult{}, err
	}
	agentResult, err := s.invokeAgent(ctx, input, agentAttempted)
	if err != nil {
		return TaskRunResult{}, err
	}
	children, err := s.Store.DelegatedSessions(ctx, input.task.ID)
	if err != nil {
		return TaskRunResult{}, err
	}
	for _, child := range children {
		if child.Status != lifecycle.Completed && agentResult.NeedsInput == "" {
			agentResult.NeedsInput = "A managed child task is unfinished; inspect its status and continue this task after it is resolved."
		}
	}
	return s.finalizeTurn(ctx, input, bases, heads, agentResult)
}

func (s *Service) prepareFeature(ctx context.Context, input turnExecution) (map[string]string, map[string]string, error) {
	bases, err := s.Store.TaskBases(ctx, input.task.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(bases) == 0 && input.task.Status != lifecycle.Queued {
		// Backfill older sessions from the first candidate, if one exists.
		bases, err = s.Store.FirstCandidateBases(ctx, input.task.ID)
		if err != nil {
			return nil, nil, err
		}
		if len(bases) > 0 {
			if err := s.Store.SaveTaskBases(ctx, input.task.ID, bases); err != nil {
				return nil, nil, err
			}
		}
	}
	var heads map[string]string
	if len(bases) == 0 {
		heads, err = gitstate.BeginFeature(ctx, input.workspace, input.task.ID, input.work.Scope.Repositories)
	} else {
		heads, err = gitstate.EnsureFeature(ctx, input.workspace, input.task.ID, input.work.Scope.Repositories)
	}
	if err != nil {
		return nil, nil, err
	}
	if len(bases) == 0 {
		bases = heads
		if err := s.Store.SaveTaskBases(ctx, input.task.ID, bases); err != nil {
			return nil, nil, err
		}
	}
	if len(bases) != len(input.session.Repositories) {
		return nil, nil, fault.New(fault.CodeCandidateCorrupt, "task base vector is incomplete")
	}
	if _, err := s.saveCheckpoint(ctx, input.task.SessionID, input.task.ID, input.session,
		input.workspace, input.state, input.row.NativeID.Value); err != nil {
		return nil, nil, err
	}
	return bases, heads, nil
}

func (s *Service) invokeAgent(ctx context.Context, input turnExecution, agentAttempted *bool) (agents.AgentResult, error) {
	driver, err := s.DriverFactory(input.agent.Runtime)
	if err != nil {
		return agents.AgentResult{}, err
	}
	controlDir, closeControl, err := s.startDelegationServer(input)
	if err != nil {
		return agents.AgentResult{}, err
	}
	defer closeControl()
	prompt := agents.AgentPrompt(input.work, input.work.Scope.Repositories, input.reply, input.task.ErrorCode.Value, artifacts.ClaimPath)
	if len(input.agent.Delegates) > 0 {
		prompt += " You may use the Turnyard MCP gateway's find_tools and call_tool to invoke " + agents.DelegationToolID + "/delegate for submitting, inspecting, or continuing managed child tasks. Allowed child agents: " + strings.Join(input.agent.Delegates, ", ") + ". Each submit must give the child a complete actionable objective and concrete acceptance conditions copied from this task; placeholders are invalid. Do not add conflicting requirements. Each child has an isolated session; verified files appear under /turnyard-control/delegations/. Integrate selected changes yourself, then satisfy this task's own checks and deliverables."
	}
	*agentAttempted = true
	result, err := driver.Invoke(ctx, agents.AgentInvocation{
		Sandbox: input.backend, Environment: input.env, AgentID: input.session.PrimaryAgent,
		Workspace: input.workspace, Scope: input.work.Scope.Repositories, State: input.state, ArtifactDir: artifactOutputDir(input),
		InputDir: inputDir(input), ControlDir: controlDir,
		NativeID: input.row.NativeID.Value, Prompt: prompt,
		InvocationID: input.invocationID, LogPath: input.logPath, Timeout: input.timeout,
	})
	if err != nil {
		return agents.AgentResult{}, err
	}
	err = s.Store.CompleteAgent(ctx, input.task.SessionID, input.task.ID, input.invocationID, result.NativeID,
		map[string]any{"native_id": result.NativeID, "event_count": len(result.Events), "needs_input": result.NeedsInput,
			"actual_provider": result.ActualProvider, "actual_model": result.ActualModel,
			"configured_skills": input.agent.Skills, "connected_tools": result.ConnectedTools,
			"loaded_skills": result.LoadedSkills, "called_tools": result.CalledTools})
	if err != nil {
		return agents.AgentResult{}, err
	}
	observe.Log.InfoContext(ctx, "agent completed", "session_id", input.task.SessionID, "task_id", input.task.ID,
		"invocation_id", input.invocationID, "runtime", input.agent.Runtime, "native_id", result.NativeID,
		"provider", result.ActualProvider, "model", result.ActualModel)
	return result, nil
}

func artifactOutputDir(input turnExecution) string {
	return filepath.Join(filepath.Dir(input.workspace), "artifacts", input.invocationID)
}

func inputDir(input turnExecution) string {
	if len(input.work.Inputs) == 0 {
		return ""
	}
	return filepath.Join(filepath.Dir(input.workspace), "inputs")
}

func (s *Service) finalizeTurn(ctx context.Context, input turnExecution, bases, heads map[string]string,
	agentResult agents.AgentResult) (TaskRunResult, error) {
	claims := map[string]artifacts.Claim{}
	var err error
	if artifacts.NeedsClaims(input.work.Deliverables) {
		claims, err = artifacts.ReadClaims(artifactOutputDir(input))
		if err != nil {
			return TaskRunResult{}, err
		}
	}
	vector, err := gitstate.Finalize(ctx, input.workspace, input.task.ID, input.session, input.work, bases, heads)
	if err != nil {
		return TaskRunResult{}, err
	}
	candidateID := contracts.NewID("cand")
	localPaths, err := artifacts.SealOutputs(ctx, artifactOutputDir(input), input.workspace, filepath.Dir(input.workspace), candidateID, vector, input.work.Deliverables)
	if err != nil {
		return TaskRunResult{}, err
	}
	deliverables, err := artifacts.Inspect(ctx, input.workspace, input.session.Repositories, vector, input.work.Deliverables, claims, localPaths, s.ArtifactVerifier)
	if err != nil {
		return TaskRunResult{}, err
	}
	digest := candidateDigest(vector, input.work, deliverables)
	if err := s.Store.RecordCandidate(ctx, input.task.SessionID, input.task.ID, candidateID, digest, vector, deliverables); err != nil {
		return TaskRunResult{}, err
	}
	checkpointID, err := s.saveCheckpoint(ctx, input.task.SessionID, input.task.ID, input.session,
		input.workspace, input.state, agentResult.NativeID)
	if err != nil {
		return TaskRunResult{}, err
	}
	checks := []CheckResult{}
	if agentResult.NeedsInput == "" {
		checks, err = s.runChecks(ctx, input.task.ID, input.task.SessionID, candidateID, input.work,
			input.env, input.workspace, input.backend)
		if err != nil {
			return TaskRunResult{}, err
		}
		if checksPassed(checks) {
			deliverables, err = s.deliverOutputs(ctx, input.task.SessionID, input.task.ID, input.env, input.work, deliverables)
			if err != nil {
				return TaskRunResult{}, err
			}
		}
	}
	status, code := candidateOutcome(agentResult.NeedsInput, checks, deliverables)
	if err := s.Store.FinishCandidate(ctx, store.CandidateOutcome{
		SessionID: input.task.SessionID, TaskID: input.task.ID, CandidateID: candidateID,
		TurnID: input.turnID, Status: status, CheckpointID: checkpointID, ErrorCode: code,
		Checks: checks, Deliverables: deliverables,
	}); err != nil {
		return TaskRunResult{}, err
	}
	observe.Log.InfoContext(ctx, "candidate finalized", "session_id", input.task.SessionID, "task_id", input.task.ID,
		"candidate_id", candidateID, "digest", digest, "status", status)
	return TaskRunResult{TaskID: input.task.ID, Status: status, CandidateID: candidateID,
		CandidateDigest: digest, CheckpointID: checkpointID, Checks: checks,
		Deliverables: deliverables, NeedsInput: agentResult.NeedsInput,
		NativeSessionID: agentResult.NativeID}, nil
}

func candidateDigest(vector map[string]gitstate.RepoVersion, work contracts.WorkSpec, results []artifacts.Result) string {
	type fingerprint struct{ ID, Kind, SHA256, Text, URL string }
	outputs := make([]fingerprint, 0, len(results))
	for _, result := range results {
		outputs = append(outputs, fingerprint{result.ID, string(result.Kind), result.SHA256, result.Text, result.URL})
	}
	return contracts.Digest(struct {
		Vector  map[string]gitstate.RepoVersion
		Work    contracts.WorkSpec
		Outputs []fingerprint
	}{vector, work, outputs})
}

func legacyCandidateDigest(vector map[string]gitstate.RepoVersion, digest string) bool {
	return len(vector) > 0 && contracts.Digest(vector) == digest
}

func checksPassed(checks []CheckResult) bool {
	for _, check := range checks {
		if !check.Passed {
			return false
		}
	}
	return true
}

func (s *Service) deliverOutputs(ctx context.Context, sid, tid string, env contracts.EnvironmentSpec, work contracts.WorkSpec, results []artifacts.Result) ([]artifacts.Result, error) {
	for i, spec := range work.Deliverables {
		if spec.Destination == nil || spec.Destination.Kind != contracts.DestinationConnector || results[i].Status != artifacts.StatusPresent {
			continue
		}
		intent := map[string]any{"deliverable_id": spec.ID, "connector": spec.Destination.Connector,
			"uri_digest": contracts.Digest(spec.Destination.URI), "sha256": results[i].SHA256}
		if err := s.Store.AppendEvent(ctx, sid, tid, lifecycle.EventArtifactDeliveryIntent, intent); err != nil {
			return nil, err
		}
		results[i] = artifacts.DeliverConnector(ctx, env, spec, results[i])
		if err := s.Store.AppendEvent(ctx, sid, tid, lifecycle.EventArtifactDeliveryResult,
			map[string]any{"deliverable_id": spec.ID, "status": results[i].Status, "sha256": results[i].SHA256}); err != nil {
			return nil, err
		}
		observe.Log.InfoContext(ctx, "artifact delivery completed", "task_id", tid, "deliverable_id", spec.ID, "status", results[i].Status)
	}
	return results, nil
}

func candidateOutcome(needsInput string, checks []CheckResult, deliverables []artifacts.Result) (string, fault.Code) {
	if needsInput != "" {
		return lifecycle.NeedsInput, ""
	}
	status := lifecycle.Verified
	for _, check := range checks {
		if !check.Passed {
			status = lifecycle.Failed
		}
	}
	if !artifacts.Pass(deliverables) {
		status = lifecycle.Failed
	}
	if status == lifecycle.Verified {
		return status, ""
	}
	for _, item := range deliverables {
		if item.Status != artifacts.StatusPresent {
			return status, fault.Code(item.ErrorCode)
		}
	}
	return status, fault.CodeCheckFailed
}
