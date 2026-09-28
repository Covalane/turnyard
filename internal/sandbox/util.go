package sandbox

import (
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/observe"
)

var operationalLog = observe.Log

func sortedKeys[V any](m map[string]V) []string { return contracts.SortedKeys(m) }
