package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/gitstate"
	"github.com/go-git/go-git/v5/plumbing"
)

const maxOutputBytes = 512 << 20

// SealedPaths reconstructs candidate-owned output paths without trusting a
// path supplied by an agent or a previously serialized result.
func SealedPaths(sessionRoot, candidateID string, specs []contracts.DeliverableSpec) map[string]string {
	paths := map[string]string{}
	for _, spec := range specs {
		if spec.Kind != contracts.DeliverableFile && spec.Kind != contracts.DeliverableImage {
			continue
		}
		if spec.Repository == "" || spec.Destination != nil && spec.Destination.Kind == contracts.DestinationConnector {
			paths[spec.ID] = filepath.Join(sessionRoot, "outputs", candidateID, spec.ID)
		}
	}
	return paths
}

// RebaseLocalPaths keeps result locations usable after state restore moves a
// session to another root. Paths are derived from trusted IDs, never claims.
func RebaseLocalPaths(results []Result, sessionRoot, candidateID string) {
	for i := range results {
		if results[i].LocalPath != "" {
			results[i].LocalPath = filepath.Join(sessionRoot, "outputs", candidateID, results[i].ID)
		}
	}
}

// SealOutputs copies invocation files or candidate Git blobs into a directory
// that no later agent invocation mounts. Missing files remain missing outputs.
func SealOutputs(ctx context.Context, invocationDir, workspace, sessionRoot, candidateID string, vector map[string]gitstate.RepoVersion, specs []contracts.DeliverableSpec) (map[string]string, error) {
	paths := SealedPaths(sessionRoot, candidateID, specs)
	if len(paths) == 0 {
		return paths, nil
	}
	dir := filepath.Join(sessionRoot, "outputs", candidateID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	for _, spec := range specs {
		target, needed := paths[spec.ID]
		if !needed {
			continue
		}
		var source io.ReadCloser
		if spec.Repository == "" {
			root, err := os.OpenRoot(invocationDir)
			if err == nil {
				name := filepath.ToSlash(filepath.Join("files", spec.Path))
				info, statErr := root.Lstat(name)
				if statErr == nil && info.Mode().IsRegular() && info.Size() <= maxOutputBytes {
					source, err = root.Open(name)
				} else {
					err = fault.New(fault.CodeDeliverableInvalid, "output %s is missing or not regular", spec.ID)
				}
				_ = root.Close()
			}
			if err != nil {
				continue
			}
		} else {
			var err error
			source, err = gitBlobReader(workspace, vector[spec.Repository], spec.Repository, spec.Path)
			if err != nil {
				continue
			}
		}
		tmp, err := os.CreateTemp(dir, ".output-*")
		if err != nil {
			return nil, fault.At(errors.Join(err, source.Close()), "create output staging file")
		}
		n, copyErr := io.Copy(tmp, io.LimitReader(source, maxOutputBytes+1))
		var syncErr error
		if copyErr == nil && n <= maxOutputBytes && ctx.Err() == nil {
			syncErr = tmp.Sync()
		}
		closeErr := errors.Join(source.Close(), tmp.Close())
		if copyErr != nil {
			copyErr = fault.Wrap(fault.CodeDeliverableInvalid, "seal output bytes", errors.Join(copyErr, closeErr), "output %s copy failed", spec.ID)
		} else if n > maxOutputBytes || ctx.Err() != nil {
			copyErr = fault.New(fault.CodeDeliverableInvalid, "output %s exceeds size limit or copy was cancelled", spec.ID)
		} else if err := errors.Join(syncErr, closeErr); err != nil {
			copyErr = fault.Wrap(fault.CodeDeliverableInvalid, "seal output file", err, "output %s could not be sealed", spec.ID)
		}
		if copyErr != nil {
			return nil, errors.Join(copyErr, os.Remove(tmp.Name()))
		}
		if err := os.Rename(tmp.Name(), target); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func gitBlobReader(workspace string, version gitstate.RepoVersion, repository, path string) (io.ReadCloser, error) {
	repo, _, err := gitstate.OpenRepo(filepath.Join(workspace, repository))
	if err != nil {
		return nil, err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(version.Head))
	if err != nil || commit.TreeHash.String() != version.Tree {
		return nil, fault.New(fault.CodeCandidateCorrupt, "candidate Git tree is unavailable")
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}
	file, err := tree.File(path)
	if err != nil || !file.Mode.IsRegular() {
		return nil, fault.New(fault.CodeDeliverableInvalid, "candidate output is missing or not regular")
	}
	return file.Reader()
}

func inspectLocalOutput(ctx context.Context, spec contracts.DeliverableSpec, path string) (Result, error) {
	result := Result{ID: spec.ID, Kind: spec.Kind, Path: spec.Path, LocalPath: path,
		ExpectedSHA256: spec.ExpectedSHA256, MediaType: spec.MediaType, Verification: VerificationSealedFileSHA256,
		Status: StatusMissing, ErrorCode: string(fault.CodeDeliverableMissing)}
	if path == "" {
		return result, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxOutputBytes {
		result.Status, result.ErrorCode = StatusInvalid, string(fault.CodeDeliverableInvalid)
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()
	hash := sha256.New()
	var header [512]byte
	n, err := io.ReadFull(file, header[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Result{}, err
	}
	if _, err := hash.Write(header[:n]); err != nil {
		return Result{}, err
	}
	remaining, err := io.Copy(hash, io.LimitReader(file, maxOutputBytes+1))
	if err != nil || ctx.Err() != nil {
		return Result{}, fault.New(fault.CodeDeliverableInvalid, "output read failed")
	}
	result.Bytes = int64(n) + remaining
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	result.Status, result.ErrorCode = StatusPresent, ""
	if spec.ExpectedSHA256 != "" && spec.ExpectedSHA256 != result.SHA256 {
		result.Status, result.ErrorCode = StatusMismatch, string(fault.CodeDeliverableMismatch)
	}
	if spec.Kind == contracts.DeliverableImage {
		media := detectedImageType(header[:n])
		result.MediaType = media
		if media == "" || spec.MediaType != "" && media != spec.MediaType {
			result.Status, result.ErrorCode = StatusInvalid, string(fault.CodeDeliverableInvalid)
		}
	}
	return result, nil
}

func detectedImageType(data []byte) string {
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	switch media := http.DetectContentType(data); media {
	case "image/png", "image/jpeg", "image/gif":
		return media
	}
	return ""
}
