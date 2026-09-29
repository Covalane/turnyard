// Package stateops owns offline state backups and terminal-session retention.
package stateops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Covalane/turnyard/internal/collections"
	"github.com/Covalane/turnyard/internal/store"
)

const (
	manifestName = "turnyard-backup.json"
	lockName     = "daemon.lock"
	backupFormat = "turnyard-state-backup/v1"
)

// EntryKind is the filesystem object recorded in a backup manifest.
type EntryKind string

const (
	EntryDirectory EntryKind = "directory"
	EntrySymlink   EntryKind = "symlink"
	EntryFile      EntryKind = "file"
)

type Entry struct {
	Path   string    `json:"path"`
	Kind   EntryKind `json:"kind"`
	Mode   uint32    `json:"mode,omitempty"`
	Size   int64     `json:"size,omitempty"`
	SHA256 string    `json:"sha256,omitempty"`
	Target string    `json:"target,omitempty"`
}

type Manifest struct {
	Format    string  `json:"format"`
	Source    string  `json:"source"`
	CreatedAt string  `json:"created_at"`
	Entries   []Entry `json:"entries"`
}

// Lock excludes the running supervisor while backup and prune operate.
type Lock struct{ file *os.File }

func Acquire(home string) (*Lock, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(home, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("state is in use; stop the supervisor first: %w", errors.Join(err, file.Close()))
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return errors.Join(syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN), l.file.Close())
}

