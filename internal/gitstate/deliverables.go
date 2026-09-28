package gitstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// DeliverableResult describes bytes in the recorded Git commit, not an
// uncommitted file or an agent's claim in its final message.
type DeliverableResult struct {
	ID             string                      `json:"id"`
	Repository     string                      `json:"repository"`
	Path           string                      `json:"path"`
	Kind           contracts.DeliverableKind   `json:"kind"`
	Commit         string                      `json:"commit"`
	Status         contracts.DeliverableStatus `json:"status"`
	SHA256         string                      `json:"sha256,omitempty"`
	ExpectedSHA256 string                      `json:"expectedSha256,omitempty"`
	Bytes          int64                       `json:"bytes"`
	ErrorCode      string                      `json:"errorCode,omitempty"`
}

const (
	DeliverablePresent  = contracts.DeliverableStatusPresent
	DeliverableMissing  = contracts.DeliverableStatusMissing
	DeliverableInvalid  = contracts.DeliverableStatusInvalid
	DeliverableMismatch = contracts.DeliverableStatusMismatch
)

// InspectDeliverables reads each declared output from the candidate's Git tree.
func InspectDeliverables(ctx context.Context, workspace string, vector map[string]RepoVersion, specs []contracts.DeliverableSpec) ([]DeliverableResult, error) {
	results := make([]DeliverableResult, 0, len(specs))
	trees := make(map[string]*object.Tree, len(vector))
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		version, ok := vector[spec.Repository]
		if !ok {
			return nil, fault.New(fault.CodeCandidateCorrupt, "deliverable %s repository is missing from candidate", spec.ID)
		}
		result := DeliverableResult{ID: spec.ID, Repository: spec.Repository, Path: spec.Path, Kind: spec.Kind,
			Commit: version.Head, ExpectedSHA256: spec.ExpectedSHA256}
		tree := trees[spec.Repository]
		if tree == nil {
			repo, _, err := OpenRepo(filepath.Join(workspace, spec.Repository))
			if err != nil {
				return nil, err
			}
			commit, err := repo.CommitObject(plumbing.NewHash(version.Head))
			if err != nil || commit.TreeHash.String() != version.Tree {
				return nil, fault.New(fault.CodeCandidateCorrupt, "deliverable %s candidate commit is unavailable or changed", spec.ID)
			}
			tree, err = commit.Tree()
			if err != nil {
				return nil, err
			}
			trees[spec.Repository] = tree
		}
		file, err := tree.File(spec.Path)
		if errors.Is(err, object.ErrFileNotFound) {
			result.Status, result.ErrorCode = DeliverableMissing, string(fault.CodeDeliverableMissing)
			results = append(results, result)
			continue
		}
		if err != nil {
			return nil, err
		}
		if !file.Mode.IsRegular() {
			result.Status, result.ErrorCode = DeliverableInvalid, string(fault.CodeDeliverableInvalid)
			results = append(results, result)
			continue
		}
		reader, err := file.Reader()
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		result.Bytes, err = io.Copy(hash, contextReader{ctx: ctx, reader: reader})
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		result.SHA256 = hex.EncodeToString(hash.Sum(nil))
		result.Status = DeliverablePresent
		if spec.ExpectedSHA256 != "" && result.SHA256 != spec.ExpectedSHA256 {
			result.Status, result.ErrorCode = DeliverableMismatch, string(fault.CodeDeliverableMismatch)
		}
		results = append(results, result)
	}
	return results, nil
}
