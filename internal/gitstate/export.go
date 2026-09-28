package gitstate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
)

const maxDelegatedFile = 512 << 20
const maxDelegatedChanges = 2 << 30

// ChangeOperation is the operation the parent may apply to a delegated file.
type ChangeOperation string

const (
	ChangeWrite  ChangeOperation = "write"
	ChangeDelete ChangeOperation = "delete"
)

type ExportedChange struct {
	Repository string          `json:"repository"`
	Path       string          `json:"path"`
	Operation  ChangeOperation `json:"operation"`
	Source     string          `json:"source,omitempty"`
	SHA256     string          `json:"sha256,omitempty"`
	Mode       string          `json:"mode,omitempty"`
}

// ExportChanges copies exact blobs from a verified child candidate into a
// handoff directory. The parent may inspect and integrate them, but Turnyard
// never writes a child result into the parent's live repository behind it.
func ExportChanges(ctx context.Context, repoPath, repoID, base, head, destination string) ([]ExportedChange, error) {
	repo, _, err := OpenRepo(repoPath)
	if err != nil {
		return nil, err
	}
	baseCommit, err := repo.CommitObject(plumbing.NewHash(base))
	if err != nil {
		return nil, err
	}
	headCommit, err := repo.CommitObject(plumbing.NewHash(head))
	if err != nil {
		return nil, err
	}
	baseTree, err := baseCommit.Tree()
	if err != nil {
		return nil, err
	}
	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, err
	}
	changes, err := baseTree.DiffContext(ctx, headTree)
	if err != nil {
		return nil, err
	}
	result := make([]ExportedChange, 0, len(changes))
	var total int64
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		from, to, err := change.Files()
		if err != nil {
			return nil, fault.Wrap(fault.CodeScopeViolation, "read delegated change", err, "unsupported Git tree entry")
		}
		if from == nil && to == nil {
			return nil, fault.New(fault.CodeScopeViolation, "delegated candidate contains unsupported tree entry")
		}
		name := change.To.Name
		if to == nil {
			name = change.From.Name
		}
		if !safeExportPath(name) {
			return nil, fault.New(fault.CodeScopeViolation, "delegated candidate contains unsafe path")
		}
		if from != nil && to != nil && change.From.Name != change.To.Name {
			if !safeExportPath(change.From.Name) {
				return nil, fault.New(fault.CodeScopeViolation, "delegated candidate contains unsafe old path")
			}
			result = append(result, ExportedChange{Repository: repoID, Path: change.From.Name, Operation: ChangeDelete})
		}
		entry := ExportedChange{Repository: repoID, Path: name}
		if to == nil {
			entry.Operation = ChangeDelete
			result = append(result, entry)
			continue
		}
		if to.Mode == filemode.Submodule || to.Size > maxDelegatedFile {
			return nil, fault.New(fault.CodeScopeViolation, "delegated candidate contains unsupported file")
		}
		total += to.Size
		if total > maxDelegatedChanges {
			return nil, fault.New(fault.CodeScopeViolation, "delegated change set exceeds handoff limit")
		}
		entry.Operation = ChangeWrite
		entry.Mode = fmt.Sprintf("%06o", uint32(to.Mode))
		target := filepath.Join(destination, "repositories", repoID, name)
		if err := makeReadablePath(destination, filepath.Dir(target)); err != nil {
			return nil, err
		}
		reader, err := to.Reader()
		if err != nil {
			return nil, err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, errors.Join(err, reader.Close())
		}
		_, copyErr := io.CopyN(file, reader, to.Size)
		closeErr := file.Close()
		readerCloseErr := reader.Close()
		if err := errors.Join(copyErr, closeErr, readerCloseErr); err != nil {
			return nil, fault.Wrap(fault.CodeGitError, "export delegated blob", err, "cannot export delegated blob")
		}
		if err := os.Chmod(target, 0o644); err != nil {
			return nil, err
		}
		entry.SHA256, err = contracts.FileDigest(target)
		if err != nil {
			return nil, err
		}
		entry.Source = "/turnyard-control/delegations/" + filepath.Base(destination) + "/repositories/" + repoID + "/" + filepath.ToSlash(name)
		result = append(result, entry)
	}
	return result, nil
}

func safeExportPath(name string) bool {
	return filepath.IsLocal(name) && filepath.Clean(name) == name && !strings.Contains(name, "\\") && !strings.HasPrefix(name, ".git/") && name != ".git"
}

func makeReadablePath(root, directory string) error {
	rel, err := filepath.Rel(root, directory)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fault.New(fault.CodeScopeViolation, "handoff path escapes root")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := os.MkdirAll(current, 0o755); err != nil {
			return err
		}
		if err := os.Chmod(current, 0o755); err != nil {
			return err
		}
	}
	return nil
}
