package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/inputs"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type testSandbox struct {
	checkExit int
	checkErr  error
}

func (*testSandbox) Name() string { return "apple-container" }
func (*testSandbox) Probe(context.Context) (map[string]any, error) {
	return map[string]any{"available": true}, nil
}
func (*testSandbox) ImageIdentity(context.Context, string) (string, error) { return "sha256:test", nil }
func (*testSandbox) ValidateSpec(context.Context, SandboxSpec) error       { return nil }
func (*testSandbox) StopInvocation(context.Context, string) error          { return nil }
func (*testSandbox) StopTaskChecks(context.Context, string) error          { return nil }
func (b *testSandbox) Run(_ context.Context, input SandboxRun) (SandboxResult, error) {
	if b.checkErr != nil {
		return SandboxResult{}, b.checkErr
	}
	_ = os.MkdirAll(filepath.Dir(input.LogPath), 0o700)
	_ = os.WriteFile(input.LogPath, []byte("test check"), 0o600)
	return SandboxResult{ExitCode: b.checkExit, Backend: b.Name()}, nil
}

type testDriver struct {
	run     func(AgentInvocation) (AgentResult, error)
	runtime string
}

func (d *testDriver) Runtime() string {
	if d.runtime != "" {
		return d.runtime
	}
	return "opencode"
}
func (*testDriver) ValidateBinding(ModelBinding) error                   { return nil }
func (*testDriver) ValidateEnvironment(AgentSpec, EnvironmentSpec) error { return nil }
func (d *testDriver) Invoke(_ context.Context, input AgentInvocation) (AgentResult, error) {
	return d.run(input)
}
func fixtureRepo(t *testing.T, root, name string) RepositorySpec {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	r, err := git.PlainInit(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "README.txt"), []byte(name), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add("README.txt"); err != nil {
		t.Fatal(err)
	}
	hash, err := w.Commit("fixture", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@localhost", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	return RepositorySpec{ID: name, Type: "local-git", Path: path, Commit: hash.String()}
}
func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	service *Service
	root    string
	sid     string
	driver  *testDriver
	sandbox *testSandbox
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	api := fixtureRepo(t, root, "api")
	web := fixtureRepo(t, root, "web")
	inputs := filepath.Join(root, "inputs")
	session := SessionSpec{SchemaVersion: "turnyard.session/v1", IdempotencyKey: "fixture-session", Repositories: []RepositorySpec{api, web},
		Environment: "environment.json", PrimaryAgent: "lead"}
	env := EnvironmentSpec{SchemaVersion: "turnyard.environment/v1",
		Sandbox:       SandboxSpec{Backend: "apple-container", Image: "test:latest"},
		Agents:        []AgentSpec{{ID: "lead", Runtime: "opencode", ModelBinding: "model"}},
		ModelBindings: []ModelBinding{{ID: "model", Provider: "ollama-cloud", Model: "glm-5.3-flash", CredentialEnv: "OLLAMA_API_KEY"}},
		Git:           GitPolicy{LocalCommits: true, RemoteWrites: "none"},
		Checks:        []CheckSpec{{ID: "cross", Argv: []string{"go", "version"}, Repositories: []string{"api", "web"}}}}
	writeJSON(t, filepath.Join(inputs, "session.json"), session)
	writeJSON(t, filepath.Join(inputs, "environment.json"), env)
	service, err := NewService(context.Background(), filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	sb := &testSandbox{}
	driver := &testDriver{}
	service.BackendFactory = func(string) (SandboxBackend, error) { return sb, nil }
	service.DriverFactory = func(string) (AgentDriver, error) { return driver, nil }
	created, err := service.CreateSession(context.Background(), filepath.Join(inputs, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{service, root, created.SessionID, driver, sb}
}

func TestSessionCreationReplayAndConflict(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	path := filepath.Join(f.root, "inputs", "session.json")
	replay, err := f.service.CreateSession(ctx, path)
	if err != nil || replay.SessionID != f.sid || replay.Replayed != true {
		t.Fatalf("same input must replay the existing session: %v %v", replay, err)
	}
	reopened, err := NewService(ctx, f.service.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	replay, err = reopened.CreateSession(ctx, path)
	if err != nil || replay.SessionID != f.sid || !replay.Replayed {
		t.Fatalf("creation replay did not survive supervisor restart: %v %v", replay, err)
	}
	envPath := filepath.Join(f.root, "inputs", "environment.json")
	var env EnvironmentSpec
	if err := ReadJSON(envPath, "environment", &env); err != nil {
		t.Fatal(err)
	}
	env.ModelBindings[0].Model = "another-model"
	writeJSON(t, envPath, env)
	if _, err := f.service.CreateSession(ctx, path); ErrorCode(err) != string(fault.CodeIdempotencyConflict) {
		t.Fatalf("same key with changed input must conflict: %v", err)
	}
}

func TestConcurrentSessionCreationUsesOneKey(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.root, "inputs", "session.json")
	var session SessionSpec
	if err := ReadJSON(path, "session", &session); err != nil {
		t.Fatal(err)
	}
	session.IdempotencyKey = "concurrent-session"
	writeJSON(t, path, session)
	type outcome struct {
		result SessionCreationResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for range 2 {
		go func() {
			result, err := f.service.CreateSession(context.Background(), path)
			outcomes <- outcome{result, err}
		}()
	}
	first, second := <-outcomes, <-outcomes
	if first.err != nil || second.err != nil || first.result.SessionID != second.result.SessionID {
		t.Fatalf("concurrent creation diverged: %+v %+v", first, second)
	}
	entries, err := os.ReadDir(filepath.Join(f.service.Store.Root, "sessions"))
	if err != nil || len(entries) != 2 { // fixture session plus one concurrent creation
		t.Fatalf("orphaned workspace after replay: %v entries=%d", err, len(entries))
	}
}

func TestSessionPreparationIsAdmittedBeforeRepositoryWork(t *testing.T) {
	f := newFixture(t)
	capacity, err := NewCapacity(CapacityLimits{MaxTasks: 2, MaxPreparations: 1, QueueWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	f.service.Capacity = capacity
	entered, unblock := make(chan struct{}), make(chan struct{})
	var firstProbe sync.Once
	f.service.BackendFactory = func(string) (SandboxBackend, error) {
		firstProbe.Do(func() {
			close(entered)
			<-unblock
		})
		return f.sandbox, nil
	}
	path := filepath.Join(f.root, "inputs", "session.json")
	var session SessionSpec
	if err := ReadJSON(path, "session", &session); err != nil {
		t.Fatal(err)
	}
	session.IdempotencyKey = "preparation-one"
	firstPath := filepath.Join(f.root, "inputs", "preparation-one.json")
	writeJSON(t, firstPath, session)
	result := make(chan error, 1)
	go func() {
		_, err := f.service.CreateSession(context.Background(), firstPath)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first preparation did not start")
	}
	session.IdempotencyKey = "preparation-two"
	secondPath := filepath.Join(f.root, "inputs", "preparation-two.json")
	writeJSON(t, secondPath, session)
	secondResult := make(chan error, 1)
	go func() {
		_, err := f.service.CreateSession(context.Background(), secondPath)
		secondResult <- err
	}()
	deadline := time.After(5 * time.Second)
	for f.service.Capacity.Status().WaitingPreparations != 1 {
		select {
		case <-deadline:
			t.Fatal("second session did not wait for preparation capacity")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(unblock)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-secondResult; err != nil {
		t.Fatal(err)
	}
}

func TestRejectedTaskAdditionRemovesStagedInputs(t *testing.T) {
	f := newFixture(t)
	if _, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "busy")); err != nil {
		t.Fatal(err)
	}
	attachment := filepath.Join(f.root, "attachment.txt")
	if err := os.WriteFile(attachment, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := WorkSpec{SchemaVersion: "turnyard.work/v1", IdempotencyKey: "rejected", Objective: "Use attachment",
		Acceptance: []string{"read attachment"}, Checks: []string{"cross"},
		Inputs: []contracts.InputSpec{{ID: "note", Source: contracts.InputSource{Kind: contracts.InputFile, Path: attachment}}}}
	work.Scope.Repositories = []ScopeRepo{{ID: "api", Mode: "read"}, {ID: "web", Mode: "read"}}
	path := filepath.Join(f.root, "inputs", "rejected.json")
	writeJSON(t, path, work)
	work.RequestDigest = contracts.Digest(work)
	root := inputs.BatchPath(filepath.Join(f.service.Store.Root, "sessions", f.sid), work)
	if _, err := f.service.AddTask(context.Background(), f.sid, path); fault.CodeOf(err) != fault.CodeSessionBusy {
		t.Fatalf("task was not rejected as busy: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("rejected input snapshot remained: %v", err)
	}
}

func TestConfiguredRuntimeAndSandboxUseFactories(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.root, "inputs", "environment.json")
	var env EnvironmentSpec
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	env.Agents[0].Runtime = "future-agent"
	env.Sandbox.Backend = "future-sandbox"
	env.ModelBindings[0].Provider = "future-provider"
	env.ModelBindings[0].CredentialEnv = "FUTURE_API_KEY"
	writeJSON(t, path, env)
	f.driver.runtime = "future-agent"
	f.service.DriverFactory = func(runtime string) (AgentDriver, error) {
		if runtime != "future-agent" {
			t.Fatalf("runtime=%q", runtime)
		}
		return f.driver, nil
	}
	f.service.BackendFactory = func(backend string) (SandboxBackend, error) {
		if backend != "future-sandbox" {
			t.Fatalf("backend=%q", backend)
		}
		return f.sandbox, nil
	}
	sessionPath := filepath.Join(f.root, "inputs", "session.json")
	var session SessionSpec
	if err := ReadJSON(sessionPath, "session", &session); err != nil {
		t.Fatal(err)
	}
	session.IdempotencyKey = "configured-runtime"
	writeJSON(t, sessionPath, session)
	if _, err := f.service.CreateSession(context.Background(), sessionPath); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) task(t *testing.T, key string, modes ...string) string {
	t.Helper()
	webMode := contracts.ScopeWrite
	if len(modes) > 0 {
		webMode = contracts.ScopeMode(modes[0])
	}
	work := WorkSpec{SchemaVersion: "turnyard.work/v1", IdempotencyKey: key, Objective: "Edit both repositories", CommitMessage: "feat: update both repositories",
		Acceptance: []string{"cross check passes"}, Checks: []string{"cross"}}
	work.Scope.Repositories = []ScopeRepo{{ID: "api", Mode: "write"}, {ID: "web", Mode: webMode}}
	path := filepath.Join(f.root, "inputs", key+".json")
	writeJSON(t, path, work)
	return path
}
func TestGoTwoRepositoriesAndCheckpoint(t *testing.T) {
	f := newFixture(t)
	calls := 0
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		calls++
		for _, repo := range []string{"api", "web"} {
			if err := os.WriteFile(filepath.Join(input.Workspace, repo, "new.txt"), []byte("from Go"), 0o600); err != nil {
				return AgentResult{}, err
			}
		}
		if err := os.MkdirAll(input.State, 0o700); err != nil {
			return AgentResult{}, err
		}
		_ = os.WriteFile(filepath.Join(input.State, "native.db"), []byte("state"), 0o600)
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	path := f.task(t, "one")
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil || replay.Replayed != true {
		t.Fatalf("replay: %v %v", replay, err)
	}
	tid := added.TaskID
	out, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "verified" || calls != 1 {
		t.Fatalf("result: %v", out)
	}
	row, err := f.service.Store.Session(context.Background(), f.sid)
	if err != nil {
		t.Fatal(err)
	}
	status, err := gitstate.WorkspaceStatus(context.Background(), row.Workspace, []RepositorySpec{{ID: "api"}, {ID: "web"}})
	if err != nil {
		t.Fatal(err)
	}
	if status["api"].Branch != "feature/"+tid || status["web"].Branch != "feature/"+tid {
		t.Fatalf("branches: %v", status)
	}
	for _, name := range []string{"api", "web"} {
		repo, err := git.PlainOpen(filepath.Join(row.Workspace, name))
		if err != nil {
			t.Fatal(err)
		}
		commit, err := repo.CommitObject(plumbing.NewHash(status[name].Head))
		if err != nil {
			t.Fatal(err)
		}
		want := "feat: update both repositories\n\nTurnyard-Task: " + tid + "\n"
		if commit.Message != want {
			t.Fatalf("%s commit message = %q, want %q", name, commit.Message, want)
		}
	}
	workspace := row.Workspace
	state := filepath.Join(filepath.Dir(workspace), "agent-state")
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(state); err != nil {
		t.Fatal(err)
	}
	restored, err := f.service.Restore(context.Background(), out.CheckpointID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Restored != true {
		t.Fatalf("restore: %v", restored)
	}
	if _, err := os.Stat(filepath.Join(workspace, "api", "new.txt")); err != nil {
		t.Fatal(err)
	}
}
func TestGoHumanResumeAndFailedCheck(t *testing.T) {
	f := newFixture(t)
	calls := 0
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		calls++
		if calls == 1 {
			_ = os.MkdirAll(input.State, 0o700)
			return AgentResult{NativeID: "ses_native", NeedsInput: "Choose value", ActualProvider: "ollama-cloud"}, nil
		}
		if input.NativeID != "ses_native" {
			t.Fatalf("wrong resume native ID %q", input.NativeID)
		}
		_ = os.WriteFile(filepath.Join(input.Workspace, "api", "answer.txt"), []byte("approved"), 0o600)
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud"}, nil
	}
	added, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "human"))
	if err != nil {
		t.Fatal(err)
	}
	tid := added.TaskID
	first, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "needs_input" {
		t.Fatalf("first: %v", first)
	}
	f.sandbox.checkExit = 1
	second, err := f.service.RunTask(context.Background(), tid, "Use approved", false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "failed" {
		t.Fatalf("second: %v", second)
	}
	f.sandbox.checkExit = 0
	rechecked, err := f.service.VerifyCandidate(context.Background(), tid)
	if err != nil {
		t.Fatal(err)
	}
	if rechecked.Status != "verified" || calls != 2 {
		t.Fatalf("verify: %v calls=%d", rechecked, calls)
	}
}
func TestRetryCreatesNumberedCommitForSameTask(t *testing.T) {
	f := newFixture(t)
	calls := 0
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		calls++
		if err := os.WriteFile(filepath.Join(input.Workspace, "api", "change.txt"), []byte{byte('0' + calls)}, 0o600); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	added, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "retry-title"))
	if err != nil {
		t.Fatal(err)
	}
	f.sandbox.checkExit = 1
	first, err := f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute)
	if err != nil || first.Status != "failed" {
		t.Fatalf("first candidate: %+v %v", first, err)
	}
	f.sandbox.checkExit = 0
	second, err := f.service.RunTask(context.Background(), added.TaskID, "", true, time.Minute)
	if err != nil || second.Status != "verified" {
		t.Fatalf("revised candidate: %+v %v", second, err)
	}
	row, err := f.service.Store.Session(context.Background(), f.sid)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainOpen(filepath.Join(row.Workspace, "api"))
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	want := "feat: update both repositories (revision 2)\n\nTurnyard-Task: " + added.TaskID + "\n"
	if commit.Message != want || calls != 2 {
		t.Fatalf("retry commit = %q, calls=%d", commit.Message, calls)
	}
}
func TestGoTimeoutIsUnknown(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(AgentInvocation) (AgentResult, error) {
		return AgentResult{}, fault.New(fault.CodeAgentTimeout, "timeout")
	}
	added, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "timeout"))
	if err != nil {
		t.Fatal(err)
	}
	tid := added.TaskID
	if _, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute); err == nil {
		t.Fatal("expected timeout")
	}
	task, err := f.service.Store.Task(context.Background(), tid)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "unknown" {
		t.Fatalf("status %s", task.Status)
	}
	if _, err := f.service.RunTask(context.Background(), tid, "", true, time.Minute); ErrorCode(err) != "INVALID_TRANSITION" {
		t.Fatalf("retry: %v", err)
	}
	if _, err := f.service.CancelSession(context.Background(), f.sid, "stop after timeout"); ErrorCode(err) != "INVALID_TRANSITION" {
		t.Fatalf("unknown task cancelled without reconciliation: %v", err)
	}
	if _, err := f.service.ReconcileFailed(context.Background(), tid); err != nil {
		t.Fatalf("explicit unknown reconciliation: %v", err)
	}
	task, err = f.service.Store.Task(context.Background(), tid)
	if err != nil || task.Status != "failed" || task.ErrorCode.Value != "RECONCILED_UNKNOWN" {
		t.Fatalf("reconciled state: %+v %v", task, err)
	}
	if _, err := f.service.CancelSession(context.Background(), f.sid, "stop after timeout"); err != nil {
		t.Fatalf("reconciled task cannot be cancelled: %v", err)
	}
}

