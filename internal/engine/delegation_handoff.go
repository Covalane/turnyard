package engine

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/artifacts"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/Covalane/turnyard/internal/store"
)

type delegatedFile struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}

type delegationHandoff struct {
	CandidateID     string                    `json:"candidateId"`
	CandidateDigest string                    `json:"candidateDigest"`
	Changes         []gitstate.ExportedChange `json:"changes"`
	Files           []delegatedFile           `json:"files"`
}

func (d *delegationServer) exportHandoff(ctx context.Context, child store.SessionRow, candidate *CandidateResult) (delegationHandoff, error) {
	d.handoffMu.Lock()
	defer d.handoffMu.Unlock()
	if cached, ok := d.handoffs[child.ID]; ok && cached.CandidateDigest == candidate.Digest {
		return cached, nil
	}
	result := delegationHandoff{CandidateID: candidate.ID, CandidateDigest: candidate.Digest,
		Changes: []gitstate.ExportedChange{}, Files: []delegatedFile{}}
	root := filepath.Join(d.controlDir, "delegations", child.ID)
	if err := os.RemoveAll(root); err != nil {
		return result, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return result, err
	}
	if err := os.Chmod(filepath.Dir(root), 0o755); err != nil {
		return result, err
	}
	if err := os.Chmod(root, 0o755); err != nil {
		return result, err
	}
	for repoID, version := range candidate.Vector {
		if version.Base == version.Head {
			continue
		}
		changes, err := gitstate.ExportChanges(ctx, filepath.Join(child.Workspace, repoID), repoID,
			version.Base, version.Head, root)
		if err != nil {
			return result, err
		}
		result.Changes = append(result.Changes, changes...)
	}
	for _, output := range candidate.Deliverables {
		if output.Status != artifacts.StatusPresent || output.LocalPath == "" {
			continue
		}
		info, err := os.Lstat(output.LocalPath)
		if os.IsNotExist(err) {
			return result, fault.New(fault.CodeDeliverableMismatch, "delegated file %s is missing", output.ID)
		}
		if err != nil {
			return result, err
		}
		if !info.Mode().IsRegular() || info.Size() > maxDelegatedOutput {
			return result, fault.New(fault.CodeDeliverableMismatch, "delegated file %s is unavailable", output.ID)
		}
		sha, err := contracts.FileDigest(output.LocalPath)
		if err != nil {
			return result, err
		}
		if sha != output.SHA256 {
			return result, fault.New(fault.CodeDeliverableMismatch, "delegated file %s changed", output.ID)
		}
		target := filepath.Join(root, "artifacts", output.ID)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return result, err
		}
		if err := os.Chmod(filepath.Dir(target), 0o755); err != nil {
			return result, err
		}
		if err := copyHandoffFile(output.LocalPath, target, sha); err != nil {
			return result, err
		}
		result.Files = append(result.Files, delegatedFile{ID: output.ID, Source: "/turnyard-control/delegations/" + child.ID + "/artifacts/" + output.ID, SHA256: sha})
	}
	d.handoffs[child.ID] = result
	return result, nil
}

func copyHandoffFile(source, target, expectedSHA string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(0o644))
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(in, maxDelegatedOutput+1))
	closeErr := out.Close()
	if err != nil || n > maxDelegatedOutput {
		removeErr := os.Remove(target)
		if err != nil || closeErr != nil || removeErr != nil {
			return fault.Wrap(fault.CodeDeliverableMismatch, "copy delegated file", errors.Join(err, closeErr, removeErr), "delegated file copy failed")
		}
		return fault.New(fault.CodeDeliverableMismatch, "delegated file exceeded handoff limit")
	}
	if closeErr != nil {
		return fault.Wrap(fault.CodeDeliverableMismatch, "close delegated file", closeErr, "delegated file could not be closed")
	}
	actual, err := contracts.FileDigest(target)
	if err != nil {
		return errors.Join(err, os.Remove(target))
	}
	if actual != expectedSHA {
		return errors.Join(fault.New(fault.CodeDeliverableMismatch, "delegated file changed during handoff"), os.Remove(target))
	}
	return os.Chmod(target, 0o644)
}
