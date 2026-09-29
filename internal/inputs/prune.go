package inputs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/collections"
)

// PruneOrphaned removes staged batches with no committed task. It runs under
// the supervisor's exclusive state lock before new requests are accepted.
func PruneOrphaned(ctx context.Context, stateRoot string, referenced collections.Set[string]) (int, error) {
	sessions, err := os.ReadDir(filepath.Join(stateRoot, "sessions"))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, session := range sessions {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !session.IsDir() {
			continue
		}
		inputRoot := filepath.Join(stateRoot, "sessions", session.Name(), "inputs")
		batches, err := os.ReadDir(inputRoot)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return removed, err
		}
		for _, batch := range batches {
			if !batch.IsDir() || !isBatchDigest(batch.Name()) || referenced.Has(session.Name()+"/"+batch.Name()) {
				continue
			}
			if err := os.RemoveAll(filepath.Join(inputRoot, batch.Name())); err != nil {
				return removed, fmt.Errorf("remove orphaned input batch for %s: %w", session.Name(), err)
			}
			removed++
		}
	}
	return removed, nil
}

func isBatchDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
