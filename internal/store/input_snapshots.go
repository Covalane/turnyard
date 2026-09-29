package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Covalane/turnyard/internal/collections"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/store/ent/task"
)

// ReferencedInputBatches returns the attachment batches owned by persisted
// tasks. Startup cleanup uses this read-only view before accepting requests.
func (s *Store) ReferencedInputBatches(ctx context.Context) (collections.Set[string], error) {
	refs := collections.NewSet[string](0)
	const pageSize = 256
	after := ""
	for {
		tasks, err := s.client.Task.Query().Select(task.FieldID, task.FieldSessionID, task.FieldSpec).
			Where(task.IDGT(after)).Order(task.ByID()).Limit(pageSize).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range tasks {
			var work contracts.WorkSpec
			if err := json.Unmarshal([]byte(item.Spec), &work); err != nil {
				return nil, fmt.Errorf("decode persisted task %s inputs: %w", item.ID, err)
			}
			for _, input := range work.Inputs {
				if input.Source.Kind != contracts.InputStaged {
					continue
				}
				batch, _, ok := strings.Cut(input.Source.Path, "/")
				if !ok || len(batch) != 64 {
					return nil, fmt.Errorf("persisted task %s has invalid input snapshot path", item.ID)
				}
				decoded, decodeErr := hex.DecodeString(batch)
				if decodeErr != nil || hex.EncodeToString(decoded) != batch {
					return nil, fmt.Errorf("persisted task %s has invalid input snapshot path", item.ID)
				}
				refs.Add(item.SessionID + "/" + batch)
			}
		}
		if len(tasks) < pageSize {
			return refs, nil
		}
		after = tasks[len(tasks)-1].ID
	}
}
