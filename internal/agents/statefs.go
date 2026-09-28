package agents

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/fault"
)

// The container can write /state. Before the host touches an agent-controlled
// path on a later turn, reject symlinks at every path component. The supervisor
// runs one invocation per session, so the agent cannot race these host checks.
func EnsureStateDirectory(root, directory string) error {
	root = filepath.Clean(root)
	directory = filepath.Clean(directory)
	rel, err := filepath.Rel(root, directory)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fault.New(fault.CodeNativeStateUnsafe, "state path escapes its root")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	check := func(path string) error {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fault.New(fault.CodeNativeStateUnsafe, "state directory is not a real directory: %s", path)
		}
		return nil
	}
	if err := check(root); err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := check(current); err != nil {
			return err
		}
	}
	return nil
}

func CheckStateFile(root, path string) error {
	if err := EnsureStateDirectory(root, filepath.Dir(path)); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fault.New(fault.CodeNativeStateUnsafe, "state file is not regular: %s", path)
	}
	return nil
}

func ReadStateFile(root, path string) ([]byte, error) {
	if err := CheckStateFile(root, path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func writeStateFile(root, path string, body []byte) error {
	if err := CheckStateFile(root, path); err != nil {
		return err
	}
	// Rename replaces an existing file or symlink instead of following it.
	temp, err := os.CreateTemp(filepath.Dir(path), ".turnyard-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		return errors.Join(err, temp.Close())
	}
	if _, err := temp.Write(body); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func WritePinnedConfiguration(root, path string, body []byte, label string) error {
	prior, err := ReadStateFile(root, path)
	if err == nil {
		if !bytes.Equal(prior, body) {
			return fault.New(fault.CodeEnvironmentDrift, "%s changed within this session", label)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeStateFile(root, path, body)
}
