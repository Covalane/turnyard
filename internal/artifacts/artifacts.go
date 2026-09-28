// Package artifacts turns required outputs into a candidate-bound delivery
// manifest. A claim is never treated as verification of an external effect.
package artifacts

import (
	"context"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
)

const (
	StatusPresent    = contracts.DeliverableStatusPresent
	StatusMissing    = contracts.DeliverableStatusMissing
	StatusInvalid    = contracts.DeliverableStatusInvalid
	StatusMismatch   = contracts.DeliverableStatusMismatch
	StatusUnverified = contracts.DeliverableStatusUnverified
)

// VerificationMethod describes the evidence used for a delivery result.
type VerificationMethod string

const (
	VerificationGitTree               VerificationMethod = "git_tree"
	VerificationGitTreeImageSignature VerificationMethod = "git_tree_and_image_signature"
	VerificationSealedFileSHA256      VerificationMethod = "sealed_file_sha256"
	VerificationTextPresence          VerificationMethod = "text_presence"
	VerificationProviderReadback      VerificationMethod = "provider_readback"
	VerificationConnectorReadback     VerificationMethod = "connector_readback_sha256"
)

// Claim is an agent's declaration. Only text and remote references need one;
// repository files are discovered from the recorded Git tree instead.
type Claim struct {
	ID   string `json:"id"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Result is the common output envelope. Type-specific locations and content
// are optional, while ID, kind, status, and verification always have meaning.
type Result struct {
	ID             string                      `json:"id"`
	Kind           contracts.DeliverableKind   `json:"kind"`
	Status         contracts.DeliverableStatus `json:"status"`
	Verification   VerificationMethod          `json:"verification"`
	Repository     string                      `json:"repository,omitempty"`
	Path           string                      `json:"path,omitempty"`
	LocalPath      string                      `json:"localPath,omitempty"`
	URI            string                      `json:"uri,omitempty"`
	Commit         string                      `json:"commit,omitempty"`
	SHA256         string                      `json:"sha256,omitempty"`
	ExpectedSHA256 string                      `json:"expectedSha256,omitempty"`
	Bytes          int64                       `json:"bytes,omitempty"`
	MediaType      string                      `json:"mediaType,omitempty"`
	Text           string                      `json:"text,omitempty"`
	URL            string                      `json:"url,omitempty"`
	Base           string                      `json:"base,omitempty"`
	Head           string                      `json:"head,omitempty"`
	ErrorCode      string                      `json:"errorCode,omitempty"`
}

// PullRequestVerifier is supplied by an SCM integration. It must authenticate
// the returned PR and bind it to the supplied repository, not just parse a URL.
type PullRequestVerifier interface {
	ReadPullRequest(context.Context, contracts.RepositorySpec, string) (PullRequestSnapshot, error)
}

type PullRequestSnapshot struct {
	URL  string
	Base string
	Head string
	Open bool
}

func Pass(results []Result) bool {
	for _, result := range results {
		if result.Status != StatusPresent {
			return false
		}
	}
	return true
}

// Inspect checks every required output against a pinned candidate. A PR can
// only pass when a configured verifier reads back its real head and base.
func Inspect(ctx context.Context, workspace string, sources []contracts.RepositorySpec, vector map[string]gitstate.RepoVersion, specs []contracts.DeliverableSpec, claims map[string]Claim, localPaths map[string]string, verifier PullRequestVerifier) ([]Result, error) {
	results := make([]Result, 0, len(specs))
	sourceByID := make(map[string]contracts.RepositorySpec, len(sources))
	for _, source := range sources {
		sourceByID[source.ID] = source
	}
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var result Result
		var err error
		switch spec.Kind {
		case contracts.DeliverableFile, contracts.DeliverableImage:
			if spec.Repository == "" {
				result, err = inspectLocalOutput(ctx, spec, localPaths[spec.ID])
			} else {
				result, err = inspectGitOutput(ctx, workspace, vector, spec)
				if spec.Destination != nil && spec.Destination.Kind == contracts.DestinationConnector {
					result.LocalPath = localPaths[spec.ID]
				}
			}
		case contracts.DeliverableText:
			result = inspectText(spec, claims[spec.ID])
		case contracts.DeliverablePullRequest:
			result = inspectPullRequest(ctx, spec, claims[spec.ID], sourceByID[spec.Repository], vector[spec.Repository], verifier)
		default:
			return nil, fault.New(fault.CodeInvalidSpec, "unsupported deliverable kind %s", spec.Kind)
		}
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}
