//go:build integration

package toolgateway

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRealModelRanksGrantedTool(t *testing.T) {
	if os.Getenv("TURNYARD_MATCHER_E2E") != "1" {
		t.Skip("set TURNYARD_MATCHER_E2E=1 for a real model ranking request")
	}
	credentialEnv := os.Getenv("TURNYARD_MATCHER_CREDENTIAL_ENV")
	if credentialEnv == "" || os.Getenv(credentialEnv) == "" {
		t.Fatal("TURNYARD_MATCHER_CREDENTIAL_ENV must name an available model credential")
	}
	endpoint, model := os.Getenv("TURNYARD_MATCHER_ENDPOINT"), os.Getenv("TURNYARD_MATCHER_MODEL")
	if endpoint == "" || model == "" {
		t.Fatal("TURNYARD_MATCHER_ENDPOINT and TURNYARD_MATCHER_MODEL are required")
	}
	matcher, err := NewLLMMatcher(MatcherConfig{Endpoint: endpoint, Model: model, CredentialEnv: credentialEnv})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ids, err := matcher.Rank(ctx, "Generate a witness token from a value", []candidate{
		{ID: "stamp_cli/run", Description: "Generate a witness token from an input value"},
		{ID: "docs/search", Description: "Search reference documentation"},
	})
	if err != nil || len(ids) == 0 || ids[0] != "stamp_cli/run" {
		t.Fatalf("real model ranking failed: ids=%v err=%v", ids, err)
	}
}
