package contracts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitPolicyIsRequiredOnlyForRepositorySessions(t *testing.T) {
	dir := t.TempDir()
	environment := `{"schema_version":"turnyard.environment/v1","sandbox":{"backend":"docker","image":"agent:test"},"agents":[{"id":"lead","runtime":"opencode","model_binding":"cloud"}],"model_bindings":[{"id":"cloud","provider":"ollama-cloud","model":"test","credential_env":"OLLAMA_API_KEY"}]}`
	if err := os.WriteFile(filepath.Join(dir, "environment.json"), []byte(environment), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.json")
	writeSession := func(repositories string) {
		t.Helper()
		body := `{"schema_version":"turnyard.session/v1","idempotency_key":"test","repositories":` + repositories + `,"environment":"environment.json","primary_agent":"lead"}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSession(`[]`)
	if _, _, err := LoadSession(path); err != nil {
		t.Fatalf("repository-free session should not require Git: %v", err)
	}
	writeSession(`[{"id":"app","type":"local-git","path":".","commit":"0000000000000000000000000000000000000000"}]`)
	if _, _, err := LoadSession(path); err == nil {
		t.Fatal("repository session accepted without Git policy")
	}
}

func TestLoadSessionRejectsInvalidCustomModelEndpoint(t *testing.T) {
	dir := t.TempDir()
	session := `{"schema_version":"turnyard.session/v1","idempotency_key":"model","repositories":[],"environment":"environment.json","primary_agent":"lead"}`
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"http://models.example.test/v1", "https://user:secret@models.example.test/v1", "https://models.example.test/v1?token=x"} {
		env := `{"schema_version":"turnyard.environment/v1","sandbox":{"backend":"docker","image":"agent:test"},"agents":[{"id":"lead","runtime":"opencode","model_binding":"cloud"}],"model_bindings":[{"id":"cloud","provider":"custom","model":"test","credential_env":"CUSTOM_API_KEY","endpoints":{"openai_chat":"` + endpoint + `"}}]}`
		if err := os.WriteFile(filepath.Join(dir, "environment.json"), []byte(env), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadSession(filepath.Join(dir, "session.json")); err == nil {
			t.Errorf("invalid endpoint %q was accepted", endpoint)
		}
	}
}

func TestLoadSessionKeepsSandboxIsolationProfile(t *testing.T) {
	dir := t.TempDir()
	session := `{"schema_version":"turnyard.session/v1","idempotency_key":"isolation","repositories":[],"environment":"environment.json","primary_agent":"lead"}`
	environment := `{"schema_version":"turnyard.environment/v1","sandbox":{"backend":"docker","image":"agent:test","isolation":"gvisor","network":"model-only"},"agents":[{"id":"lead","runtime":"opencode","model_binding":"cloud"}],"model_bindings":[{"id":"cloud","provider":"ollama-cloud","model":"test","credential_env":"OLLAMA_API_KEY"}]}`
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "environment.json"), []byte(environment), 0o600); err != nil {
		t.Fatal(err)
	}
	_, env, err := LoadSession(filepath.Join(dir, "session.json"))
	if err != nil || env.Sandbox.Isolation != SandboxIsolationGVisor || env.Sandbox.Network != SandboxNetworkModelOnly {
		t.Fatalf("sandbox isolation was not preserved: %+v %v", env.Sandbox, err)
	}
}
