// Package inputs snapshots task attachments before the task is accepted.
package inputs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/artifactio"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/observe"
)

const maxInputBytes = 256 << 20

// Stage replaces source URLs and local paths with content-pinned, session-local
// paths. The original signed URL is not stored in the task specification.
func BatchPath(sessionRoot string, work contracts.WorkSpec) (string, error) {
	batch, err := contracts.Digest(work)
	if err != nil {
		return "", err
	}
	return filepath.Join(sessionRoot, "inputs", batch), nil
}

func Stage(ctx context.Context, work contracts.WorkSpec, workPath, sessionRoot string, env contracts.EnvironmentSpec) (staged contracts.WorkSpec, stageErr error) {
	if len(work.Inputs) == 0 {
		return work, nil
	}
	batch, err := contracts.Digest(work)
	if err != nil {
		return work, err
	}
	root := filepath.Join(sessionRoot, "inputs", batch)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return work, err
	}
	defer func() {
		if stageErr != nil {
			stageErr = errors.Join(stageErr, os.RemoveAll(root))
		}
	}()
	for i := range work.Inputs {
		input := &work.Inputs[i]
		target := filepath.Join(root, input.ID)
		tmp, err := os.CreateTemp(root, ".input-*")
		if err != nil {
			return work, err
		}
		tempPath := tmp.Name()
		if err := tmp.Close(); err != nil {
			return work, fault.Wrap(fault.CodeInvalidSpec, "close input staging file", errors.Join(err, os.Remove(tempPath)), "input %s cannot be staged", input.ID)
		}
		defer os.Remove(tempPath)
		switch input.Source.Kind {
		case contracts.InputFile:
			path := input.Source.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(filepath.Dir(workPath), path)
			}
			err = copyRegular(ctx, path, tempPath)
		case contracts.InputHTTPS:
			err = downloadHTTPS(ctx, input.Source.URL, env.InputHosts, tempPath)
		case contracts.InputConnector:
			var connector artifactio.Connector
			connector, err = artifactio.Find(env, input.Source.Connector)
			if err == nil {
				err = connector.Get(ctx, input.Source.URI, tempPath, maxInputBytes)
			}
		}
		if err != nil {
			return work, err
		}
		info, err := os.Lstat(tempPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxInputBytes {
			return work, fault.New(fault.CodeInvalidSpec, "input %s is not a regular file within the size limit", input.ID)
		}
		digest, err := contracts.FileDigest(tempPath)
		if err != nil {
			return work, err
		}
		if input.ExpectedSHA256 != "" && digest != input.ExpectedSHA256 {
			return work, fault.New(fault.CodeDeliverableMismatch, "input %s SHA-256 mismatch", input.ID)
		}
		if err := os.Link(tempPath, target); err != nil {
			if !os.IsExist(err) {
				return work, err
			}
			info, statErr := os.Lstat(target)
			if statErr != nil || !info.Mode().IsRegular() {
				return work, fault.New(fault.CodeIdempotencyConflict, "input %s snapshot path is invalid", input.ID)
			}
			existing, digestErr := contracts.FileDigest(target)
			if digestErr != nil || existing != digest {
				return work, fault.New(fault.CodeIdempotencyConflict, "input %s snapshot already contains different bytes", input.ID)
			}
		}
		input.ExpectedSHA256 = digest
		input.Source = contracts.InputSource{Kind: contracts.InputStaged, Path: filepath.ToSlash(filepath.Join(batch, input.ID))}
	}
	return work, nil
}