func TestPreflightFailureIsObservableAndRetryable(t *testing.T) {
	f := newFixture(t)
	added, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "preflight"))
	if err != nil {
		t.Fatal(err)
	}
	tid := added.TaskID
	row, err := f.service.Store.Session(context.Background(), f.sid)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(row.Workspace, "api", "README.txt")
	if err := os.WriteFile(path, []byte("drift"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute); ErrorCode(err) != "WORKSPACE_DRIFT" {
		t.Fatalf("preflight drift: %v", err)
	}
	task, err := f.service.Store.Task(context.Background(), tid)
	if err != nil || task.Status != "failed" || task.ErrorCode.Value != "WORKSPACE_DRIFT" {
		t.Fatalf("unobservable preflight: %+v %v", task, err)
	}
	if err := os.WriteFile(path, []byte("api"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		return AgentResult{NativeID: "ses_native", ActualProvider: "ollama-cloud"}, nil
	}
	result, err := f.service.RunTask(context.Background(), tid, "", true, time.Minute)
	if err != nil || result.Status != "verified" {
		t.Fatalf("retry: %v %v", result, err)
	}
}

func TestSecondTaskPreflightRetryCreatesItsFeatureBranch(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(AgentInvocation) (AgentResult, error) { return AgentResult{NativeID: "ses_native"}, nil }
	first, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "first-task"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RunTask(context.Background(), first.TaskID, "", false, time.Minute); err != nil {
		t.Fatal(err)
	}
	second, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "second-task"))
	if err != nil {
		t.Fatal(err)
	}
	tid := second.TaskID
	row, err := f.service.Store.Session(context.Background(), f.sid)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(row.Workspace, "api", "README.txt")
	if err := os.WriteFile(path, []byte("drift"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute); ErrorCode(err) != "WORKSPACE_DRIFT" {
		t.Fatalf("preflight: %v", err)
	}
	if err := os.WriteFile(path, []byte("api"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.RunTask(context.Background(), tid, "", true, time.Minute)
	if err != nil || result.Status != "verified" {
		t.Fatalf("second task retry: %v %v", result, err)
	}
	status, err := gitstate.WorkspaceStatus(context.Background(), row.Workspace, []RepositorySpec{{ID: "api"}, {ID: "web"}})
	if err != nil || status["api"].Branch != "feature/"+tid {
		t.Fatalf("branch: %v %v", status, err)
	}
}

func TestPostAgentEvidenceFailureIsUnknown(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(AgentInvocation) (AgentResult, error) {
		return AgentResult{}, fault.New(fault.CodeModelEvidenceMissing, "no model metadata")
	}
	added, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	tid := added.TaskID
	if _, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute); ErrorCode(err) != "MODEL_EVIDENCE_MISSING" {
		t.Fatalf("run: %v", err)
	}
	task, err := f.service.Store.Task(context.Background(), tid)
	if err != nil || task.Status != "unknown" {
		t.Fatalf("unsafe retry state: %+v %v", task, err)
	}
}

func TestTaskBaseSurvivesHumanTurn(t *testing.T) {
	f := newFixture(t)
	row, err := f.service.Store.Session(context.Background(), f.sid)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := gitstate.WorkspaceStatus(context.Background(), row.Workspace, []RepositorySpec{{ID: "api"}, {ID: "web"}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		calls++
		name := "first.txt"
		needs := "decision"
		if calls == 2 {
			name, needs = "second.txt", ""
		}
		if err := os.WriteFile(filepath.Join(input.Workspace, "api", name), []byte(name), 0o600); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "ses_native", NeedsInput: needs}, nil
	}
	added, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "base"))
	if err != nil {
		t.Fatal(err)
	}
	tid := added.TaskID
	if _, err := f.service.RunTask(context.Background(), tid, "", false, time.Minute); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.RunTask(context.Background(), tid, "approved", false, time.Minute)
	if err != nil || result.Status != "verified" {
		t.Fatalf("second turn: %v %v", result, err)
	}
	candidate, err := f.service.Store.Candidate(context.Background(), result.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	var vector map[string]RepoVersion
	if err := json.Unmarshal([]byte(candidate.Vector), &vector); err != nil {
		t.Fatal(err)
	}
	if vector["api"].Base != initial["api"].Head || vector["api"].Head == vector["api"].Base {
		t.Fatalf("task base lost: %+v, initial=%s", vector["api"], initial["api"].Head)
	}
}