// Backup copies a quiescent home to an atomic, checksummed directory.
func Backup(ctx context.Context, home, destination string) (Manifest, error) {
	home, destination, err := backupPaths(home, destination)
	if err != nil {
		return Manifest{}, err
	}
	if _, err := os.Stat(filepath.Join(home, store.DatabaseFileName)); err != nil {
		return Manifest{}, fmt.Errorf("state database is unavailable: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return Manifest{}, fmt.Errorf("backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Manifest{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".turnyard-backup-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(stage)
	manifest := Manifest{Format: backupFormat, Source: home, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	err = filepath.WalkDir(home, func(path string, item fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(home, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == lockName {
			return nil
		}
		if rel == manifestName {
			return fmt.Errorf("state contains reserved backup manifest name")
		}
		entry, err := copyEntry(ctx, path, filepath.Join(stage, rel), filepath.ToSlash(rel), item)
		if err != nil {
			return err
		}
		manifest.Entries = append(manifest.Entries, entry)
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, manifestName), append(body, '\n'), 0o600); err != nil {
		return Manifest{}, err
	}
	if _, err := Verify(ctx, stage); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(stage, destination); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func backupPaths(home, destination string) (string, string, error) {
	home, err := filepath.Abs(home)
	if err != nil {
		return "", "", err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(home, destination)
	if err != nil {
		return "", "", err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", "", fmt.Errorf("backup must be outside the state directory")
	}
	return home, destination, nil
}

func copyEntry(ctx context.Context, source, destination, name string, item fs.DirEntry) (Entry, error) {
	info, err := item.Info()
	if err != nil {
		return Entry{}, err
	}
	switch {
	case item.IsDir():
		if err := os.Mkdir(destination, 0o700); err != nil {
			return Entry{}, err
		}
		return Entry{Path: name, Kind: EntryDirectory}, nil
	case item.Type()&os.ModeSymlink != 0:
		target, err := os.Readlink(source)
		if err != nil {
			return Entry{}, err
		}
		if !safeLink(name, target) {
			return Entry{}, fmt.Errorf("unsafe state symlink: %s", name)
		}
		if err := os.Symlink(target, destination); err != nil {
			return Entry{}, err
		}
		return Entry{Path: name, Kind: EntrySymlink, Target: target}, nil
	case item.Type().IsRegular():
		in, err := os.Open(source)
		if err != nil {
			return Entry{}, err
		}
		defer in.Close()
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return Entry{}, err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(out, hash), contextReader{ctx, in})
		closeErr := out.Close()
		if copyErr != nil {
			return Entry{}, copyErr
		}
		if closeErr != nil {
			return Entry{}, closeErr
		}
		if size != info.Size() {
			return Entry{}, fmt.Errorf("state file changed while copying: %s", name)
		}
		mode := info.Mode().Perm()
		if err := os.Chmod(destination, mode); err != nil {
			return Entry{}, err
		}
		return Entry{Path: name, Kind: EntryFile, Mode: uint32(mode), Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
	default:
		return Entry{}, fmt.Errorf("unsupported state file type: %s", name)
	}
}

type contextReader struct {
	ctx context.Context
	in  io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.in.Read(data)
}

func safeLink(name, target string) bool {
	if target == "" || filepath.IsAbs(target) {
		return false
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(name), target))
	return resolved != ".." && !strings.HasPrefix(resolved, ".."+string(filepath.Separator))
}

// Verify rejects changed files, unsafe links, and unlisted backup content.
func Verify(ctx context.Context, backup string) (Manifest, error) {
	body, err := os.ReadFile(filepath.Join(backup, manifestName))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.Format != backupFormat || manifest.Source == "" || manifest.CreatedAt == "" {
		return Manifest{}, fmt.Errorf("unsupported backup manifest")
	}
	seen := collections.Set[string]{}
	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		name := filepath.FromSlash(entry.Path)
		if name == "." || filepath.IsAbs(name) || name == manifestName || name == lockName ||
			name != filepath.Clean(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || seen.Has(name) {
			return Manifest{}, fmt.Errorf("invalid backup entry: %s", entry.Path)
		}
		seen.Add(name)
		path := filepath.Join(backup, name)
		info, err := os.Lstat(path)
		if err != nil {
			return Manifest{}, err
		}
		switch entry.Kind {
		case EntryDirectory:
			if !info.IsDir() {
				return Manifest{}, fmt.Errorf("backup directory changed: %s", entry.Path)
			}
		case EntrySymlink:
			target, err := os.Readlink(path)
			if err != nil || target != entry.Target || !safeLink(name, target) {
				return Manifest{}, fmt.Errorf("backup link changed: %s", entry.Path)
			}
		case EntryFile:
			if !info.Mode().IsRegular() || info.Size() != entry.Size || (entry.Mode != 0 && uint32(info.Mode().Perm()) != entry.Mode) {
				return Manifest{}, fmt.Errorf("backup file changed: %s", entry.Path)
			}
			hash, err := fileHash(ctx, path)
			if err != nil || hash != entry.SHA256 {
				return Manifest{}, fmt.Errorf("backup checksum mismatch: %s", entry.Path)
			}
		default:
			return Manifest{}, fmt.Errorf("unknown backup entry type: %s", entry.Path)
		}
	}
	err = filepath.WalkDir(backup, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(backup, path)
		if err != nil || rel == "." || rel == manifestName {
			return err
		}
		if !seen.Has(rel) {
			return fmt.Errorf("unlisted backup entry: %s", rel)
		}
		return nil
	})
	return manifest, err
}

func fileHash(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx, file}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// VerifyCurrent confirms an offline backup still represents the live home.
func VerifyCurrent(ctx context.Context, home, backup string) error {
	manifest, err := Verify(ctx, backup)
	if err != nil {
		return err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return err
	}
	if home != manifest.Source {
		return fmt.Errorf("backup was created for a different state directory")
	}
	seen := collections.Set[string]{}
	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(home, filepath.FromSlash(entry.Path))
		seen.Add(filepath.FromSlash(entry.Path))
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch entry.Kind {
		case EntryDirectory:
			if !info.IsDir() {
				return fmt.Errorf("state changed after backup: %s", entry.Path)
			}
		case EntrySymlink:
			target, err := os.Readlink(path)
			if err != nil || target != entry.Target {
				return fmt.Errorf("state changed after backup: %s", entry.Path)
			}
		case EntryFile:
			if !info.Mode().IsRegular() || info.Size() != entry.Size || (entry.Mode != 0 && uint32(info.Mode().Perm()) != entry.Mode) {
				return fmt.Errorf("state changed after backup: %s", entry.Path)
			}
			hash, err := fileHash(ctx, path)
			if err != nil || hash != entry.SHA256 {
				return fmt.Errorf("state changed after backup: %s", entry.Path)
			}
		}
	}
	return filepath.WalkDir(home, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(home, path)
		if err != nil || rel == "." || rel == lockName {
			return err
		}
		if !seen.Has(rel) {
			return fmt.Errorf("state gained a file after backup: %s", rel)
		}
		return nil
	})
}

// Restore materializes a verified backup into a new state directory.
func Restore(ctx context.Context, backup, destination string) error {
	manifest, err := Verify(ctx, backup)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	backup, err = filepath.Abs(backup)
	if err != nil {
		return err
	}
	if _, _, err := backupPaths(backup, destination); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("restore destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".turnyard-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, entry := range manifest.Entries {
		name := filepath.FromSlash(entry.Path)
		item, err := os.Lstat(filepath.Join(backup, name))
		if err != nil {
			return err
		}
		if _, err := copyEntry(ctx, filepath.Join(backup, name), filepath.Join(stage, name), name, dirEntry{item}); err != nil {
			return err
		}
	}
	if manifest.Source != destination {
		if err := relocateDatabase(ctx, filepath.Join(stage, store.DatabaseFileName), manifest.Source, destination); err != nil {
			return err
		}
	}
	return os.Rename(stage, destination)
}

type dirEntry struct{ os.FileInfo }

func (d dirEntry) Type() fs.FileMode          { return d.Mode().Type() }
func (d dirEntry) Info() (fs.FileInfo, error) { return d.FileInfo, nil }
