package artifacts

import (
	"encoding/json"
	"errors"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"io"
	"os"
)

const (
	ClaimsVersion  = "turnyard.artifact-claims/v1"
	maxClaimsBytes = 128 << 10
)

type claimDocument struct {
	SchemaVersion string  `json:"schema_version"`
	Artifacts     []Claim `json:"artifacts"`
}

const ClaimPath = "/workspace/.turnyard-output/artifacts.json"

func NeedsClaims(specs []contracts.DeliverableSpec) bool {
	for _, spec := range specs {
		if spec.Kind == contracts.DeliverableText || spec.Kind == contracts.DeliverablePullRequest {
			return true
		}
	}
	return false
}

// ReadClaims confines the agent-written manifest to its invocation-specific
// output mount. A missing manifest becomes missing required outputs.
func ReadClaims(outputDir string) (map[string]Claim, error) {
	root, err := os.OpenRoot(outputDir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Claim{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat("artifacts.json")
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Claim{}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxClaimsBytes {
		return nil, fault.New(fault.CodeAgentOutputInvalid, "artifact claims must be a regular file of at most %d bytes", maxClaimsBytes)
	}
	file, err := root.Open("artifacts.json")
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Claim{}, nil
	}
	if err != nil {
		return nil, fault.Wrap(fault.CodeAgentOutputInvalid, "read artifact claims", err, "cannot read artifact claims")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxClaimsBytes {
		return nil, fault.New(fault.CodeAgentOutputInvalid, "artifact claims must be a regular file of at most %d bytes", maxClaimsBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxClaimsBytes+1))
	decoder.DisallowUnknownFields()
	var document claimDocument
	if err := decoder.Decode(&document); err != nil || document.SchemaVersion != ClaimsVersion {
		return nil, fault.New(fault.CodeAgentOutputInvalid, "invalid artifact claims document")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fault.New(fault.CodeAgentOutputInvalid, "artifact claims have trailing content")
	}
	claims := make(map[string]Claim, len(document.Artifacts))
	for _, claim := range document.Artifacts {
		if claim.ID == "" || claims[claim.ID].ID != "" || claim.Text != "" && claim.URL != "" {
			return nil, fault.New(fault.CodeAgentOutputInvalid, "artifact claims contain a duplicate or ambiguous ID")
		}
		claims[claim.ID] = claim
	}
	return claims, nil
}

// ClaimsFromResults freezes agent-declared values for a later recheck. It does
// not read a mutable state file after the first candidate has been recorded.
func ClaimsFromResults(results []Result) map[string]Claim {
	claims := make(map[string]Claim, len(results))
	for _, result := range results {
		if result.Text != "" || result.URL != "" {
			claims[result.ID] = Claim{ID: result.ID, Text: result.Text, URL: result.URL}
		}
	}
	return claims
}
