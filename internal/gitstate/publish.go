package gitstate

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/Covalane/turnyard/internal/fault"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

const publishRemoteName = "turnyard-publish"

// PublishFeature pushes only the candidate's feature branch. The branch and
// commit tree must still match the verified version before any remote write.
// A read-back distinguishes a confirmed push from an uncertain transport error.
func PublishFeature(ctx context.Context, workspace, repoID, target string, version RepoVersion) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if version.Branch == "" || !plumbing.IsHash(version.Head) || !plumbing.IsHash(version.Tree) {
		return false, fault.New(fault.CodeCandidateCorrupt, "invalid version for %s", repoID)
	}
	r, _, err := OpenRepo(filepath.Join(workspace, repoID))
	if err != nil {
		return false, err
	}
	branch := plumbing.NewBranchReferenceName(version.Branch)
	ref, err := r.Reference(branch, true)
	if err != nil || ref.Hash().String() != version.Head {
		return false, fault.New(fault.CodeCandidateStale, "%s branch no longer matches candidate", repoID)
	}
	commit, err := r.CommitObject(ref.Hash())
	if err != nil || commit.TreeHash.String() != version.Tree {
		return false, fault.New(fault.CodeCandidateStale, "%s tree no longer matches candidate", repoID)
	}
	auth, err := AuthForURL(ctx, target)
	if err != nil {
		return false, err
	}
	remote := git.NewRemote(r.Storer, &config.RemoteConfig{Name: publishRemoteName, URLs: []string{target}})
	remoteHead := func(readCtx context.Context) (string, error) {
		refs, err := remote.ListContext(readCtx, &git.ListOptions{Auth: auth})
		if errors.Is(err, transport.ErrEmptyRemoteRepository) {
			return "", nil
		}
		if err != nil {
			return "", fault.Wrap(remoteErrorCode(err), "list remote branch", err, "cannot read remote refs for %s", repoID)
		}
		for _, item := range refs {
			if item.Name() == branch {
				return item.Hash().String(), nil
			}
		}
		return "", nil
	}
	before, err := remoteHead(ctx)
	if err != nil {
		return false, err
	}
	if before == version.Head {
		return false, nil
	}
	if before != "" {
		return false, fault.New(fault.CodeBranchDrift, "%s remote branch %s already exists at another commit", repoID, version.Branch)
	}
	refspec := config.RefSpec(branch.String() + ":" + branch.String())
	pushErr := remote.PushContext(ctx, &git.PushOptions{RemoteName: publishRemoteName, Auth: auth, RefSpecs: []config.RefSpec{refspec}})
	// A timeout can occur after the remote accepted the write. Reconcile before
	// reporting a failure; callers can safely repeat a later publish attempt.
	readCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
	}
	after, readErr := remoteHead(readCtx)
	if readErr == nil && after == version.Head {
		return true, nil
	}
	if pushErr != nil && !errors.Is(pushErr, git.NoErrAlreadyUpToDate) {
		return false, fault.Wrap(remoteErrorCode(pushErr), "push feature branch", pushErr, "push outcome for %s requires reconciliation", repoID)
	}
	if readErr != nil {
		return false, readErr
	}
	return false, fault.New(fault.CodeBranchDrift, "%s remote branch %s did not match candidate after push", repoID, version.Branch)
}
