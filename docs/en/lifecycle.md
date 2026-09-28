# Lifecycle, delivery, and storage

[User guide](README.md) · [中文](../zh/lifecycle.md)

## One requirement, one finite session

Session creation pins repository commits, the environment, and the primary agent. More tasks can be appended for the same requirement. A task can run, wait for human input, retry after failure, or rerun checks against the same candidate. Compute containers exist only during invocation; the workspace, native agent state, and checkpoints support continuation after a pause.

When a human decision is required, the agent calls the built-in `turnyard_input/request_input` through the shared MCP gateway with `{"question":"the question to answer"}`. The tool records a structured, invocation-scoped request. After the agent ends the turn, Turnyard saves a candidate and checkpoint and sets the task to `needs_input`; checks and delivery wait. The operator uses `task reply <task-id> --text "answer"` to continue the same native session. Ordinary answer text, including phrases such as “no input needed,” cannot trigger a pause; Turnyard no longer parses a response marker.

Creation requires an `idempotency_key`. If the response is lost or the client times out, retrying with the same key and original input returns the same session; changed input with that key conflicts. Repository preparation defaults to a 10-minute limit, adjustable with `session create --timeout <seconds>`.

| Session state | Meaning | Next action |
| --- | --- | --- |
| `ready` | No active call; can append after the prior task is verified | Run, complete, or cancel |
| `running` | Agent invocation in progress | Await result; no terminal action |
| `paused` | Awaiting human input, retry, or fault handling | Reply, recheck, retry, or cancel; inspect and reconcile `unknown` first |
| `completed` | All tasks verified and delivery confirmed | Review; optionally publish feature branches |
| `cancelled` | Requirement withdrawn; unfinished tasks do not advance | Read-only review |

`verified` means the candidate passed configured machine checks and declared deliverable validation. After review of the whole requirement, call `session complete`. The session must be `ready` and contain at least one task; every task must reference a verified candidate, and the workspace must match the latest checkpoint. Completion records `completed_at` and an audit event. To withdraw a requirement, an operator can call `session cancel <id> --reason "reason"` while the session is `ready` or `paused`; unfinished tasks become `cancelled`, and existing candidates remain. Its response uses `turnyard.session-cancellation/v1`. Neither terminal action is allowed during an active call. Repeated terminal calls return their original timestamps. Terminal sessions cannot append, run, recheck, or restore work. A completed session can explicitly publish verified feature branches under the [Git workflow](git.md); a cancelled session cannot. Code, checks, events, and logs remain on disk for review.

An `unknown` task needs inspection of external effects and explicit `task reconcile` before session cancellation. Data does not expire automatically after completion or cancellation. An operator can make an offline backup, verify it, and explicitly prune a terminal session. There is no timed retention policy; the operator must protect the backup.

## Offline backup, restore, and pruning

Stop the supervisor before operating on the state directory:

```sh
bin/turnyard daemon stop
bin/turnyard state backup --out /secure/backups/turnyard-2026-09-26
bin/turnyard state verify --from /secure/backups/turnyard-2026-09-26
bin/turnyard session prune <completed-or-cancelled-session-id> --backup /secure/backups/turnyard-2026-09-26
```

The restricted backup directory contains SQLite, workspaces, native agent state, logs, and checkpoints. Its manifest records each file's SHA-256. The backup must be outside the state directory. Pruning rechecks the backup and requires the live state to match it exactly before removing the terminal session's database records and files. Make a new backup before pruning another session. `state restore --from <backup> --to <new-directory>` restores the complete state and relocates stored state paths. Backups can contain source code, task text, and native agent state; handle them as sensitive data.

## Task attachments and deliverables

`Work.inputs` provides actual image, document, or other file bytes. Each input has an `id`, a `source`, optional `media_type`, and optional `expected_sha256`:

| Source | Fields | Handling |
| --- | --- | --- |
| `file` | `path` | Read from the submitting host; relative to the Work JSON directory |
| `https` | `url` | Download only from `Environment.input_hosts`; a SHA-256 pin is required, and redirects are checked |
| `connector` | `connector`, `uri` | Download with a configured connector from an allowed URI prefix; a SHA-256 pin is required |

At `task add`, Turnyard copies and verifies the bytes into a read-only session snapshot. Later changes to the original source cannot change the task. Original URLs, signed query strings, and local source paths are not stored in the task record. An input is currently limited to 256 MiB. Intake, queueing, and persistence share a 20-minute default deadline, adjustable with `task add --timeout <seconds>`. The agent sees `/workspace/.turnyard-input/<snapshot>/<id>`; whether an image is interpreted visually depends on the selected runtime and model.

`Work.deliverables` declares required results. Every task must declare at least one trusted check or one deliverable; a task with neither cannot become verified:

| Kind | Location | Verification |
| --- | --- | --- |
| `file` | Candidate Git commit with `repository` and `path`, or `/workspace/.turnyard-output/files/<path>` when repository is omitted | Git tree or sealed file SHA-256; use this kind for arbitrary document and binary formats |
| `image` | Same as file | Also checks PNG/JPEG/GIF/WebP signature; other formats can be delivered as `file` |
| `text` | Invocation claim in `artifacts.json` | Nonempty UTF-8, 64 KiB limit, SHA-256; does not prove the statement is true |
| `pull_request` | URL claim | Provider readback of repository, state, base branch, and candidate commit |

