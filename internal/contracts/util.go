package contracts

import (
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

func JSONText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func NewID(prefix string) string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(bytes[:])
}

func Digest(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var normal any
	if err := json.Unmarshal(data, &normal); err != nil {
		panic(err)
	}
	canonical, err := json.Marshal(normal)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
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
