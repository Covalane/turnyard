package contracts

import (
	"os"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/fault"
)

func BundleDigest(root string) (string, error) { return digestBundle(root, false) }
func bundleDigest(root string) (string, error) { return BundleDigest(root) }

func InstalledBundleDigest(root string) (string, error) { return digestBundle(root, true) }

func digestBundle(root string, skipMarker bool) (string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, item os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.Type()&os.ModeSymlink != 0 {
			return fault.New(fault.CodeInvalidSpec, "bundle contains symlink: %s", path)
		}
		if item.IsDir() {
			return nil
		}
		if !item.Type().IsRegular() {
			return fault.New(fault.CodeNativeStateUnsafe, "bundle member is not a regular file: %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if skipMarker && relative == ".turnyard-digest" {
			return nil
		}
		hash, err := FileDigest(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = hash
		return nil
	})
	if err != nil {
		return "", err
	}
	return Digest(files)
}
