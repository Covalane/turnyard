package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func artifactFixture(t *testing.T, connector bool) *fixture {
	t.Helper()
	root := t.TempDir()
	env := EnvironmentSpec{SchemaVersion: contracts.EnvironmentVersion,
		Sandbox:       SandboxSpec{Backend: "apple-container", Image: "test:latest"},
		Agents:        []AgentSpec{{ID: "lead", Runtime: "opencode", ModelBinding: "model"}},
		ModelBindings: []ModelBinding{{ID: "model", Provider: "ollama-cloud", Model: "glm-5.3-flash", CredentialEnv: "MODEL_API_KEY"}}}
	if connector {
		env.ArtifactConnectors = []contracts.ArtifactConnectorSpec{{ID: "object", URIPrefix: "mem://bucket/reports/",
			GetArgv: []string{os.Args[0], "-test.run=^TestArtifactConnectorHelperProcess$", "--", "get", "{uri}", "{file}"},
			PutArgv: []string{os.Args[0], "-test.run=^TestArtifactConnectorHelperProcess$", "--", "put", "{uri}", "{file}"},
			PassEnv: []string{"TURNYARD_FAKE_BLOB_ROOT", "TURNYARD_FAKE_PUT_FAIL_AFTER_WRITE", "TURNYARD_FAKE_CONNECTOR"}}}
	}
	inputRoot := filepath.Join(root, "specs")
	writeJSON(t, filepath.Join(inputRoot, "environment.json"), env)
	writeJSON(t, filepath.Join(inputRoot, "session.json"), SessionSpec{SchemaVersion: contracts.SessionVersion,
		IdempotencyKey: "artifact-session", Repositories: []RepositorySpec{}, Environment: "environment.json", PrimaryAgent: "lead"})
	service, err := NewService(context.Background(), filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	sb := &testSandbox{}
	driver := &testDriver{}
	service.BackendFactory = func(string) (SandboxBackend, error) { return sb, nil }
	service.DriverFactory = func(string) (AgentDriver, error) { return driver, nil }
	created, err := service.CreateSession(context.Background(), filepath.Join(inputRoot, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{service: service, root: root, sid: created.SessionID, driver: driver, sandbox: sb}
}

func artifactWork(t *testing.T, f *fixture, output contracts.DeliverableSpec, input []contracts.InputSpec) string {
	t.Helper()
	work := WorkSpec{SchemaVersion: contracts.WorkVersion, IdempotencyKey: "artifact-work", Objective: "Produce a report from the attached file",
		Acceptance: []string{"The report is available at the declared destination"}, Checks: []string{}, Inputs: input,
		Deliverables: []contracts.DeliverableSpec{output}}
	work.Scope.Repositories = []ScopeRepo{}
	path := filepath.Join(f.root, "specs", "work.json")
	writeJSON(t, path, work)
	return path
}

func TestAttachmentAndLocalOutputWithoutRepository(t *testing.T) {
	f := artifactFixture(t, false)
	source := filepath.Join(f.root, "specs", "diagram.txt")
	if err := os.WriteFile(source, []byte("diagram source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := contracts.DeliverableSpec{ID: "report", Kind: contracts.DeliverableFile, Path: "report.txt",
		MediaType: "text/plain", Destination: &contracts.DestinationSpec{Kind: contracts.DestinationLocal}}
	path := artifactWork(t, f, output, []contracts.InputSpec{{ID: "source", Source: contracts.InputSource{Kind: contracts.InputFile, Path: "diagram.txt"}}})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("source changed after staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	replayed, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil || !replayed.Replayed || replayed.TaskID != added.TaskID {
		t.Fatalf("idempotent add fetched changed source: %+v %v", replayed, err)
	}
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		if input.InputDir == "" || !strings.Contains(input.Prompt, "/workspace/.turnyard-input/") {
			t.Fatalf("agent did not receive the attachment path: %+v", input)
		}
		entries, err := os.ReadDir(input.InputDir)
		if err != nil {
			return AgentResult{}, err
		}
		data, err := os.ReadFile(filepath.Join(input.InputDir, entries[0].Name(), "source"))
		if err != nil || string(data) != "diagram source\n" {
			t.Fatalf("attachment was not snapshotted: %q %v", data, err)
		}
		if err := os.MkdirAll(filepath.Join(input.ArtifactDir, "files"), 0o700); err != nil {
			return AgentResult{}, err
		}
		if err := os.WriteFile(filepath.Join(input.ArtifactDir, "files", "report.txt"), []byte("completed report\n"), 0o600); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "artifact-native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	out, err := f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute)
	if err != nil || out.Status != "verified" || len(out.Deliverables) != 1 || out.Deliverables[0].LocalPath == "" {
		t.Fatalf("local artifact task: %+v %v", out, err)
	}
	data, err := os.ReadFile(out.Deliverables[0].LocalPath)
	if err != nil || string(data) != "completed report\n" {
		t.Fatalf("sealed output: %q %v", data, err)
	}
	if _, err := f.service.CompleteSession(context.Background(), f.sid); err != nil {
		t.Fatal(err)
	}
	row, err := f.service.Store.Task(context.Background(), added.TaskID)
	if err != nil || strings.Contains(row.Spec, "diagram.txt") || strings.Contains(row.Spec, "source changed") {
		t.Fatalf("task persisted mutable input source: %v %s", err, row.Spec)
	}
}

func TestConnectorOutputReadbackAndRecoveryAfterLostAcknowledgement(t *testing.T) {
	t.Setenv("TURNYARD_FAKE_CONNECTOR", "1")
	t.Setenv("TURNYARD_FAKE_PUT_FAIL_AFTER_WRITE", "1")
	blobRoot := t.TempDir()
	t.Setenv("TURNYARD_FAKE_BLOB_ROOT", blobRoot)
	inputBytes := []byte("remote attachment")
	if err := os.WriteFile(filepath.Join(blobRoot, "incoming.txt"), inputBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	inputHash := sha256.Sum256(inputBytes)
	f := artifactFixture(t, true)
	content := []byte("published report\n")
	sum := sha256.Sum256(content)
	spec := contracts.DeliverableSpec{ID: "report", Kind: contracts.DeliverableFile, Path: "report.txt",
		Destination: &contracts.DestinationSpec{Kind: contracts.DestinationConnector, Connector: "object", URI: "mem://bucket/reports/{sha256}.txt"}}
	path := artifactWork(t, f, spec, []contracts.InputSpec{{ID: "remote", Source: contracts.InputSource{Kind: contracts.InputConnector,
		Connector: "object", URI: "mem://bucket/reports/incoming.txt"}, ExpectedSHA256: hex.EncodeToString(inputHash[:])}})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		if err := os.MkdirAll(filepath.Join(input.ArtifactDir, "files"), 0o700); err != nil {
			return AgentResult{}, err
		}
		if err := os.WriteFile(filepath.Join(input.ArtifactDir, "files", "report.txt"), content, 0o600); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "artifact-native", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	out, err := f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute)
	if err != nil || out.Status != "failed" || out.Deliverables[0].Status != "unverified" {
		t.Fatalf("lost upload acknowledgement should fail closed: %+v %v", out, err)
	}
	uri := "mem://bucket/reports/" + hex.EncodeToString(sum[:]) + ".txt"
	if out.Deliverables[0].URI != uri {
		t.Fatalf("wrong delivery URI: %+v", out.Deliverables[0])
	}
	t.Setenv("TURNYARD_FAKE_PUT_FAIL_AFTER_WRITE", "")
	rechecked, err := f.service.VerifyCandidate(context.Background(), added.TaskID)
	if err != nil || rechecked.Status != "verified" || rechecked.Deliverables[0].URI != uri {
		t.Fatalf("readback did not recover uploaded bytes: %+v %v", rechecked, err)
	}
	if _, err := f.service.CompleteSession(context.Background(), f.sid); err != nil {
		t.Fatal(err)
	}
}

// TestArtifactConnectorHelperProcess runs only in the isolated helper process.
func TestArtifactConnectorHelperProcess(t *testing.T) {
	if os.Getenv("TURNYARD_FAKE_CONNECTOR") != "1" {
		return
	}
	index := -1
	for i, arg := range os.Args {
		if arg == "--" {
			index = i
			break
		}
	}
	if index < 0 || len(os.Args) != index+4 {
		os.Exit(2)
	}
	mode, uri, file := os.Args[index+1], os.Args[index+2], os.Args[index+3]
	if !strings.HasPrefix(uri, "mem://bucket/reports/") {
		os.Exit(2)
	}
	remote := filepath.Join(os.Getenv("TURNYARD_FAKE_BLOB_ROOT"), strings.TrimPrefix(uri, "mem://bucket/reports/"))
	if mode == "get" {
		data, err := os.ReadFile(remote)
		if err != nil || os.WriteFile(file, data, 0o600) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if mode != "put" {
		os.Exit(2)
	}
	from, err := os.Open(file)
	if err != nil {
		os.Exit(1)
	}
	to, err := os.OpenFile(remote, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		os.Exit(1)
	}
	_, err = io.Copy(to, from)
	_ = from.Close()
	_ = to.Close()
	if err != nil || os.Getenv("TURNYARD_FAKE_PUT_FAIL_AFTER_WRITE") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestInputSnapshotTamperPreventsExecution(t *testing.T) {
	f := artifactFixture(t, false)
	source := filepath.Join(f.root, "specs", "input.txt")
	if err := os.WriteFile(source, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := artifactWork(t, f, contracts.DeliverableSpec{ID: "report", Kind: contracts.DeliverableFile, Path: "out.txt", Destination: &contracts.DestinationSpec{Kind: contracts.DestinationLocal}},
		[]contracts.InputSpec{{ID: "input", Source: contracts.InputSource{Kind: contracts.InputFile, Path: "input.txt"}}})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	row, err := f.service.Store.Task(context.Background(), added.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var work contracts.WorkSpec
	if err := json.Unmarshal([]byte(row.Spec), &work); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(f.service.Store.Root, "sessions", f.sid, "inputs", work.Inputs[0].Source.Path)
	if err := os.WriteFile(inputPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	f.driver.run = func(AgentInvocation) (AgentResult, error) { called = true; return AgentResult{}, nil }
	if _, err := f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute); ErrorCode(err) != "CANDIDATE_CORRUPT" || called {
		t.Fatalf("tampered attachment reached agent: called=%v err=%v", called, err)
	}
}

func TestInterruptedCheckRetainsSealedCandidateForReverify(t *testing.T) {
	f := newFixture(t)
	f.driver.run = func(input AgentInvocation) (AgentResult, error) {
		if err := os.MkdirAll(filepath.Join(input.ArtifactDir, "files"), 0o700); err != nil {
			return AgentResult{}, err
		}
		if err := os.WriteFile(filepath.Join(input.ArtifactDir, "files", "report.txt"), []byte("sealed result"), 0o600); err != nil {
			return AgentResult{}, err
		}
		return AgentResult{NativeID: "native-check", ActualProvider: "ollama-cloud", ActualModel: "glm-5.3-flash"}, nil
	}
	f.sandbox.checkErr = fault.New(fault.CodeCheckError, "simulated checker interruption")
	path := deliveryWork(t, f, "interrupted-check", []contracts.DeliverableSpec{{ID: "report", Kind: contracts.DeliverableFile,
		Path: "report.txt", Destination: &contracts.DestinationSpec{Kind: contracts.DestinationLocal}}})
	added, err := f.service.AddTask(context.Background(), f.sid, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RunTask(context.Background(), added.TaskID, "", false, time.Minute); err == nil {
		t.Fatal("interrupted check should not pass")
	}
	current, err := f.service.TaskResult(context.Background(), added.TaskID)
	if err != nil || current.Task.Status != "unknown" || current.Candidate == nil || len(current.Candidate.Deliverables) != 1 ||
		current.Candidate.Deliverables[0].SHA256 == "" {
		t.Fatalf("ready candidate lost its output manifest: %+v %v", current, err)
	}
	if _, err := f.service.ReconcileFailed(context.Background(), added.TaskID); err != nil {
		t.Fatal(err)
	}
	f.sandbox.checkErr = nil
	rechecked, err := f.service.VerifyCandidate(context.Background(), added.TaskID)
	if err != nil || rechecked.Status != "verified" {
		t.Fatalf("sealed candidate could not be reverified: %+v %v", rechecked, err)
	}
}
