package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/agents/registry"
	"github.com/Covalane/turnyard/internal/artifacts"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/sandbox"
	"github.com/Covalane/turnyard/internal/store"
)

type Service struct {
	Store             *store.Store
	Capacity          *Capacity
	BackendFactory    func(string) (sandbox.SandboxBackend, error)
	DriverFactory     func(string) (agents.AgentDriver, error)
	ArtifactVerifier  artifacts.PullRequestVerifier
	activeDelegations atomic.Int64
	delegationMu      sync.Mutex
	activeChildTasks  map[string]bool
	runMu             sync.Mutex
	runningSessions   map[string]bool
	additionMu        sync.Mutex
	additionLocks     map[string]*additionLock
}

type additionLock struct {
	slot  chan struct{}
	users int
}

// lockAddition serializes attachment promotion and database admission for one
// session, so an unsuccessful retry cannot remove another request's snapshot.
func (s *Service) lockAddition(ctx context.Context, sessionID string) (func(), error) {
	s.additionMu.Lock()
	if s.additionLocks == nil {
		s.additionLocks = make(map[string]*additionLock)
	}
	lock := s.additionLocks[sessionID]
	if lock == nil {
		lock = &additionLock{slot: make(chan struct{}, 1)}
		s.additionLocks[sessionID] = lock
	}
	lock.users++
	s.additionMu.Unlock()
	releaseReference := func() {
		s.additionMu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(s.additionLocks, sessionID)
		}
		s.additionMu.Unlock()
	}
	select {
	case lock.slot <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-lock.slot
			releaseReference()
			return nil, err
		}
		return func() {
			<-lock.slot
			releaseReference()
		}, nil
	case <-ctx.Done():
		releaseReference()
		return nil, ctx.Err()
	}
}

func (s *Service) ActiveDelegations() int64 { return s.activeDelegations.Load() }

// reserveSession prevents a managed child run and an operator-triggered
// verification from using the same workspace and task state concurrently.
func (s *Service) reserveSession(sessionID string) (func(), error) {
	s.runMu.Lock()
	if s.runningSessions == nil {
		s.runningSessions = map[string]bool{}
	}
	if s.runningSessions[sessionID] {
		s.runMu.Unlock()
		return nil, fault.New(fault.CodeConcurrentRun, "session already has active work")
	}
	s.runningSessions[sessionID] = true
	s.runMu.Unlock()
	return func() {
		s.runMu.Lock()
		delete(s.runningSessions, sessionID)
		s.runMu.Unlock()
	}, nil
}

func NewService(ctx context.Context, root string) (*Service, error) {
	return NewServiceWithCapacity(ctx, root, DefaultCapacityLimits())
}

