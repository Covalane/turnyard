package engine

import (
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"
)

func TestDelegatedModelDependencyClosure(t *testing.T) {
	primary := contracts.ModelBinding{ID: "main"}
	search := contracts.ModelBinding{ID: "search"}
	for _, tc := range []struct {
		name   string
		search *contracts.ToolSearchSpec
		want   int
		valid  bool
	}{
		{"no search", nil, 1, true},
		{"lexical", &contracts.ToolSearchSpec{Mode: contracts.ToolSearchLexical}, 1, true},
		{"shared model", &contracts.ToolSearchSpec{Mode: contracts.ToolSearchLLMRerank, ModelBinding: "main"}, 1, true},
		{"separate model", &contracts.ToolSearchSpec{Mode: contracts.ToolSearchLLMRerank, ModelBinding: "search"}, 2, true},
		{"missing model", &contracts.ToolSearchSpec{Mode: contracts.ToolSearchLLMRerank, ModelBinding: "missing"}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := contracts.EnvironmentSpec{ModelBindings: []contracts.ModelBinding{primary, search, {ID: "unrelated"}},
				Agents: []contracts.AgentSpec{{ID: "helper", ModelBinding: "main"}}, ToolSearch: tc.search}
			env.ModelBindings = delegatedModelBindings(env, primary)
			if len(env.ModelBindings) != tc.want {
				t.Fatalf("unexpected dependency closure: %+v", env.ModelBindings)
			}
			if err := contracts.ValidateModelReferences(env); (err == nil) != tc.valid {
				t.Fatalf("reference validation=%v", err)
			}
		})
	}
}
