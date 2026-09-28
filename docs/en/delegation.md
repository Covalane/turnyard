# Primary agents and managed delegation

[Guide](README.md) · [中文](../zh/delegation.md)

## Purpose

`Session.primary_agent` selects the primary agent. Other environment agents remain a catalog until the primary agent lists them in `agents[].delegates`. That list exposes the `turnyard_delegate` MCP tool. Without it, native tools, skills and MCP continue unchanged. Runtime-native subagents remain owned by their runtime and are not represented as Turnyard managed children.

Each managed delegation is an independent child session and task with its own native session, turns, invocations, logs, checkpoints, candidate and outputs. SQLite links it to the parent session, task and invocation. `task show` and `session show` include `delegations` summaries.

## Configuration and use

Declare each agent with its own runtime, model, skills and tools, then allow specific children on the primary agent:

```json
{
  "agents": [
    {"id": "lead", "runtime": "opencode", "model_binding": "main", "delegates": ["helper"]},
    {"id": "helper", "runtime": "kimi", "model_binding": "child", "tools": ["search"]}
  ]
}
```

The primary agent uses `find_tools` on the single MCP gateway to discover `turnyard_delegate/delegate`, then calls it through `call_tool` with `action: "submit"`, a stable `key`, an allowed `agent_id`, objective, acceptance conditions, full repository scope, trusted checks and declared outputs. `input_ids` may reference the parent's staged attachments. Each child task needs at least one check or output. `submit` returns a child session ID and status; `status` inspects the same key. A repeated key and identical request returns the same child; changed input conflicts. `continue` accepts a reply for `needs_input` or an explicit retry for `failed`. An `unknown` child requires an operator to inspect external effects and run `task reconcile <child-task-id>` before retrying.

The child clones the parent's repository HEAD into an isolated workspace. Submission rejects uncommitted parent edits so the child cannot silently miss them. Its repository permissions cannot exceed the parent's scope. Child sessions disable remote Git publication and further managed delegation. Only after a candidate passes its checks and output verification does `status` return a read-only `handoff` under `/turnyard-control/delegations/<child-session-id>/`. Git blobs and deletions use the `repositories/` namespace; sealed non-Git files use `artifacts/`. Handoffs allow up to 512 MiB per file and 2 GiB of Git changes. A child session completes only after handoff succeeds. Deterministic size or unsupported-entry failures mark the child `failed / HANDOFF_UNAVAILABLE`, allowing an explicit retry under the same key. Transient export errors can be retried with `status`. The primary agent chooses what to integrate; Turnyard does not write into the parent's live workspace concurrently. Parent checks and outputs must still pass. An unfinished child makes the parent task wait, and the parent session cannot finish before its children.

## Capability boundary

The invocation-specific request directory accepts only allowed child agents and scopes within the parent task. Host responses and handoff files are mounted read-only. The supervisor's general control socket is not exposed. A child receives only its granted tool catalog and has separate native state; on Docker, tool credentials stay in the child's invocation-specific tool container. Delegated tasks currently reject PR outputs and host-side external uploads; authorized parent capabilities can handle those later.

`agents[].tools` grants tool gateway backends; it does not deny built-in shell access. To bound network access and model credentials, set `"network": "model-only"` on a Docker sandbox. Turnyard creates an internal network and a fixed-upstream model gateway for each invocation. The agent receives only a placeholder model key; the real model credential stays in the model gateway, while tool credentials stay in a separate tool container. The agent can call granted tools through the single MCP entry point. Checks and native-session inventory run without network access. The agent has no arbitrary external HTTP access. A Docker host administrator can still inspect gateway containers and their credentials, and requests still reach the configured model service.

The regular `network: "default"` mode retains broad agent egress and exposes model credentials to the agent; tool-specific credentials stay in the tool container. Omitting a PR tool alone does not prove the agent cannot open a PR. Real Docker probes verified that `model-only` blocks direct agent connections to the public internet and host, but this is not a fine-grained allowlist across every backend. There is also no uniform per-call tool approval across all four runtimes. The tool container can reach external services, so its destinations still need image and network policy controls. Host-side delivery connectors remain suitable for final external delivery after verification.

Child and parent sessions follow the normal explicit completion, cancellation and offline pruning rules. Prune child sessions before their parent. Interrupted executions remain `unknown`; Turnyard does not automatically replay calls that may have produced external effects.
