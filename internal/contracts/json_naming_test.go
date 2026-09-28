package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestSessionJSONUsesSnakeCase(t *testing.T) {
	session := SessionSpec{SchemaVersion: SessionVersion, IdempotencyKey: "key", Environment: "environment.json", PrimaryAgent: "lead"}
	body, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"schema_version"`, `"idempotency_key"`, `"primary_agent"`} {
		if !strings.Contains(string(body), key) {
			t.Fatalf("serialized session omitted %s: %s", key, body)
		}
	}
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":"turnyard.session/v1","idempotencyKey":"key","repositories":[],"environment":"environment.json","primaryAgent":"lead"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var decoded SessionSpec
	if got := fault.CodeOf(ReadJSON(path, "session", &decoded)); got != fault.CodeInvalidSpec {
		t.Fatalf("old camelCase input was accepted: %s", got)
	}
}
