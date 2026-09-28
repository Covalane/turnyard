# Turnyard

[中文](../../README.md) · [Architecture](architecture.md) · [Validation](validation.md) · [Lifecycle](lifecycle.md)

Turnyard is a standalone task executor for coding agents. Describe the work, execution environment, checks, and required outputs in JSON. Turnyard runs an agent in a single-host sandbox, keeps a resumable session, checks a pinned candidate, and returns an inspectable delivery record.

Coding work can span repositories and turns, pause for a human decision, and produce files or text as well as code. Turnyard groups those steps into a finite session. It executes work on one machine; a separate system decides when to run work and which machine receives it.

## What it does

| Capability | Current approach |
| --- | --- |
| Agents and models | OpenCode, Kimi Code, Claude Code, and Codex CLI; runtime and model binding are configured separately, subject to driver compatibility |
| Sessions | Resume the same native agent session across tasks, accept human replies, restore checkpoints, and close the session explicitly |
| Sandboxes | Apple `container`, Docker, and Podman backends; optional gVisor `runsc` with Linux Docker |
| Outputs | Optional multiple Git repositories and task-specific feature commits; files, images, and text can also be delivered without Git |
| Tools | Skills, native MCP servers, executable tools, and managed delegation behind a searchable MCP gateway |
| Records | Candidate versions, checks, required outputs, state events, and invocation logs |

Sandbox security differs by backend. Docker `model-only` can block direct agent egress and keep model and tool credentials in separate gateways. Read the [architecture](architecture.md) and [validation matrix](validation.md) for exact boundaries. `verified` means the configured checks and required-output validation passed; it does not replace human review of the requirement.

## Quick start

This example uses Docker, OpenCode, and Ollama Cloud with an **existing, clean Go Git repository**. You need Go 1.27.1, Docker, Git, and a model API key. From the Turnyard repository root:

```sh
go build -o bin/turnyard ./cmd/turnyard
docker build -f Containerfile -t turnyard-agent:dev .
export OLLAMA_API_KEY='your API key'
bin/turnyard doctor
```

Generate three editable JSON files outside the target repository. The output directory must not contain files with the same names:

```sh
go run ./examples/local-go \
  --repo /absolute/path/to/your/go-repo \
  --out /tmp/turnyard-first-task \
  --objective 'Add a health check and tests to the service' \
  --commit-message 'feat: add health check'
```

Review the generated model binding, check command, and expected outputs. To require a specific file, add `--deliverable` to the generator or edit `work.json`. Assign the returned `session_id` and `task_id` to the variables below:

```sh
bin/turnyard session create --file /tmp/turnyard-first-task/session.json
SESSION_ID='paste the returned session_id'
bin/turnyard task add "$SESSION_ID" --file /tmp/turnyard-first-task/work.json
TASK_ID='paste the returned task_id'
bin/turnyard task run "$TASK_ID"
bin/turnyard task wait "$TASK_ID"
bin/turnyard task show "$TASK_ID"
bin/turnyard session complete "$SESSION_ID"
```

`task run` returns after scheduling; the resident supervisor continues in the background. You can append more work before completing the session. Git changes stay on local task feature branches by default. Remote publication is explicit and happens after completion. Turnyard does not manage PR creation or branch merging.

## Configure the inputs

[Session](../../internal/contracts/schemas/session.json) names optional pinned repositories and a creation `idempotency_key`. [Environment](../../internal/contracts/schemas/environment.json) defines the sandbox, agents, model bindings, skills, native MCP servers or executable tools, checks, attachment host allowlists, and trusted byte-transfer connectors. Agents discover selected tools through one MCP gateway. [Work](../../internal/contracts/schemas/work.json) defines the objective, optional repository scope, acceptance criteria, checks, file inputs, and required outputs. Git work may specify a human-readable `commit_message`; documents can run without a repository. Files and images may be handed off locally or through a configured connector. See [tool injection](tools.md) and [lifecycle and delivery](lifecycle.md) for the full contracts.

The [local-go generator](local-go.md) pins the repository's current HEAD. For additional repositories, attachments, or non-Git output, edit the JSON contracts directly.

## Run and close a session

Use `session publish <session-id>` for explicitly enabled remote Git writes, `session cancel <session-id> --reason "..."` to end an abandoned requirement, and `session events <session-id>` to inspect its state history.

The session and task commands start the local supervisor automatically if it is absent. Use `bin/turnyard daemon start`, `bin/turnyard daemon status`, and `bin/turnyard daemon stop` for explicit process control. `daemon stop` refuses to stop during active work. Turnyard does not include a service manager to restart the supervisor after a machine reboot.

`session create` allows 10 minutes for repository preparation by default. Set `--timeout <seconds>` to change it, up to 7200 seconds. After a timeout, retry with the same `idempotency_key` so Turnyard checks for an existing session first.

## Human input and recovery

Use `task reply <task-id> --text "decision"` when a task is `needs_input`. `task verify <task-id>` reruns checks on the same failed candidate; `task retry <task-id>` starts another agent invocation. `task reconcile <task-id>` checkpoints a failed task. For an `unknown` task, the operator should first inspect the workspace, invocation logs, and external tool effects; reconcile confirms the containers have stopped, records an audit event, and makes the task retryable as `failed`. `checkpoint restore <checkpoint-id>` requires the workspace and native state directories to be absent so it cannot overwrite them.

Append tasks to a session for the same requirement. `session complete` succeeds only after every task has a verified candidate and returns a candidate and deliverable manifest. An abandoned requirement can be ended with `session cancel` while no call is active. Terminal sessions cannot execute or restore work. See [lifecycle, delivery, and storage](lifecycle.md).

## State and result records

State defaults to `~/.local/share/turnyard`; set `TURNYARD_HOME` or prefix a command with `--home <directory>`. The SQLite event log and `daemon.log` provide operational traceability; private invocation logs contain agent and check output. `daemon.log` records identifiers and outcomes, not task text or API keys. Protect and back up the state directory.

`verified` means the recorded candidate passed configured checks and declared deliverable validation; it remains subject to human review. `unknown` means an interrupted or timed-out operation needs inspection and explicit reconciliation before retry. Remote feature-branch publication is opt-in after session completion; see the [Git workflow](git.md). Turnyard does not create pull requests or merge branches.

For checks maintained outside agent-writable code, see [task acceptance boundaries](acceptance.md).

## License

Turnyard is licensed under the [Apache License 2.0](../../LICENSE).
