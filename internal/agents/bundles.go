package agents

import (
	"os"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func copyBundle(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fault.New(fault.CodeInvalidSpec, "bundle symlink %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info.Mode()&0o111 != 0 {
			mode = 0o700
		}
		return os.WriteFile(target, body, mode)
	})
}
func InstallPinnedBundle(state, source, expected, dest, label string) error {
	actual, err := contracts.BundleDigest(source)
	if err != nil {
		return err
	}
	if actual != expected {
		return fault.New(fault.CodeEnvironmentDrift, "%s changed", label)
	}
	if _, err := os.Lstat(filepath.Join(source, ".turnyard-digest")); err == nil {
		return fault.New(fault.CodeInvalidSpec, "%s uses a reserved bundle filename", label)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := EnsureStateDirectory(state, dest); err != nil {
		return err
	}
	marker := filepath.Join(dest, ".turnyard-digest")
	prior, err := ReadStateFile(state, marker)
	if err == nil {
		installed, err := contracts.InstalledBundleDigest(dest)
		if err != nil {
			return err
		}
		if string(prior) != actual || installed != actual {
			return fault.New(fault.CodeEnvironmentDrift, "installed %s changed", label)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fault.New(fault.CodeEnvironmentDrift, "installed %s lacks its manifest", label)
	}
	if err := copyBundle(source, dest); err != nil {
		return err
	}
	return writeStateFile(state, marker, []byte(actual))
}
func SelectedSkill(env contracts.EnvironmentSpec, id string) (*contracts.BundleSpec, error) {
	for i := range env.Skills {
		if env.Skills[i].ID == id {
			return &env.Skills[i], nil
		}
	}
	return nil, fault.New(fault.CodeInvalidSpec, "skill %s unavailable", id)
}
func selectedTool(env contracts.EnvironmentSpec, id string) (*contracts.ToolSpec, error) {
	for i := range env.Tools {
		if env.Tools[i].ID == id {
			return &env.Tools[i], nil
		}
	}
	return nil, fault.New(fault.CodeInvalidSpec, "tool %s unavailable", id)
}
