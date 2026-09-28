package engine

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
	"github.com/Covalane/turnyard/internal/store"
)

const maxDelegationRequest = 1 << 20
const maxDelegatedOutput = 512 << 20

var delegationKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$`)
var delegationFilePattern = regexp.MustCompile(`^[a-f0-9]{32}\.json$`)

type delegationRequest struct {
	Action         contracts.DelegationAction  `json:"action"`
	Key            string                      `json:"key"`
	AgentID        string                      `json:"agent_id,omitempty"`
	Objective      string                      `json:"objective,omitempty"`
	Acceptance     []string                    `json:"acceptance,omitempty"`
	Scope          []contracts.ScopeRepo       `json:"scope,omitempty"`
	Checks         []string                    `json:"checks,omitempty"`
	Deliverables   []contracts.DeliverableSpec `json:"deliverables,omitempty"`
	InputIDs       []string                    `json:"input_ids,omitempty"`
	Reply          string                      `json:"reply,omitempty"`
	Retry          bool                        `json:"retry,omitempty"`
	TimeoutSeconds int                         `json:"timeout_seconds,omitempty"`
}

type delegationResponse struct {
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type delegationServer struct {
	service     *Service
	parent      turnExecution
	controlDir  string
	requestRoot *os.Root
	handoffMu   sync.Mutex
	handoffs    map[string]delegationHandoff
	stop        chan struct{}
	done        chan struct{}
}

func (d *delegationServer) dispatch(ctx context.Context, req delegationRequest) (any, error) {
	if !delegationKeyPattern.MatchString(req.Key) {
		return nil, fault.New(fault.CodeInvalidRequest, "delegation key must be 1-40 simple characters")
	}
	if req.TimeoutSeconds != 0 && (req.TimeoutSeconds < 1 || req.TimeoutSeconds > 7200) {
		return nil, fault.New(fault.CodeInvalidRequest, "delegation timeout must be 1-7200 seconds")
	}
	key := "delegate:" + d.parent.task.ID + ":" + req.Key
	switch req.Action {
	case contracts.DelegationActionSubmit:
		return d.submit(ctx, key, req)
	case contracts.DelegationActionStatus:
		return d.status(ctx, key)
	case contracts.DelegationActionContinue:
		return d.continueTask(ctx, key, req)
	default:
		return nil, fault.New(fault.CodeInvalidRequest, "unknown delegation action")
	}
}

func (d *delegationServer) submit(ctx context.Context, key string, req delegationRequest) (any, error) {
	if len([]rune(strings.TrimSpace(req.Objective))) < 2 || len(req.Acceptance) == 0 {
		return nil, fault.New(fault.CodeInvalidRequest, "delegation needs a concrete objective and acceptance condition")
	}
	for _, condition := range req.Acceptance {
		if len([]rune(strings.TrimSpace(condition))) < 2 {
			return nil, fault.New(fault.CodeInvalidRequest, "delegation acceptance cannot be a placeholder")
		}
	}
	requestShape := req
	requestShape.Action, requestShape.Reply = "", ""
	requestShape.Retry, requestShape.TimeoutSeconds = false, 0
	requestDigest := contracts.Digest(requestShape)
	if !contains(d.parent.agent.Delegates, req.AgentID) {
		return nil, fault.New(fault.CodeCapabilityMissing, "agent %s cannot delegate to %s", d.parent.agent.ID, req.AgentID)
	}
	parentTask, err := d.service.Store.Task(ctx, d.parent.task.ID)
	if err != nil {
		return nil, err
	}
	if parentTask.Status != lifecycle.Running {
		return nil, fault.New(fault.CodeInvalidTransition, "parent task is not running")
	}
	childAgent, binding, err := agents.SelectAgent(d.parent.env, req.AgentID)
	if err != nil {
		return nil, err
	}
	prior, found, err := d.service.Store.SessionByCreationKey(ctx, key)
	if err != nil {
		return nil, err
	}
	created := SessionCreationResult{}
	var existing store.SessionRow
	if found {
		existing, err = d.service.Store.Session(ctx, prior.ID)
		if err != nil {
			return nil, err
		}
		if !existing.ParentTaskID.Present || existing.ParentTaskID.Value != d.parent.task.ID ||
			!existing.DelegateAgentID.Present || existing.DelegateAgentID.Value != childAgent.ID ||
			!existing.DelegationRequestDigest.Present || existing.DelegationRequestDigest.Value != requestDigest {
			return nil, fault.New(fault.CodeIdempotencyConflict, "delegation key names different parent or agent")
		}
		tasks, err := d.service.Store.Tasks(ctx, existing.ID)
		if err != nil {
			return nil, err
		}
		if len(tasks) == 1 {
			if tasks[0].Status == lifecycle.Queued {
				d.runChild(tasks[0].ID, "", false, req.TimeoutSeconds)
			}
			return d.status(ctx, key)
		}
		if len(tasks) != 0 {
			return nil, fault.New(fault.CodeSessionCorrupt, "delegation has multiple child tasks")
		}
		created.SessionID = existing.ID
	}
	childAgent.Delegates = nil // A child cannot silently grow a new delegation tree.
	childEnv := d.parent.env
	childEnv.Agents = []contracts.AgentSpec{childAgent}
	childEnv.ModelBindings = []contracts.ModelBinding{binding}
	childEnv.Git.RemoteWrites = contracts.GitRemoteWritesNone
	childSpec := d.parent.session
	childSpec.IdempotencyKey = key
	childSpec.PrimaryAgent = childAgent.ID
	if found {
		childSpec, childEnv, err = parseSessionRow(existing)
		if err != nil {
			return nil, err
		}
	} else {
		childSpec.Repositories = make([]contracts.RepositorySpec, 0, len(d.parent.session.Repositories))
		status, err := gitstate.WorkspaceStatus(ctx, d.parent.workspace, d.parent.session.Repositories)
		if err != nil {
			return nil, err
		}
		for _, repo := range d.parent.session.Repositories {
			if status[repo.ID].Dirty {
				return nil, fault.New(fault.CodeDirtyCandidate, "commit or discard parent edits before delegating repository %s", repo.ID)
			}
			childSpec.Repositories = append(childSpec.Repositories, contracts.RepositorySpec{
				ID: repo.ID, Type: contracts.RepositorySourceLocalGit, Path: filepath.Join(d.parent.workspace, repo.ID), Commit: status[repo.ID].Head,
			})
		}
	}
	work := contracts.WorkSpec{SchemaVersion: contracts.WorkVersion, IdempotencyKey: key,
		Objective: req.Objective, Acceptance: req.Acceptance, Checks: req.Checks, Deliverables: req.Deliverables}
	work.Scope.Repositories = req.Scope
	if work.Checks == nil {
		work.Checks = []string{}
	}
	if work.Scope.Repositories == nil {
		work.Scope.Repositories = []contracts.ScopeRepo{}
	}
	if err := d.validateScope(req.Scope); err != nil {
		return nil, err
	}
	for _, output := range work.Deliverables {
		if output.Kind == contracts.DeliverablePullRequest || output.Destination != nil && output.Destination.Kind == contracts.DestinationConnector {
			return nil, fault.New(fault.CodeCapabilityMissing, "delegated external delivery requires an explicit host capability")
		}
	}
	if len(req.InputIDs) > 0 {
		inputs := map[string]contracts.InputSpec{}
		for _, item := range d.parent.work.Inputs {
			inputs[item.ID] = item
		}
		seen := map[string]bool{}
		for _, id := range req.InputIDs {
			item, ok := inputs[id]
			if !ok || seen[id] {
				return nil, fault.New(fault.CodeInvalidSpec, "invalid delegated input %s", id)
			}
			seen[id] = true
			work.Inputs = append(work.Inputs, contracts.InputSpec{ID: id, ExpectedSHA256: item.ExpectedSHA256,
				MediaType: item.MediaType, Source: contracts.InputSource{Kind: contracts.InputFile,
					Path: filepath.Join(filepath.Dir(d.parent.workspace), "inputs", filepath.FromSlash(item.Source.Path))}})
		}
	}
	if _, err := contracts.ValidateWorkSpec(work, childSpec, childEnv); err != nil {
		return nil, err
	}
	if !found {
		created, err = d.service.createSession(ctx, childSpec, childEnv, store.CreateSessionInput{
			ParentSessionID: d.parent.task.SessionID, ParentTaskID: d.parent.task.ID,
			ParentInvocationID: d.parent.invocationID, DelegateAgentID: childAgent.ID,
			DelegationRequestDigest: requestDigest})
		if err != nil {
			return nil, err
		}
	}
	added, err := d.service.addTask(ctx, created.SessionID, work, filepath.Join(d.parent.workspace, "delegated-work.json"))
	if err != nil {
		return nil, err
	}
	if added.Status == lifecycle.Queued {
		d.runChild(added.TaskID, "", false, req.TimeoutSeconds)
	}
	observe.Log.InfoContext(ctx, "delegation submitted", "parent_task_id", d.parent.task.ID,
		"child_session_id", created.SessionID, "child_task_id", added.TaskID, "agent_id", childAgent.ID)
	return d.status(ctx, key)
}

func (d *delegationServer) validateScope(scope []contracts.ScopeRepo) error {
	parent := parentRepositoryScope{}
	for _, item := range d.parent.work.Scope.Repositories {
		parent[item.ID] = item.Mode
	}
	if len(scope) != len(parent) {
		return fault.New(fault.CodeScopeViolation, "delegation must name every parent repository")
	}
	seen := map[string]bool{}
	for _, item := range scope {
		if seen[item.ID] || !parent.allows(item) {
			return fault.New(fault.CodeScopeViolation, "delegation exceeds parent repository scope")
		}
		seen[item.ID] = true
	}
	return nil
}

type parentRepositoryScope map[string]contracts.ScopeMode

func (parent parentRepositoryScope) allows(item contracts.ScopeRepo) bool {
	mode, exists := parent[item.ID]
	if !exists {
		return false
	}
	switch mode {
	case contracts.ScopeRead:
		return item.Mode == contracts.ScopeRead
	case contracts.ScopeWrite:
		return item.Mode == contracts.ScopeRead || item.Mode == contracts.ScopeWrite
	default:
		return false
	}
}

func (d *delegationServer) find(ctx context.Context, key string) (store.SessionRow, store.TaskRow, error) {
	created, found, err := d.service.Store.SessionByCreationKey(ctx, key)
	if err != nil {
		return store.SessionRow{}, store.TaskRow{}, err
	}
	if !found {
		return store.SessionRow{}, store.TaskRow{}, fault.New(fault.CodeNotFound, "delegation not found")
	}
	session, err := d.service.Store.Session(ctx, created.ID)
	if err != nil {
		return store.SessionRow{}, store.TaskRow{}, err
	}
	if !session.ParentTaskID.Present || session.ParentTaskID.Value != d.parent.task.ID {
		return store.SessionRow{}, store.TaskRow{}, fault.New(fault.CodeCapabilityMissing, "delegation belongs to another task")
	}
	tasks, err := d.service.Store.Tasks(ctx, session.ID)
	if err != nil {
		return store.SessionRow{}, store.TaskRow{}, err
	}
	if len(tasks) != 1 {
		return session, store.TaskRow{}, fault.New(fault.CodeSessionCorrupt, "delegation has no single child task")
	}
	return session, tasks[0], nil
}

func (d *delegationServer) status(ctx context.Context, key string) (any, error) {
	childSession, childTask, err := d.find(ctx, key)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"delegation_id": childSession.ID, "agent_id": childSession.DelegateAgentID.Value,
		"session_id": childSession.ID, "task_id": childTask.ID, "status": childTask.Status,
		"error_code": childTask.ErrorCode.Value}
	if childTask.CandidateID.Present {
		taskResult, err := d.service.TaskResult(ctx, childTask.ID)
		if err != nil {
			return nil, err
		}
		result["candidate"] = taskResult.Candidate
		if childTask.Status == lifecycle.Verified && taskResult.Candidate != nil && taskResult.Candidate.Status == lifecycle.Verified {
			handoff, err := d.exportHandoff(ctx, childSession, taskResult.Candidate)
			if err != nil {
				if code := fault.CodeOf(err); code == fault.CodeScopeViolation || code == fault.CodeDeliverableMismatch {
					if failErr := d.service.Store.FailDelegationHandoff(ctx, childSession.ID, childTask.ID, taskResult.Candidate.ID); failErr != nil {
						return nil, failErr
					}
					result["status"] = lifecycle.Failed
					result["error_code"] = fault.CodeHandoffUnavailable
					taskResult.Candidate.Status = lifecycle.Failed
				} else {
					result["status"] = DelegationHandoffUnavailable
				}
				result["handoff_error"] = err.Error()
			} else {
				result["handoff"] = handoff
				if childSession.Status != lifecycle.Completed {
					if _, err := d.service.completeSession(ctx, childSession.ID, true); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return result, nil
}

func (d *delegationServer) continueTask(ctx context.Context, key string, req delegationRequest) (any, error) {
	_, task, err := d.find(ctx, key)
	if err != nil {
		return nil, err
	}
	if task.Status == lifecycle.NeedsInput && req.Reply != "" && !req.Retry || task.Status == lifecycle.Failed && req.Retry && req.Reply == "" {
		if !d.runChild(task.ID, req.Reply, req.Retry, req.TimeoutSeconds) {
			return nil, fault.New(fault.CodeConcurrentRun, "delegated task is still settling")
		}
		return d.status(ctx, key)
	}
	return nil, fault.New(fault.CodeInvalidTransition, "delegation cannot continue from %s; unknown requires operator reconciliation", task.Status)
}

func (d *delegationServer) runChild(taskID, reply string, retry bool, seconds int) bool {
	d.service.delegationMu.Lock()
	if d.service.activeChildTasks == nil {
		d.service.activeChildTasks = map[string]bool{}
	}
	if d.service.activeChildTasks[taskID] {
		d.service.delegationMu.Unlock()
		return false
	}
	d.service.activeChildTasks[taskID] = true
	d.service.delegationMu.Unlock()
	if seconds == 0 {
		seconds = 900
	}
	if seconds < 1 || seconds > 7200 {
		seconds = 900
	}
	d.service.activeDelegations.Add(1)
	go func() {
		defer d.service.activeDelegations.Add(-1)
		defer func() {
			d.service.delegationMu.Lock()
			delete(d.service.activeChildTasks, taskID)
			d.service.delegationMu.Unlock()
		}()
		ctx := observe.WithIDs(context.Background(), observe.IDs{TaskID: taskID})
		_, err := d.service.RunTask(ctx, taskID, reply, retry, time.Duration(seconds)*time.Second)
		if err != nil {
			observe.LogFailure(ctx, "delegation run failed", fault.Ensure(err, "run delegated task"))
			return
		}
	}()
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
