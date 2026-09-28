package gitstate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

const (
	checkpointWorkspaceRoot = "workspace"
	// AgentStateDirName is both the per-session state directory and its checkpoint root.
	AgentStateDirName = "agent-state"
	// Old tar archives can mark regular files with a zero type flag.
	legacyRegularTypeFlag byte = 0
)

func checkpointFileAllowed(name string) bool {
	lower := strings.ToLower(filepath.Base(name))
	if strings.HasPrefix(lower, ".env") {
		return false
	}
	for _, word := range []string{"auth", "credential", "token"} {
		if strings.Contains(lower, word) {
			return false
		}
	}
	return true
}

func CheckpointArchive(ctx context.Context, workspace, state, destination string) (digest string, archiveErr error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), "checkpoint-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer func() {
		if err := os.Remove(temp.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			archiveErr = fault.At(errors.Join(archiveErr, err), "remove incomplete checkpoint archive")
		}
	}()
	gz := gzip.NewWriter(temp)
	tw := tar.NewWriter(gz)
	add := func(root, prefix string, filter bool) error {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			return nil
		}
		return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if canceled := ctx.Err(); canceled != nil {
				return canceled
			}
			if err != nil {
				return err
			}
			if filter && !checkpointFileAllowed(path) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(filepath.Join(prefix, rel))
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(path)
				if err != nil {
					return err
				}
			}
			h, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			h.Name = name
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, contextReader{ctx, f})
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		})
	}
	if err := add(workspace, checkpointWorkspaceRoot, false); err != nil {
		return "", errors.Join(err, tw.Close(), gz.Close(), temp.Close())
	}
	if err := add(state, AgentStateDirName, true); err != nil {
		return "", errors.Join(err, tw.Close(), gz.Close(), temp.Close())
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temp.Name(), destination); err != nil {
		return "", err
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return "", err
	}
	return contracts.FileDigest(destination)
}

func RestoreArchive(ctx context.Context, archive, expected, workspace, state string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hash, err := contracts.FileDigest(archive)
	if err != nil {
		return err
	}
	if hash != expected {
		return fault.New(fault.CodeCheckpointCorrupt, "checkpoint SHA-256 mismatch")
	}
	if _, err := os.Lstat(workspace); err == nil {
		return fault.New(fault.CodeRestoreConflict, "workspace already exists")
	}
	if _, err := os.Lstat(state); err == nil {
		return fault.New(fault.CodeRestoreConflict, "agent state already exists")
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	root := filepath.Dir(workspace)
	stage, err := os.MkdirTemp(root, "restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fault.Wrap(fault.CodeCheckpointCorrupt, "read checkpoint tar", err, "archive entry is corrupt")
		}
		clean := filepath.Clean(h.Name)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return fault.New(fault.CodeCheckpointCorrupt, "unsafe archive path %s", h.Name)
		}
		top := strings.Split(clean, string(os.PathSeparator))[0]
		if top != checkpointWorkspaceRoot && top != AgentStateDirName {
			return fault.New(fault.CodeCheckpointCorrupt, "unexpected archive root %s", top)
		}
		dst := filepath.Join(stage, clean)
		if !strings.HasPrefix(dst, stage+string(os.PathSeparator)) {
			return fault.New(fault.CodeCheckpointCorrupt, "unsafe path")
		}
		// Existing parent components must never redirect extraction through a symlink.
		for parent := filepath.Dir(dst); parent != stage && parent != "."; parent = filepath.Dir(parent) {
			if info, err := os.Lstat(parent); err == nil && info.Mode()&os.ModeSymlink != 0 {
				return fault.New(fault.CodeCheckpointCorrupt, "archive parent is a symlink: %s", parent)
			}
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) {
				return fault.New(fault.CodeCheckpointCorrupt, "absolute archive symlink")
			}
			base := filepath.Join(stage, top)
			target := filepath.Clean(filepath.Join(filepath.Dir(dst), h.Linkname))
			if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
				return fault.New(fault.CodeCheckpointCorrupt, "archive symlink escapes %s", top)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, dst); err != nil {
				return err
			}
		case tar.TypeLink:
			return fault.New(fault.CodeCheckpointCorrupt, "archive hardlinks are unsupported")
		case tar.TypeReg, legacyRegularTypeFlag:
			if h.Size > 2<<30 {
				return fault.New(fault.CodeCheckpointCorrupt, "oversized archive member")
			}
			total += h.Size
			if total > 8<<30 {
				return fault.New(fault.CodeCheckpointCorrupt, "archive exceeds restored data limit")
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(h.Mode)&0o700)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, contextReader{ctx, tr}, h.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fault.New(fault.CodeCheckpointCorrupt, "unsupported archive member %s", h.Name)
		}
	}
	for _, name := range []string{checkpointWorkspaceRoot, AgentStateDirName} {
		info, err := os.Lstat(filepath.Join(stage, name))
		if err != nil || !info.IsDir() {
			return fault.New(fault.CodeCheckpointCorrupt, "archive lacks %s directory", name)
		}
	}
	if err := os.Rename(filepath.Join(stage, AgentStateDirName), state); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(stage, checkpointWorkspaceRoot), workspace); err != nil {
		return errors.Join(err, os.RemoveAll(state))
	}
	return nil
}
