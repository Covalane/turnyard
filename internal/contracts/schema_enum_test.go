package contracts

import (
	"encoding/json"
	"slices"
	"testing"
)

// Embedded schemas are maintained separately from Go declarations. This
// checks the closed wire sets where a schema enum and named values coexist.
func TestSchemaEnumsMatchNamedValues(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		path   []string
		want   []string
	}{
		{"repository source", "session", []string{"properties", "repositories", "items", "properties", "type", "enum"},
			[]string{string(RepositorySourceLocalGit), string(RepositorySourceRemoteGit)}},
		{"sandbox network", "environment", []string{"properties", "sandbox", "properties", "network", "enum"},
			[]string{string(SandboxNetworkDefault), string(SandboxNetworkNone), string(SandboxNetworkModelOnly)}},
		{"tool search mode", "environment", []string{"properties", "tool_search", "properties", "mode", "enum"},
			[]string{string(ToolSearchLexical), string(ToolSearchLLMRerank)}},
		{"remote Git writes", "environment", []string{"properties", "git", "properties", "remote_writes", "enum"},
			[]string{string(GitRemoteWritesNone), string(GitRemoteWritesPush)}},
		{"repository scope", "work", []string{"properties", "scope", "properties", "repositories", "items", "properties", "mode", "enum"},
			[]string{string(ScopeRead), string(ScopeWrite)}},
		{"input source", "work", []string{"properties", "inputs", "items", "properties", "source", "properties", "kind", "enum"},
			[]string{string(InputFile), string(InputHTTPS), string(InputConnector)}},
		{"deliverable kind", "work", []string{"properties", "deliverables", "items", "properties", "kind", "enum"},
			[]string{string(DeliverableFile), string(DeliverableImage), string(DeliverableText), string(DeliverablePullRequest)}},
		{"delivery destination", "work", []string{"properties", "deliverables", "items", "properties", "destination", "properties", "kind", "enum"},
			[]string{string(DestinationLocal), string(DestinationConnector)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := schemaFiles.ReadFile("schemas/" + tc.schema + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var node any
			if err := json.Unmarshal(body, &node); err != nil {
				t.Fatal(err)
			}
			for _, key := range tc.path {
				object, ok := node.(map[string]any)
				if !ok {
					t.Fatalf("%s is not an object", key)
				}
				node, ok = object[key]
				if !ok {
					t.Fatalf("schema field %s is missing", key)
				}
			}
			values, ok := node.([]any)
			if !ok {
				t.Fatal("schema enum is missing")
			}
			got := make([]string, len(values))
			for i, value := range values {
				var ok bool
				got[i], ok = value.(string)
				if !ok {
					t.Fatalf("enum value %d is not a string", i)
				}
			}
			slices.Sort(got)
			slices.Sort(tc.want)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("schema enum %v differs from named values %v", got, tc.want)
			}
		})
	}
}