func TestFirstCheckpointRestoresEmptyNativeState(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		return AgentResult{NativeID: "ses_native"}, nil
	}
	added, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "first-checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if out.CheckpointID == "" {
		t.Fatal("first invocation did not return a checkpoint")
	}
	row, err := f.service.Store.Session(context.Background(), f.sid)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(filepath.Dir(row.Workspace), "agent-state")
	if err := os.RemoveAll(row.Workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(state); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Restore(context.Background(), out.CheckpointID); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(state); err != nil || !info.IsDir() {
		t.Fatalf("native state missing: %v", err)
	}
}

func TestGoScopeViolation(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		_ = os.WriteFile(filepath.Join(input.Workspace, "web", "README.txt"), []byte("illegal"), 0o600)
		return AgentResult{NativeID: "ses_native"}, nil
	}
	added, err := f.service.AddTask(context.Background(), f.sid, f.task(t, "scope", "read"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute)
	if ErrorCode(err) != "SCOPE_VIOLATION" {
		t.Fatalf("violation: %v", err)
	}
}
func TestGoRejectDuplicateJSONAndWrongSHA(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.root, "inputs", "duplicate.json")
	_ = os.WriteFile(path, []byte(`{"idempotency_key":"a","idempotency_key":"b"}`), 0o600)
	var out map[string]any
	if err := ReadJSON(path, "", &out); ErrorCode(err) != "INVALID_JSON" {
		t.Fatalf("duplicate: %v", err)
	}
	sessionPath := filepath.Join(f.root, "inputs", "session.json")
	var session SessionSpec
	if err := ReadJSON(sessionPath, "session", &session); err != nil {
		t.Fatal(err)
	}
	session.Repositories[0].Commit = "0000000000000000000000000000000000000000"
	session.IdempotencyKey = "wrong-commit"
	writeJSON(t, sessionPath, session)
	if _, err := f.service.CreateSession(context.Background(), sessionPath); ErrorCode(err) != "INVALID_REPOSITORY" {
		t.Fatalf("wrong SHA: %v", err)
	}
}
