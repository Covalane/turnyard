//go:build integration

package e2e

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

// TestRealOSSConnector is opt-in because it creates and then removes a real
// object. It checks the supervisor connector's upload and byte-for-byte
// readback, while the agent itself only produces the file.
func TestRealOSSConnector(t *testing.T) {
	if os.Getenv("TURNYARD_OSS_E2E") != "1" {
		t.Skip("set TURNYARD_OSS_E2E=1 with a writable OSS test prefix")
	}
	prefix, region := os.Getenv("TURNYARD_OSS_E2E_PREFIX"), os.Getenv("TURNYARD_OSS_E2E_REGION")
	if !strings.HasPrefix(prefix, "oss://") || !strings.HasSuffix(prefix, "/") || region == "" {
		t.Fatal("TURNYARD_OSS_E2E_PREFIX must be an oss://bucket/prefix/ and TURNYARD_OSS_E2E_REGION must be set")
	}
	if _, err := exec.LookPath("aliyun"); err != nil {
		t.Fatalf("aliyun CLI is required: %v", err)
	}
	requireCredential(t)
	h := newHarness(t, "oss-go")
	h.configEvidence()
	marker := contracts.NewID("oss")
	env := h.environment([]contracts.CheckSpec{{ID: "contains-marker", Argv: []string{"grep", "-Fq", marker, "/workspace/.turnyard-output/report"}, Repositories: []string{}}})
	env.Git = contracts.GitPolicy{}
	env.ArtifactConnectors = []contracts.ArtifactConnectorSpec{{
		ID: "oss", URIPrefix: prefix,
		GetArgv:        []string{"aliyun", "ossutil", "cp", "{uri}", "{file}", "--region", region, "-f"},
		PutArgv:        []string{"aliyun", "ossutil", "cp", "{file}", "{uri}", "--region", region, "--ignore-existing", "-f"},
		TimeoutSeconds: 90,
	}}
	h.save("environment.json", env)
	session := contracts.SessionSpec{SchemaVersion: contracts.SessionVersion, IdempotencyKey: "real-oss-connector",
		Repositories: []contracts.RepositorySpec{}, Environment: "environment.json", PrimaryAgent: "lead"}
	sid := required(t, h.invoke("session", "create", "--file", h.save("session.json", session)), "sessionId")
	work := contracts.WorkSpec{SchemaVersion: contracts.WorkVersion, IdempotencyKey: marker,
		Objective:  fmt.Sprintf("在交付文件 report.txt 中写一句中文说明，并包含测试标记 %s。", marker),
		Acceptance: []string{"交付文件包含本次测试标记"}, Checks: []string{"contains-marker"},
		Deliverables: []contracts.DeliverableSpec{{ID: "report", Kind: contracts.DeliverableFile, Path: "report.txt",
			MediaType: "text/plain", Destination: &contracts.DestinationSpec{Kind: contracts.DestinationConnector,
				Connector: "oss", URI: prefix + "{sha256}.txt"}}}}
	work.Scope.Repositories = []contracts.ScopeRepo{}
	added := h.invoke("task", "add", sid, "--file", h.save("work.json", work))
	tid := required(t, added, "taskId")
	h.invoke("task", "run", tid)
	result := h.invoke("task", "wait", tid)
	items, _ := value(result, "candidate", "deliverables").([]any)
	var uri string
	if len(items) == 1 {
		item, _ := items[0].(map[string]any)
		uri = str(item, "uri")
	}
	if uri != "" {
		t.Cleanup(func() {
			if err := exec.Command("aliyun", "ossutil", "rm", uri, "--region", region, "-f").Run(); err != nil {
				t.Errorf("remove OSS test object %s: %v", uri, err)
			}
		})
	}
	h.verified("oss", result)
	if len(items) != 1 {
		t.Fatalf("expected one OSS deliverable: %v", items)
	}
	item, _ := items[0].(map[string]any)
	if str(item, "status") != "present" || uri == "" || str(item, "sha256") == "" {
		t.Fatalf("OSS readback was not verified: %v", item)
	}
	// This independent CLI read checks the final object after Turnyard's own
	// connector readback, then the cleanup removes only this exact URI.
	path := filepath.Join(h.root, "oss-readback.txt")
	if err := exec.Command("aliyun", "ossutil", "cp", uri, path, "--region", region, "-f").Run(); err != nil {
		t.Fatalf("independent OSS readback failed: %v", err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(readFile(t, path)))
	if digest != str(item, "sha256") {
		t.Fatalf("OSS bytes mismatch: %s != %s", digest, str(item, "sha256"))
	}
	h.evidence["oss"] = map[string]any{"uri": uri, "sha256": digest, "readback": "matched"}
	completed := h.invoke("session", "complete", sid)
	if str(completed, "status") != "completed" {
		t.Fatalf("OSS session did not complete: %v", completed)
	}
	h.evidence["completion"] = completed
}
