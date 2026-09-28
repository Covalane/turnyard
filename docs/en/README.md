# Turnyard

[中文](../../README.md) · [Architecture](architecture.md) · [Validation](validation.md) · [Lifecycle](lifecycle.md)

Turnyard is a standalone task executor for coding agents. It accepts structured JSON work with file and image attachments, runs an agent in an isolated container, verifies outputs, and delivers them to task-selected locations. Code changes across multiple Git repositories become task-specific feature commits; documents and other files can also be delivered locally or through a configured object-storage connector without Git.

Turnyard owns controlled execution and structured results on one machine. A separate scheduler decides when to run work and which machine should receive it. The supervisor can stay running across sessions; each agent invocation uses a temporary compute container. The host can set task concurrency and resource budgets and inspect state-usage warnings.

Coding work may span repositories and several turns, including a pause for a human decision. Turnyard keeps one requirement in a finite session. After an agent reports completion, it checks a recorded code candidate and confirms declared deliverables. Once all tasks have been reviewed, the session is explicitly completed while its delivery record remains inspectable.

The current drivers are OpenCode, Kimi Code, Claude Code, and Codex CLI. Runtime and model bindings are separate configuration fields. Apple `container`, Docker, and Podman are available behind a sandbox interface; see [validation](validation.md) for combinations actually exercised.
On Linux, run Turnyard under a dedicated non-root account with access to the chosen container runtime. Docker and Podman agent containers use that account's UID/GID for the private session mounts; keep the container runtime's management interface inaccessible to untrusted users.
On a Linux Docker host, a trusted Environment may set `"sandbox":{"backend":"docker","image":"turnyard-agent:dev","isolation":"gvisor","network":"model-only"}`. Register `runsc` with Docker using the [official gVisor guide](https://gvisor.dev/docs/user_guide/quick_start/docker/) first. Turnyard rejects session creation when the runtime is absent. Agent invocations, checks, and native-session inventory use `runsc`; the model gateway remains a trusted service container. A gVisor sandbox probe and an OpenCode cloud-model task passed in a Linux/arm64 Docker-in-Docker environment. Early nested tasks used `--ignore-cgroups`; a later normal-cgroup probe and complete task passed; see [validation](validation.md).

## Build and run

Requires Go 1.27.1, an OCI sandbox backend, and an API key for the chosen model provider; repository work also needs Git. Apple `container` and Docker have completed real workflow validation. For Apple `container`:

```sh
go build -o bin/turnyard ./cmd/turnyard
container system start --enable-kernel-install
container build -f Containerfile -t turnyard-agent:dev .
export OLLAMA_API_KEY='your API key'
bin/turnyard doctor
TURNYARD_E2E=1 go test -tags integration -run TestMultiRepoWorkflow -v -count=1 -timeout 25m ./test/e2e
```

The Go acceptance test creates independent Git repositories and exercises consecutive tasks, human input, and checkpoint restoration. It does not write to GitHub. The agent image retains Python as an optional general development tool. For tasks that must restrict direct agent egress, set `Environment.sandbox.network` to `model-only`; this mode currently requires Docker. See the [capability boundary](delegation.md).

## Inputs and commands

[Session](../../internal/contracts/schemas/session.json) names optional pinned repositories and a creation `idempotencyKey`. [Environment](../../internal/contracts/schemas/environment.json) defines the sandbox, agents, model bindings, skills, native MCP servers or executable tools, checks, attachment host allowlists, and trusted byte-transfer connectors. Agents discover selected tools through one MCP gateway. [Work](../../internal/contracts/schemas/work.json) defines the objective, optional repository scope, acceptance criteria, checks, file inputs, and required outputs. Git work may specify a human-readable `commitMessage`; documents can run without a repository. Files and images may be handed off locally or through a configured connector. See [tool injection](tools.md) and [lifecycle and delivery](lifecycle.md) for the full contracts.

For an existing Go repository on this machine, the [local-go generator](local-go.md) writes three valid, editable JSON inputs from its current HEAD.

```sh
bin/turnyard session create --file session.json
bin/turnyard task add <session-id> --file work.json
bin/turnyard task run <task-id>
bin/turnyard task wait <task-id>
bin/turnyard task show <task-id>
bin/turnyard session complete <session-id>
bin/turnyard session publish <session-id>
bin/turnyard session cancel <session-id> --reason "requirement withdrawn"
bin/turnyard session events <session-id>
```

The online session and task commands above start the local supervisor automatically if it is absent. Use `bin/turnyard daemon start`, `bin/turnyard daemon status`, and `bin/turnyard daemon stop` for explicit process control. `task run` accepts work and returns while the supervisor continues it; `task wait` polls until the task stops. `daemon stop` refuses to stop during active work. There is no separate one-shot execution mode or built-in service manager that restarts the supervisor after a machine reboot.

`session create` allows 10 minutes for repository preparation by default. Set `--timeout <seconds>` to change it, up to 7200 seconds. After a timeout, retry with the same `idempotencyKey` so Turnyard checks for an existing session first.

Use `task reply <task-id> --text "decision"` when a task is `needs_input`. `task verify <task-id>` reruns checks on the same failed candidate; `task retry <task-id>` starts another agent invocation. `task reconcile <task-id>` checkpoints a failed task. For an `unknown` task, the operator should first inspect the workspace, invocation logs, and external tool effects; reconcile confirms the containers have stopped, records an audit event, and makes the task retryable as `failed`. `checkpoint restore <checkpoint-id>` requires the workspace and native state directories to be absent so it cannot overwrite them.

Append tasks to a session for the same requirement. `session complete` succeeds only after every task has a verified candidate and returns a candidate and deliverable manifest. An abandoned requirement can be ended with `session cancel` while no call is active. Terminal sessions cannot execute or restore work. See [lifecycle, delivery, and storage](lifecycle.md).

State defaults to `~/.local/share/turnyard`; set `TURNYARD_HOME` or prefix a command with `--home <directory>`. The SQLite event log and `daemon.log` provide operational traceability; private invocation logs contain agent and check output. `daemon.log` records identifiers and outcomes, not task text or API keys. Protect and back up the state directory.

`verified` means the recorded candidate passed configured checks and declared deliverable validation; it remains subject to human review. `unknown` means an interrupted or timed-out operation needs inspection and explicit reconciliation before retry. Remote feature-branch publication is opt-in after session completion; see the [Git workflow](git.md). Turnyard does not create pull requests or merge branches.

For checks maintained outside agent-writable code, see [task acceptance boundaries](acceptance.md).

## License

Turnyard is licensed under the [Apache License 2.0](../../LICENSE).