A file or image `destination` may be `{"kind":"local"}` or `{"kind":"connector","connector":"oss","uri":"oss://bucket/reports/{sha256}.pdf"}`. A repository file with no destination remains in its Git commit; a non-repository file must declare a destination. `local` returns `local_path` in the session state directory. A connector uploads to a content-addressed URI only after checks pass, then downloads and compares SHA-256; its result contains `uri`. An uncertain upload or failed readback cannot pass. `task verify` can retry readback on the same candidate. Configure uploads to skip existing objects so conflicting bytes cannot be overwritten.

Trusted checks receive sealed files by output `id` as read-only `/workspace/.turnyard-output/<id>`. They can check document content before external delivery. For a general `file`, `media_type` is declared metadata; the built-in signature check applies only to the image formats listed above.

A document-only task can use empty `Session.repositories` and `Work.scope.repositories` arrays:

```json
{
  "inputs": [{"id": "brief", "source": {"kind": "file", "path": "brief.png"}, "media_type": "image/png"}],
  "deliverables": [{"id": "report", "kind": "file", "path": "report.pdf", "media_type": "application/pdf",
    "destination": {"kind": "connector", "connector": "oss", "uri": "oss://my-bucket/turnyard/{sha256}.pdf"}}]
}
```

The trusted Environment configures the connector commands and URI boundary; Work cannot supply commands:

```json
{
  "artifact_connectors": [{"id": "oss", "uri_prefix": "oss://my-bucket/turnyard/",
    "get_argv": ["ossutil", "cp", "{uri}", "{file}", "-f"],
    "put_argv": ["ossutil", "cp", "{file}", "{uri}", "--ignore-existing"]}]
}
```

Place this fragment inside a full Environment JSON. Connectors run on the supervisor host with their own OSS configuration; the agent does not need storage credentials. Other CLI tools can implement the same one-file transfer contract. Trusted environment operators must supply the commands. `get_argv` and `put_argv` must each use `{uri}` and `{file}` exactly once. `input_hosts` restricts Turnyard's HTTPS attachment downloader only; default sandbox networking still permits outbound connections and is not a complete agent egress policy.

For text and PR URLs, the agent writes the invocation-scoped `/workspace/.turnyard-output/artifacts.json`:

```json
{"schema_version":"turnyard.artifact-claims/v1","artifacts":[{"id":"summary","text":"Task result."}]}
```

The claim supplies values for independent checking. Git file bytes come from the pinned commit, and non-Git files from a sealed copy. A replaceable verifier reads back PRs; the default uses the authenticated GitHub CLI on the supervisor host. Managed PR creation is not implemented yet. `candidate.deliverables` and `session complete` return hashes and locations for retrieval. Byte checks and object presence do not prove the business objective; `acceptance` and trusted checks remain part of review.

## Single-machine state and SQLite

The supervisor admits two tasks at a time by default; each session still runs only one task. Additional work waits in arrival order for up to 15 minutes. A managed child can pass ordinary work that cannot yet be admitted, avoiding a capacity deadlock with its parent. Queue timeout returns `CAPACITY_WAIT_TIMEOUT`; a task exceeding the host budget returns `CAPACITY_EXCEEDED`. A task allowed to delegate keeps a slot available for its child. Repository preparation and attachment intake have separate admission: at most two preparations run and eight wait by default; a full preparation queue returns `CAPACITY_EXCEEDED`. `daemon status` reports active and waiting tasks and preparations, plus reserved task resources. Set `TURNYARD_MAX_ACTIVE_TASKS`, `TURNYARD_MAX_ACTIVE_PREPARATIONS`, `TURNYARD_MAX_PENDING_PREPARATIONS`, `TURNYARD_MAX_ACTIVE_CPUS`, `TURNYARD_MAX_ACTIVE_MEMORY_MB`, and `TURNYARD_QUEUE_WAIT_SECONDS` before starting the supervisor. Zero CPU or memory budget disables that aggregate budget. Admission uses declared sandbox limits and does not prove the container runtime enforces them.

At startup the supervisor removes staged attachment batches that no persisted task references, then scans its state root at startup and every ten minutes. `daemon status` returns `state_bytes` and `state_warning`; by default, usage above 10 GiB produces a warning. `TURNYARD_STATE_WARN_MB` changes the threshold, and zero disables it. This is an alert, not a hard disk quota. See [state migration and retention](state-evolution.md) for the implemented schema path and remaining retention work.

Turnyard executes on one machine. SQLite stores sessions, state transitions, events, and output manifests, including short text outputs; Git repositories, checkpoint archives, and raw logs are files. The supervisor holds an exclusive lock on its state directory and can serve multiple sessions continuously. SQLite fits this boundary without a separate database service. How long completed records remain is an operator policy, independent of whether the supervisor stays running.

A separate scheduler may run independent Turnyard instances on different machines and collect their structured results. Each instance owns its state directory. Turnyard does not migrate a running session between machines, and multiple machines must not share one SQLite file over a network filesystem.

Ent supports several database dialects, but `store.OpenStore`, backup and restore, and versioned migrations currently depend on SQLite behavior. Swapping the DSN is not required for Turnyard's single-machine execution goal. Numbered SQL and checksums make schema changes reviewable; deployment still needs an explicit retention and backup policy.

References: [Appropriate Uses For SQLite](https://www.sqlite.org/whentouse.html) · [SQLite Over a Network](https://www.sqlite.org/useovernet.html)
