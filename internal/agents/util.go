package agents

import "github.com/Covalane/turnyard/internal/contracts"

func SortedKeys[V any](m map[string]V) []string { return contracts.SortedKeys(m) }

func SlicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
