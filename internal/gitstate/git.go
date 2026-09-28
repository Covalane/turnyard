package gitstate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

type RepoStatus struct {
	Head   string `json:"head"`
	Branch string `json:"branch"`
	Dirty  bool   `json:"dirty"`
}
type RepoVersion struct {
	Base   string `json:"base"`
	Head   string `json:"head"`
	Tree   string `json:"tree"`
	Branch string `json:"branch,omitempty"`
}
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func OpenRepo(path string) (*git.Repository, *git.Worktree, error) {
	r, err := git.PlainOpen(path)
	if err != nil {
		return nil, nil, fault.Wrap(fault.CodeGitError, "", err, "open %s", path)
	}
	w, err := r.Worktree()
	if err != nil {
		return nil, nil, fault.Wrap(fault.CodeGitError, "", err, "worktree %s", path)
	}
	return r, w, nil
}
func PrepareRepositories(ctx context.Context, spec contracts.SessionSpec, workspace string) (runErr error) {
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			if err := os.RemoveAll(workspace); err != nil {
				runErr = fault.At(errors.Join(runErr, err), "remove incomplete repository workspace")
			}
		}
	}()
	for _, source := range spec.Repositories {
		if err := ctx.Err(); err != nil {
			return err
		}
		cloneURL := source.Path
		var auth transport.AuthMethod
		if source.Type == contracts.RepositorySourceRemoteGit {
			cloneURL = source.URL
			var err error
			auth, err = AuthForURL(ctx, cloneURL)
			if err != nil {
				return err
			}
		} else {
			if _, err := os.Stat(filepath.Join(source.Path, ".git")); err != nil {
				return fault.New(fault.CodeInvalidRepository, "%s is not a repository root", source.ID)
			}
			src, _, err := OpenRepo(source.Path)
			if err != nil {
				return err
			}
			if _, err := src.CommitObject(plumbing.NewHash(source.Commit)); err != nil {
				return fault.Wrap(fault.CodeInvalidRepository, "", err, "%s commit %s is unavailable", source.ID, source.Commit)
			}
		}
		hash := plumbing.NewHash(source.Commit)
		dst := filepath.Join(workspace, source.ID)
		r, err := git.PlainCloneContext(ctx, dst, false, &git.CloneOptions{URL: cloneURL, Auth: auth, NoCheckout: true})
		if err != nil {
			return fault.Wrap(remoteErrorCode(err), "clone source", err, "clone %s", source.ID)
		}
		if _, err := r.CommitObject(hash); err != nil {
			return fault.Wrap(fault.CodeInvalidRepository, "", err, "%s commit %s is unavailable after clone", source.ID, source.Commit)
		}
		w, err := r.Worktree()
		if err != nil {
			return err
		}
		if err := w.Checkout(&git.CheckoutOptions{Hash: hash}); err != nil {
			return fault.Wrap(fault.CodeGitError, "", err, "checkout %s", source.ID)
		}
	}
	success = true
	return nil
}
func WorkspaceStatus(ctx context.Context, workspace string, sources []contracts.RepositorySpec) (map[string]RepoStatus, error) {
	out := map[string]RepoStatus{}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, w, err := OpenRepo(filepath.Join(workspace, source.ID))
		if err != nil {
			return nil, err
		}
		head, err := r.Head()
		if err != nil {
			return nil, err
		}
		status, err := w.Status()
		if err != nil {
			return nil, err
		}
		branch := ""
		if head.Name().IsBranch() {
			branch = head.Name().Short()
		}
		out[source.ID] = RepoStatus{head.Hash().String(), branch, !status.IsClean()}
	}
	return out, nil
}
func BeginFeature(ctx context.Context, workspace, taskID string, scope []contracts.ScopeRepo) (map[string]string, error) {
	bases := map[string]string{}
	for _, item := range scope {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, w, err := OpenRepo(filepath.Join(workspace, item.ID))
		if err != nil {
			return nil, err
		}
		head, err := r.Head()
		if err != nil {
			return nil, err
		}
		bases[item.ID] = head.Hash().String()
		if item.Mode == contracts.ScopeWrite {
			name := plumbing.NewBranchReferenceName("feature/" + taskID)
			if head.Name() == name {
				continue
			}
			if err := w.Checkout(&git.CheckoutOptions{Branch: name, Create: true, Hash: head.Hash()}); err != nil {
				return nil, fault.Wrap(fault.CodeGitError, "", err, "create %s", name)
			}
		}
	}
	return bases, nil
}
func EnsureFeature(ctx context.Context, workspace, taskID string, scope []contracts.ScopeRepo) (map[string]string, error) {
	bases := map[string]string{}
	for _, item := range scope {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, w, err := OpenRepo(filepath.Join(workspace, item.ID))
		if err != nil {
			return nil, err
		}
		head, err := r.Head()
		if err != nil {
			return nil, err
		}
		bases[item.ID] = head.Hash().String()
		if item.Mode != contracts.ScopeWrite {
			continue
		}
		wanted := plumbing.NewBranchReferenceName("feature/" + taskID)
		if head.Name() == wanted {
			continue
		}
		if head.Name().IsBranch() {
			return nil, fault.New(fault.CodeBranchDrift, "%s branch is %s", item.ID, head.Name())
		}
		if err := w.Checkout(&git.CheckoutOptions{Branch: wanted, Create: true, Hash: head.Hash(), Keep: true}); err != nil {
			return nil, fault.Wrap(fault.CodeBranchDrift, "", err, "%s cannot resume", item.ID)
		}
	}
	return bases, nil
}
func Finalize(ctx context.Context, workspace, taskID string, session contracts.SessionSpec, work contracts.WorkSpec, taskBases, invocationHeads map[string]string) (map[string]RepoVersion, error) {
	modes := map[string]contracts.ScopeMode{}
	for _, r := range work.Scope.Repositories {
		modes[r.ID] = r.Mode
	}
	vector := map[string]RepoVersion{}
	for _, source := range session.Repositories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, w, err := OpenRepo(filepath.Join(workspace, source.ID))
		if err != nil {
			return nil, err
		}
		head, err := r.Head()
		if err != nil {
			return nil, err
		}
		status, err := w.Status()
		if err != nil {
			return nil, err
		}
		if modes[source.ID] == contracts.ScopeRead {
			if head.Hash().String() != invocationHeads[source.ID] || !status.IsClean() {
				return nil, fault.New(fault.CodeScopeViolation, "read-only repository %s changed", source.ID)
			}
		} else {
			wanted := "feature/" + taskID
			if !head.Name().IsBranch() || head.Name().Short() != wanted {
				return nil, fault.New(fault.CodeBranchDrift, "%s is not on %s", source.ID, wanted)
			}
			if head.Hash().String() != invocationHeads[source.ID] {
				return nil, fault.New(fault.CodeHeadDrift, "%s HEAD changed during agent run", source.ID)
			}
			revision, err := taskRevision(r, taskBases[source.ID], invocationHeads[source.ID])
			if err != nil {
				return nil, fault.Wrap(fault.CodeGitError, "", err, "inspect task history in %s", source.ID)
			}
			if err := w.AddWithOptions(&git.AddOptions{All: true}); err != nil {
				return nil, fault.Wrap(fault.CodeGitError, "", err, "stage %s", source.ID)
			}
			// go-git returns ErrEmptyCommit for unchanged repositories.
			_, err = w.Commit(taskCommitMessage(work, taskID, revision), &git.CommitOptions{Author: &object.Signature{Name: "Turnyard", Email: "turnyard@localhost", When: time.Now()}})
			if err != nil && err != git.ErrEmptyCommit {
				return nil, fault.Wrap(fault.CodeGitError, "", err, "commit %s", source.ID)
			}
		}
		head, err = r.Head()
		if err != nil {
			return nil, err
		}
		commit, err := r.CommitObject(head.Hash())
		if err != nil {
			return nil, err
		}
		status, err = w.Status()
		if err != nil {
			return nil, err
		}
		if !status.IsClean() {
			return nil, fault.New(fault.CodeDirtyCandidate, "%s has uncommitted files", source.ID)
		}
		branch := ""
		if head.Name().IsBranch() {
			branch = head.Name().Short()
		}
		vector[source.ID] = RepoVersion{taskBases[source.ID], head.Hash().String(), commit.TreeHash.String(), branch}
	}
	return vector, nil
}