func Verify(ctx context.Context, sessionRoot string, work contracts.WorkSpec) error {
	root, err := os.OpenRoot(filepath.Join(sessionRoot, "inputs"))
	if err != nil && len(work.Inputs) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	for _, input := range work.Inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if input.Source.Kind != contracts.InputStaged {
			return fault.New(fault.CodeCandidateCorrupt, "input %s was not staged", input.ID)
		}
		entry, err := root.Lstat(input.Source.Path)
		if err != nil {
			return fault.Wrap(fault.CodeCandidateCorrupt, "stat staged input", err, "input %s snapshot cannot be inspected", input.ID)
		}
		if !entry.Mode().IsRegular() || entry.Size() > maxInputBytes {
			return fault.New(fault.CodeCandidateCorrupt, "input %s snapshot is invalid", input.ID)
		}
		file, err := root.Open(input.Source.Path)
		if err != nil {
			return fault.Wrap(fault.CodeCandidateCorrupt, "open staged input", err, "input %s snapshot cannot be opened", input.ID)
		}
		info, err := file.Stat()
		if err != nil {
			return fault.Wrap(fault.CodeCandidateCorrupt, "stat opened input", errors.Join(err, file.Close()), "input %s snapshot cannot be inspected", input.ID)
		}
		if !info.Mode().IsRegular() || info.Size() > maxInputBytes {
			if err := file.Close(); err != nil {
				return fault.Wrap(fault.CodeCandidateCorrupt, "close invalid staged input", err, "input %s snapshot is invalid", input.ID)
			}
			return fault.New(fault.CodeCandidateCorrupt, "input %s snapshot is invalid", input.ID)
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(file, maxInputBytes+1))
		if err := errors.Join(err, file.Close()); err != nil {
			return fault.Wrap(fault.CodeCandidateCorrupt, "read staged input", err, "input %s snapshot cannot be verified", input.ID)
		}
		if n > maxInputBytes || hex.EncodeToString(h.Sum(nil)) != input.ExpectedSHA256 {
			return fault.New(fault.CodeCandidateCorrupt, "input %s snapshot changed", input.ID)
		}
	}
	return nil
}

func copyRegular(ctx context.Context, path, destination string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fault.Wrap(fault.CodeInvalidSpec, "stat local input", err, "input file is unavailable")
	}
	if !info.Mode().IsRegular() || info.Size() > maxInputBytes {
		return fault.New(fault.CodeInvalidSpec, "input file is unavailable or invalid")
	}
	from, err := os.Open(path)
	if err != nil {
		return err
	}
	defer from.Close()
	to, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(to, io.LimitReader(from, maxInputBytes+1))
	closeErr := to.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fault.Wrap(fault.CodeInvalidSpec, "copy local input", err, "input file copy failed")
	}
	if n > maxInputBytes || ctx.Err() != nil {
		return fault.New(fault.CodeInvalidSpec, "input file exceeds size limit or copy was cancelled")
	}
	return nil
}

func downloadHTTPS(ctx context.Context, raw string, allowed []string, destination string) error {
	return downloadHTTPSWithTransport(ctx, raw, allowed, destination, http.DefaultTransport)
}

func downloadHTTPSWithTransport(ctx context.Context, raw string, allowed []string, destination string, transport http.RoundTripper) error {
	allow := func(raw string) bool {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.User != nil {
			return false
		}
		for _, host := range allowed {
			if strings.EqualFold(host, u.Hostname()) {
				return true
			}
		}
		return false
	}
	if !allow(raw) {
		return fault.New(fault.CodeInvalidSpec, "input HTTPS host is not allowed")
	}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if !allow(req.URL.String()) {
			return fault.New(fault.CodeInvalidSpec, "input HTTPS redirect host is not allowed")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		// net/http errors can include the complete signed URL. Keep it out of
		// CLI errors and the operational log.
		observe.LogFailure(ctx, "input download failed", fault.Wrap(fault.CodeInvalidSpec, "download input", err, "input download failed"))
		return fault.New(fault.CodeInvalidSpec, "input download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fault.New(fault.CodeInvalidSpec, "input download returned HTTP %d", resp.StatusCode)
	}
	to, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(to, io.LimitReader(resp.Body, maxInputBytes+1))
	closeErr := to.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		observe.LogFailure(ctx, "input download copy failed", fault.Wrap(fault.CodeInvalidSpec, "copy downloaded input", err, "input download failed"))
		return fault.New(fault.CodeInvalidSpec, "input download failed")
	}
	if n > maxInputBytes {
		return fault.New(fault.CodeInvalidSpec, "input download exceeds size limit or failed")
	}
	return nil
}
