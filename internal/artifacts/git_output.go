package artifacts

import (
	"context"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/go-git/go-git/v5/plumbing"
	"io"
	"net/http"
	"path/filepath"
)

func inspectGitOutput(ctx context.Context, workspace string, vector map[string]gitstate.RepoVersion, spec contracts.DeliverableSpec) (Result, error) {
	items, err := gitstate.InspectDeliverables(ctx, workspace, vector, []contracts.DeliverableSpec{spec})
	if err != nil {
		return Result{}, err
	}
	item := items[0]
	result := Result{ID: item.ID, Kind: item.Kind, Status: item.Status, Verification: VerificationGitTree,
		Repository: item.Repository, Path: item.Path, Commit: item.Commit, SHA256: item.SHA256,
		ExpectedSHA256: item.ExpectedSHA256, Bytes: item.Bytes, ErrorCode: item.ErrorCode, MediaType: spec.MediaType}
	if spec.Kind != contracts.DeliverableImage || result.Status != StatusPresent {
		return result, nil
	}
	mediaType, err := imageTypeAtCommit(workspace, spec.Repository, item.Commit, spec.Path)
	if err != nil {
		return Result{}, err
	}
	result.MediaType = mediaType
	result.Verification = VerificationGitTreeImageSignature
	if mediaType == "" || spec.MediaType != "" && mediaType != spec.MediaType {
		result.Status, result.ErrorCode = StatusInvalid, string(fault.CodeDeliverableInvalid)
	}
	return result, nil
}

func imageTypeAtCommit(workspace, repository, commitHash, path string) (string, error) {
	repo, _, err := gitstate.OpenRepo(filepath.Join(workspace, repository))
	if err != nil {
		return "", err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return "", err
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", err
	}
	file, err := tree.File(path)
	if err != nil {
		return "", err
	}
	reader, err := file.Reader()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	var header [512]byte
	n, err := io.ReadFull(reader, header[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	data := header[:n]
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp", nil
	}
	switch http.DetectContentType(data) {
	case "image/png", "image/jpeg", "image/gif":
		return http.DetectContentType(data), nil
	}
	return "", nil
}
