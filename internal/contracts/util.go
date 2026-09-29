package contracts

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/Covalane/turnyard/internal/collections"
	"github.com/Covalane/turnyard/internal/fault"
)

func ErrorCode(err error) string { return string(fault.CodeOf(err)) }

func JSONText(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fault.Wrap(fault.CodeInternalError, "encode JSON", err, "value cannot be encoded")
	}
	return string(data), nil
}

func NewID(prefix string) (string, error) {
	return newID(prefix, rand.Reader)
}

func newID(prefix string, entropy io.Reader) (string, error) {
	var bytes [8]byte
	if _, err := io.ReadFull(entropy, bytes[:]); err != nil {
		return "", fault.Wrap(fault.CodeInternalError, "generate ID", err, "random source unavailable")
	}
	return prefix + "_" + hex.EncodeToString(bytes[:]), nil
}

func Digest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fault.Wrap(fault.CodeInternalError, "encode digest input", err, "digest input cannot be encoded")
	}
	var normal any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // Preserve integers above 2^53 during key ordering.
	if err := decoder.Decode(&normal); err != nil {
		return "", fault.Wrap(fault.CodeInternalError, "decode digest input", err, "digest input cannot be normalized")
	}
	canonical, err := json.Marshal(normal)
	if err != nil {
		return "", fault.Wrap(fault.CodeInternalError, "encode normalized digest input", err, "digest input cannot be normalized")
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func FileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func uniqueIDs(ids []string, label string) error {
	seen := collections.Set[string]{}
	for _, id := range ids {
		if !seen.Add(id) {
			return fault.New(fault.CodeInvalidSpec, "duplicate %s ID: %s", label, id)
		}
	}
	return nil
}

func resolvePath(base, value string) (string, error) {
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	return filepath.Abs(filepath.Join(base, value))
}

func SortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
