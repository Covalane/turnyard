# Agent capabilities and tool injection

[User guide](README.md) · [中文](../zh/tools.md)

Turnyard separates the agent runtime, skills, tools, sandbox resources, and supervisor operations. `Environment` registers skills and tools; `agents[].skills` and `agents[].tools` select those made available to an agent. `Session.primary_agent` pins the agent for the session. All four runtimes connect to one Turnyard MCP tool gateway, which connects only to the tools granted to that agent.

## Comparison with open-source implementations

[OpenCode](https://opencode.ai/docs/tools) keeps built-in shell and file tools alongside custom tools and MCP, and controls their use with agent permissions. [ClineCore](https://docs.cline.bot/cline-sdk/sessions) exposes tool approval callbacks at the session layer. [OpenHands](https://docs.openhands.dev/api-reference/store-settings) accepts MCP services with different transports in its runtime environment. Turnyard's gateway handles **discovery, per-tool authorization, argument validation, and routing**; the runtime decides when to call, the sandbox enforces **file, process, and network boundaries**, and trusted checks assess **results**.

The executable bridge preserves arbitrary program arguments and stdin without requiring a driver for each CLI; native MCP services retain their own parameter schemas. The bridge's generic `run(args, stdin)` does not describe a business command's parameters. For complex tools, prefer a native MCP server or a thin MCP adapter. The gateway grants only the backends selected in `agents[].tools`; it cannot disable a runtime's built-in shell or file tools. Sandbox policy governs those capabilities. Turnyard does not yet offer one human approval callback mechanism across all four runtimes.

## One gateway with on-demand discovery

The agent-facing MCP `tools/list` exposes only `find_tools` and `call_tool`. The agent searches first; the gateway returns up to eight matching IDs, descriptions, and input schemas. The agent then calls an exact ID. The gateway rechecks its grant and validates arguments against the backend schema before dispatch. Search results are limited to 64 KiB and call results to 256 KiB. Search adds a model interaction, and returned tool content still enters context.

At startup, the gateway connects to every backend granted to this agent and reads its catalog, then returns matching definitions on demand. This reduces tool definitions in the primary agent's context; backend startup time and memory are still paid. Starting a backend only after a search match is not implemented.

The default matcher is local keyword search. Optionally, an OpenAI Chat-compatible model ranks **already granted tools** when keyword matches are weak or close. A model failure falls back to local search, unknown IDs from its response are discarded, and the model never executes a tool. Model ranking currently handles at most 64 granted tools; larger catalogs still use local search.

```json
"tool_search": {"mode": "llm-rerank", "model_binding": "search-model"}
```

`search-model` must name a `model_bindings` entry with an OpenAI Chat endpoint. Omitting `tool_search` selects local search. `network: "none"` cannot use model ranking.

## Tool sources

| `kind` | Configuration | What the agent can call |
| --- | --- | --- |
| `mcp` | A program that already implements stdio MCP, plus its `argv` | Gateway-discovered tools with IDs `<configured ID>/<tool name>` |
| `executable` | A regular command prefix, a `description`, and an optional bundle directory | A gateway tool named `<configured ID>/run` |

The trusted environment pins an executable's `argv`, for example `['{toolDir}/stamp.sh']` or `['ossutil']`. The bridge starts the process directly; it **does not concatenate model arguments into a shell command**. `path` may point to a directory containing the executable and supporting files. Turnyard pins its digest and copies it to `/state/tools/<id>` for the session. `{toolDir}` resolves to that container directory. Programs already installed in the image need no `path`. The agent image must contain the bridge; this project's `Containerfile` includes it. Missing programs or a missing bridge fail the MCP connection or invocation.

`pass_env` selects named values from the supervisor host; a missing value fails before invocation. In Docker `default` and `model-only` modes, tools run in an invocation-specific container. The agent container runs only an MCP proxy and does not receive tool credentials. In `model-only`, the agent remains on an internal network while the selected tool container can reach external services needed for its granted capability. Apple `container`, Podman, and `network: "none"` use an in-container gateway, where `pass_env` values are visible to the agent process and are not secret isolation. A bridged executable receives only base path variables and its declared `pass_env`. `timeout_seconds` bounds a call; the bridge also bounds input, argument count, and output. A nonzero exit is a tool error. After an external write times out, inspect the external state before retrying.

The Docker tool container mounts only tool configuration, bundles, and separate tool state; it cannot read the agent's native session state, and the agent cannot see the catalog lock. There is currently no per-tool outbound domain allowlist; deployments must enforce any needed destination policy at the network layer.

Separate tool state and the catalog lock persist for the session but are not rolled back with an agent workspace checkpoint. After recovery, inspect the external result of any writing tool call before retrying it.

### Verified executable example

[`examples/executable-witness/stamp.sh`](../../examples/executable-witness/stamp.sh) is a normal script and does not implement MCP. This is an Environment excerpt:

```json
{
  "agents": [{"id": "lead", "runtime": "opencode", "model_binding": "cloud", "tools": ["stamp_cli"]}],
  "tools": [{
    "id": "stamp_cli", "kind": "executable",
    "description": "Generate a witness token from one value.",
    "path": "./examples/executable-witness",
    "argv": ["{toolDir}/stamp.sh"],
    "pass_env": ["TURNYARD_EXEC_WITNESS_MODE"],
    "timeout_seconds": 30
  }]
}
```

The supervisor environment first provides `TURNYARD_EXEC_WITNESS_MODE=enabled`. The agent calls `find_tools`, then `call_tool` with `{"tool_id":"stamp_cli/run","arguments":{"args":["alpha"]}}`. The bridge forwards the declared variable to the script, which returns `EXEC_WITNESS:alpha`; the agent writes it to the repository, and Turnyard independently checks the candidate. See [validation](validation.md) for real-model evidence. A complete Environment also needs sandbox and model bindings. Git policy is required only when the session includes repositories.

To offer `ossutil` to the agent, install it in the image and configure `kind: "executable"`, `argv: ["ossutil"]`, a useful description, and any needed `pass_env`. The model supplies subcommands through `run.args`. For a **required final deliverable**, prefer the [supervisor artifact connector](lifecycle.md): Turnyard uploads only after checks pass, then downloads and verifies the bytes. A real OSS bucket upload and two independent readbacks passed; cleanup of the test object failed because the current account lacks delete permission. See [validation](validation.md).

## Skills, sandbox, and evidence

A skill is guidance; it does not install a program by itself. The image's shell, language tools, allowed repository mounts, and sandbox-permitted network remain available to the agent. `agents[].tools` is not a system-call allowlist. Restrict file and network access through mounts, image contents, network policy, and credential configuration. Docker's `network: "model-only"` lets the agent call the selected cloud model through its own gateway while blocking direct public and host access; selected tools use their credentials in the separate tool gateway. `network: "none"` has no network and cannot directly call a cloud model. Trusted host-side connectors remain available for final delivery. Apple `container` and Podman currently reject `model-only` before execution.

Turnyard records configured skills, gateway connection information, `find_tools`/`call_tool` activity observed in native events, actual backend IDs in gateway logs, and candidate checks. `configured_skills` proves configuration only. Docker gateway logs sit beside the invocation log in a `.gateway.log` file. The gateway pins a catalog digest in the session, outside the agent-state checkpoint, on first start and fails if definitions drift on resume. New tool sources extend the gateway backend layer without a driver for each CLI program.
