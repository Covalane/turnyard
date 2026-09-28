// Package registry composes built-in agent drivers without init-time side effects.
// Each driver imports the shared agents API, while the engine imports only this
// composition root when it needs a concrete implementation.
package registry

import (
	"github.com/Covalane/turnyard/internal/agents"
	"github.com/Covalane/turnyard/internal/agents/claude"
	"github.com/Covalane/turnyard/internal/agents/codex"
	"github.com/Covalane/turnyard/internal/agents/kimi"
	"github.com/Covalane/turnyard/internal/agents/opencode"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

var factories = map[string]func() agents.AgentDriver{
	agents.RuntimeClaude:   func() agents.AgentDriver { return claude.Driver{} },
	agents.RuntimeCodex:    func() agents.AgentDriver { return codex.Driver{} },
	agents.RuntimeKimi:     func() agents.AgentDriver { return kimi.Driver{} },
	agents.RuntimeOpenCode: func() agents.AgentDriver { return opencode.Driver{} },
}

func Runtimes() []string { return contracts.SortedKeys(factories) }

func Driver(runtime string) (agents.AgentDriver, error) {
	if factory := factories[runtime]; factory != nil {
		return factory(), nil
	}
	return nil, fault.New(fault.CodeRuntimeUnavailable, "runtime %s has no verified driver", runtime)
}
