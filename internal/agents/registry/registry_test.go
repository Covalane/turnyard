package registry

import (
	"testing"

	"github.com/Covalane/turnyard/internal/contracts"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestDriversAcceptConfiguredToolKinds(t *testing.T) {
	agent := contracts.AgentSpec{ID: "lead", Tools: []string{"witness"}}
	env := contracts.EnvironmentSpec{Tools: []contracts.ToolSpec{{ID: "witness", Kind: "executable", Description: "Run witness", Argv: []string{"witness"}}}}
	for _, runtime := range Runtimes() {
		driver, err := Driver(runtime)
		if err != nil {
			t.Fatal(err)
		}
		if err := driver.ValidateEnvironment(agent, env); err != nil {
			t.Errorf("%s executable kind: %v", runtime, err)
		}
		env.Tools[0].Kind = "mcp"
		if err := driver.ValidateEnvironment(agent, env); err != nil {
			t.Errorf("%s mcp: %v", runtime, err)
		}
		env.Tools[0].Kind = "unknown"
		if code := fault.CodeOf(driver.ValidateEnvironment(agent, env)); code != fault.CodeCapabilityMissing {
			t.Errorf("%s unknown kind: %s", runtime, code)
		}
		env.Tools[0].Kind = "executable"
	}
}
