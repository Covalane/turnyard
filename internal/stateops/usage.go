package stateops

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
)

// Usage counts bytes in regular files under one state root. WalkDir does not
// follow symlinks, so a linked directory outside the state root is excluded.
func Usage(ctx context.Context, root string) (int64, error) {
	var bytes int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			bytes += info.Size()
		}
		return nil
	})
	return bytes, err
}
