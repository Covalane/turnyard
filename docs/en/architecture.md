# Architecture and extensions

[User guide](README.md) · [中文](../zh/architecture.md)

## Execution path

Turnyard owns one machine and its state directory. A separate system chooses task order and the execution machine, then uses the local CLI to submit inputs and read status and results. Turnyard does not allocate tasks across machines or share session state between them. One supervisor can serve multiple sessions continuously, with one active task at a time per session.

1. The input layer validates JSON Schema, repository and check references, connector references, resource settings, and runtime bindings. Session creation locks the image and any pinned repositories. Task append stages declared file inputs into content-pinned snapshots before storing the task.
2. The supervisor accepts commands on a private Unix socket and persists sessions, tasks, turns, invocations, candidates, checkpoints, and events in SQLite. It permits one active invocation per session.
3. Shared tool preparation gathers granted native MCP services, ordinary executables, managed delegation, and the built-in human-input request behind one tool gateway. The gateway exposes discovery and calling, then checks authorization and arguments for the selected backend; the executable bridge still accepts structured calls. Each `AgentDriver` maps that same gateway, the model binding, skills, native session ID, and prompt into its CLI configuration. Native state lives outside the disposable container under a private `/state` mount.
4. A `SandboxBackend` runs an ephemeral OCI container with read-only input snapshots, an invocation-specific output mount, and repository-specific read/write mounts when needed. On writable repositories, `.git` is mounted read-only again. Turnyard uses go-git to create feature branches and commit code after the agent exits.
5. A checkpoint archives the workspace and native state with a SHA-256 digest and path/link validation on restore. A candidate binds repository versions, the work specification, and declared output bytes. Non-Git outputs are sealed outside later agent mounts. Checks precede external delivery; a configured connector uploads to a content-addressed URI and downloads it for hash verification.
6. Once every task has a verified candidate and the workspace matches the latest checkpoint, the session can be explicitly completed. Its completion manifest records candidate digests and deliverable results; the terminal session cannot run or restore work.

## Extension contracts

Source packages follow the dependency direction:

| Directory | Responsibility |
| --- | --- |
| `internal/contracts` | JSON inputs, schemas, and domain input types |
| `internal/lifecycle` | Stable session, task, and event names |
| `internal/fault` | Stable error codes, causes, and origin locations |
| `internal/agents` | Agent drivers, model evidence, skill/tool injection, and native state file safety |
| `internal/toolgateway` | MCP catalog, search, per-tool validation, and call routing |
| `internal/artifacts` | Required-output manifest, invocation-scoped claims, local validators, and replaceable PR readback |
| `internal/humaninput` | Invocation-scoped structured human-input requests and validation |
| `internal/inputs` | Attachment staging and snapshot verification |
| `internal/artifactio` | Trusted host-side CLI byte-transfer connectors |
| `internal/sandbox` | Sandbox contract, shared OCI lifecycle, mounts, resource limits, Docker gateway coordination, and cleanup |
| `internal/sandbox/{applecontainer,docker,podman}` | Backend-specific CLI dialects and command options |
| `internal/sandbox/ocicli` | Compatible Docker/Podman CLI operations and image-inspection error classification |
| `internal/sandbox/registry` | Explicit selection and composition of built-in sandbox backends |
| `internal/gitstate` | Branches, commits, candidate versions, and checkpoint archives |
| `internal/store` | Ent schemas, SQLite persistence, domain read/write operations, and transactions; the engine cannot access the Ent client or SQL transactions |
| `internal/observe` | Structured logs with context correlation fields |
| `internal/engine` | Task state machine, checks, recovery, and result orchestration |
| `internal/transport` | Unix socket supervisor and CLI |
| `cmd/turnyard` | Executable entry point |
| `cmd/turnyard-tool-gateway` | Agent-facing MCP entry point and Docker tool-container service |

The command layer depends on the engine, which depends on agents, sandbox, Git, and storage. `contracts` provides shared input and types without depending on execution implementations.

Each `AgentDriver` validates its provider binding and owns native invocation and parsing. The four implementations live in `internal/agents/{claude,codex,kimi,opencode}` and are composed explicitly by `internal/agents/registry`, without init-time registration. A new driver must define credential delivery, native session continuation, evidence of the actual model, and tool-call reporting. Human-input requests use the shared tool rather than natural-language parsing in each driver.

Built-in provider endpoints and API transports are collected in `internal/agents/model_provider.go`. Each `model_binding.credential_env` selects a host environment variable read at invocation time. Each driver selects a supported transport and retains CLI-specific configuration and model-evidence parsing. Other trusted providers can declare an HTTPS `model_binding.endpoints` entry for `openai_chat`, `anthropic`, or `responses`, without changing a driver. Built-in providers cannot override their endpoints, so the selected credentials are not accidentally sent elsewhere. An accepted configuration does not establish real-model validation.

