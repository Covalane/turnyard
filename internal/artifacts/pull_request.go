package artifacts

import (
	"context"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
)

func inspectPullRequest(ctx context.Context, spec contracts.DeliverableSpec, claim Claim, source contracts.RepositorySpec, version gitstate.RepoVersion, verifier PullRequestVerifier) Result {
	result := Result{ID: spec.ID, Kind: spec.Kind, Repository: spec.Repository, Base: spec.Base,
		Head: version.Head, Status: StatusMissing, Verification: VerificationProviderReadback, ErrorCode: string(fault.CodeDeliverableMissing)}
	if claim.ID == "" || claim.URL == "" {
		return result
	}
	result.URL = claim.URL
	if claim.Text != "" || verifier == nil {
		result.Status, result.ErrorCode = StatusUnverified, string(fault.CodeDeliverableUnverified)
		return result
	}
	snapshot, err := verifier.ReadPullRequest(ctx, source, claim.URL)
	if err != nil {
		result.Status, result.ErrorCode = StatusUnverified, string(fault.CodeDeliverableUnverified)
		return result
	}
	if snapshot.URL != claim.URL || snapshot.Base != spec.Base || snapshot.Head != version.Head || !snapshot.Open {
		result.Status, result.ErrorCode = StatusMismatch, string(fault.CodeDeliverableMismatch)
		return result
	}
	result.Status, result.ErrorCode = StatusPresent, ""
	return result
}