func NewServiceWithCapacity(ctx context.Context, root string, limits CapacityLimits) (*Service, error) {
	capacity, err := NewCapacity(limits)
	if err != nil {
		return nil, err
	}
	store, err := store.OpenStore(ctx, root)
	if err != nil {
		return nil, err
	}
	return &Service{Store: store, Capacity: capacity, BackendFactory: sandbox.Backend, DriverFactory: registry.Driver,
		ArtifactVerifier: artifacts.GitHubCLI{}}, nil
}
func (s *Service) Close() error {
	return s.Store.Close()
}
func (s *Service) TaskResult(ctx context.Context, tid string) (TaskResult, error) {
	task, err := s.Store.Task(ctx, tid)
	if err != nil {
		return TaskResult{}, err
	}
	inv, err := s.Store.Invocations(ctx, tid)
	if err != nil {
		return TaskResult{}, err
	}
	result := TaskResult{SchemaVersion: contracts.TaskResultVersion, Task: task, Invocations: inv}
	if task.CandidateID.Present {
		candidate, err := s.Store.Candidate(ctx, task.CandidateID.Value)
		if err != nil {
			return TaskResult{}, err
		}
		var vector map[string]gitstate.RepoVersion
		var checks []CheckResult
		var deliverables []artifacts.Result
		if err := json.Unmarshal([]byte(candidate.Vector), &vector); err != nil {
			return TaskResult{}, fault.Wrap(fault.CodeCandidateCorrupt, "decode vector", err, "candidate vector is invalid")
		}
		if err := json.Unmarshal([]byte(candidate.Checks), &checks); err != nil {
			return TaskResult{}, fault.Wrap(fault.CodeCandidateCorrupt, "decode checks", err, "candidate checks are invalid")
		}
		if err := json.Unmarshal([]byte(candidate.Deliverables), &deliverables); err != nil {
			return TaskResult{}, fault.Wrap(fault.CodeCandidateCorrupt, "decode deliverables", err, "candidate deliverables are invalid")
		}
		row, err := s.Store.Session(ctx, task.SessionID)
		if err != nil {
			return TaskResult{}, err
		}
		artifacts.RebaseLocalPaths(deliverables, filepath.Dir(row.Workspace), candidate.ID)
		result.Candidate = &CandidateResult{ID: candidate.ID, TaskID: candidate.TaskID, Status: candidate.Status, Digest: candidate.Digest, Vector: vector, Checks: checks, Deliverables: deliverables, CreatedAt: candidate.CreatedAt}
	}
	turns, err := s.Store.Turns(ctx, tid)
	if err != nil {
		return TaskResult{}, err
	}
	result.Turns = turns
	result.Delegations, err = s.delegationSummaries(ctx, tid)
	if err != nil {
		return TaskResult{}, err
	}
	return result, nil
}
func (s *Service) SessionResult(ctx context.Context, sid string) (SessionResult, error) {
	session, err := s.Store.Session(ctx, sid)
	if err != nil {
		return SessionResult{}, err
	}
	tasks, err := s.Store.Tasks(ctx, sid)
	if err != nil {
		return SessionResult{}, err
	}
	deliveries, err := s.sessionDeliveries(ctx, tasks, filepath.Dir(session.Workspace))
	if err != nil {
		return SessionResult{}, err
	}
	delegations := []DelegationSummary{}
	for _, task := range tasks {
		items, err := s.delegationSummaries(ctx, task.ID)
		if err != nil {
			return SessionResult{}, err
		}
		delegations = append(delegations, items...)
	}
	return SessionResult{SchemaVersion: contracts.SessionResultVersion, Session: session, Tasks: tasks, Deliveries: deliveries, Delegations: delegations}, nil
}

func (s *Service) delegationSummaries(ctx context.Context, taskID string) ([]DelegationSummary, error) {
	children, err := s.Store.DelegatedSessions(ctx, taskID)
	if err != nil {
		return nil, err
	}
	result := make([]DelegationSummary, 0, len(children))
	for _, child := range children {
		tasks, err := s.Store.Tasks(ctx, child.ID)
		if err != nil {
			return nil, err
		}
		item := DelegationSummary{SessionID: child.ID, ParentTaskID: taskID, AgentID: child.DelegateAgentID.Value, Status: child.Status}
		if len(tasks) == 1 {
			item.TaskID = tasks[0].ID
			item.Status = tasks[0].Status
			if tasks[0].CandidateID.Present {
				candidate, err := s.Store.Candidate(ctx, tasks[0].CandidateID.Value)
				if err != nil {
					return nil, err
				}
				item.CandidateID, item.CandidateDigest = candidate.ID, candidate.Digest
			}
		}
		result = append(result, item)
	}
	return result, nil
}

// sessionDeliveries provides a candidate-bound handoff for every task.
// A task without a candidate remains visible while it is still running.
func (s *Service) sessionDeliveries(ctx context.Context, tasks []store.TaskRow, sessionRoot string) ([]Delivery, error) {
	deliveries := make([]Delivery, 0, len(tasks))
	for _, task := range tasks {
		entry := Delivery{TaskID: task.ID, Status: task.Status}
		if task.CandidateID.Present {
			candidate, err := s.Store.Candidate(ctx, task.CandidateID.Value)
			if err != nil {
				return nil, err
			}
			var outputs []artifacts.Result
			if err := json.Unmarshal([]byte(candidate.Deliverables), &outputs); err != nil {
				return nil, fault.Wrap(fault.CodeCandidateCorrupt, "decode deliverables", err, "candidate deliverables are invalid")
			}
			artifacts.RebaseLocalPaths(outputs, sessionRoot, candidate.ID)
			entry.CandidateID = candidate.ID
			entry.CandidateDigest = candidate.Digest
			entry.CandidateStatus = candidate.Status
			entry.Deliverables = &outputs
		}
		deliveries = append(deliveries, entry)
	}
	return deliveries, nil
}
func (s *Service) Events(ctx context.Context, sid string, after int64) (EventsResult, error) {
	events, err := s.Store.Events(ctx, sid, after)
	return EventsResult{Events: events}, err
}
func ErrorCode(err error) string {
	return string(fault.CodeOf(err))
}