The Environment JSON Schema checks the shape of runtime, sandbox, provider, credential variable, and tool-kind names. Session creation resolves them through `DriverFactory`, `BackendFactory`, and driver capability checks. Adding an implementation does not require extending schema enums; unavailable implementations are still rejected when a session is created.

`SandboxBackend` covers probing, capability validation, image identity, execution, and cleanup. `OCIBackend` owns the shared lifecycle and mount policy. Apple `container`, Docker, and Podman have CLI dialects in separate packages, composed explicitly by `internal/sandbox/registry`; Docker and Podman share only compatible CLI operations. The shared OCI run path still coordinates startup and cleanup of Docker-specific model and tool gateways. A new OCI backend implements `Dialect` and joins the registry. A backend that cannot meet the shared lifecycle assumptions should implement `SandboxBackend` independently instead of adding exceptions to every dialect. Docker and Podman start agent containers with the supervisor's UID/GID so private bind mounts remain writable. Non-root Podman selects the `keep-id` user namespace; rootful Podman uses its existing identity mapping.

Docker optionally accepts `isolation: "gvisor"`: agent invocations, checks, and native-session inventory select `runsc` after the daemon's runtime registration is checked at session creation. Because gVisor cannot reliably resolve the gateway alias on Docker user-defined networks, the agent container receives the invocation's internal gateway address in its hosts mapping; host networking is not used. A new backend must preserve the read-only root, repository scope, `.git` protection, resource limits, timeout cleanup, and redacted logs. The final gateway passed real single-task runs on Apple `container`, Podman in nested Linux, and gVisor. The macOS AppleHV Podman VM still requires validation. Normal-cgroup gVisor limits were measured in nested Linux, while a separate deployment host still needs validation; see [validation](validation.md).

The engine holds no SQL handle or transaction object: the store owns atomic session, invocation, candidate, and checkpoint operations. The sandbox keeps lifecycle and mount policy separate from OCI command dialects. Agent drivers share invocation preparation and main-process outcome handling while retaining their own native configuration, resume, and evidence parsing. See [errors and observability](observability.md) for correlation and error handling.

Engine operations return explicit result types for session creation, task append and execution, verification, restoration, and completion. Store projects Ent entities into domain read models before transport serializes them as JSON. Task execution prepares Git branches, invokes the agent, records a candidate, and runs checks in separate stages; the sandbox backend owns mount and credential argument assembly.

Turnyard-owned Session, Environment, and Work inputs, RPC results, delegation messages, artifact claims, and log fields use `snake_case`. This remains a pre-release v1 contract; test inputs and state created with the former camelCase fields must be regenerated. Native agent events, MCP, Docker, GitHub, and JSON Schema standard fields retain the names required by their upstream protocols.

The persistence model lives in `internal/store/ent/schema`; `go generate ./internal/store` regenerates `internal/store/ent`. Store projects generated entities into stable domain results and owns transaction boundaries. Opening an older workspace imports its `task_repositories` bases into `task_bases` once. The OpenCode driver separately reads OpenCode's own native SQLite session file to check model evidence; it is not Turnyard's business database.

Model binding is separate from runtime selection, but arbitrary runtime/provider/model combinations are not supported. Each driver validates its own compatibility. The combinations with real end-to-end evidence are listed in [validation](validation.md).

`Session.primary_agent` selects the primary agent; other environment agents are not launched automatically. `agents[].delegates` exposes managed delegation with independent child sessions, workspaces and candidates. Runtime-native subagents remain owned by their runtime. See [primary agents and managed delegation](delegation.md) for state, isolation and handoff behavior.

## Recovery and trust boundary

Human input releases the compute container while retaining native state and the Git workspace. A checkpoint restores disk state, not a memory snapshot or a subscription login across machines. API keys are read from the host environment per invocation and are absent from Environment JSON. Docker `model-only` keeps the real model credential in a model gateway and tool credentials in a separate tool container; the agent receives a model placeholder and an invocation-specific internal network. Docker regular network mode also keeps tool credentials in the tool container, while the agent retains broad egress and its own model credential. Other backends run the tool gateway in the agent container and do not isolate tool secrets.

`verified` means configured machine checks and required outputs passed their respective checks, not that the requirement or security of the code is proven. A failed check can be rerun against the same candidate. Timeout or supervisor interruption yields `unknown` because an external MCP call might have an effect that cannot be inferred automatically. After operator inspection, explicit `task reconcile` confirms the related containers and invocation network are stopped, checkpoints the workspace, and permits a retry of the same task. Host-side go-git can publish feature branches after explicit enablement and session completion. PR readback has a replaceable interface; managed PR creation is not implemented. Managed cross-agent delegation uses separate child sessions and hands off only verified candidates. Docker tool gateway calling and network boundaries have been exercised; other backends do not yet provide equivalent tool-secret isolation.
