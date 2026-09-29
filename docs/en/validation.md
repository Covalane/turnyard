# Validation scope and reproduction

[Guide](README.md) · [简体中文](../zh/validation.md)

This page summarizes verified behavior, failures, and reproduction steps. Live model, OCI, and external-service tests require an operator-provided image, credentials, and permissions. Passing the default Go suite does not mean those optional paths ran.

Historical runs took place on September 26–29, 2026, across macOS Apple Silicon, Linux/arm64 inside Docker Desktop, Apple `container`, and Podman. Results came from different source and image revisions; they do not add up to one release acceptance run. Raw invocation logs, temporary databases, and model outputs remain in an ignored state directory on the execution machine. **They are not published with this repository, so the historical runs cannot be independently audited from this page alone.**

## Verified paths

| Path | Historical result and limit |
| --- | --- |
| Go checks and public CI | Unit tests, race, Vet, Staticcheck, Govulncheck, Ent regeneration, and Linux x64 image build passed at the recorded revisions. Public CI does not call cloud models. |
| Multi-repository execution | Docker and Apple `container` OpenCode cloud-model runs completed four sequential tasks across two repositories, with feature commits, native-session continuation, human input, checkpoint recovery, and output manifests. |
| Agent and capability injection | OpenCode, Kimi, Claude Code, and Codex CLI passed single-task Skill/MCP and executable-tool paths; native events showed tool calls. This is not a full multi-task matrix for all four runtimes. |
| Managed delegation | Docker `model-only` runs covered parent-to-child tasks, candidate checks, and read-only handoff, plus `unknown` state, reconciliation, and retry after supervisor interruption. Recovery was tested on one machine. |
| Remote Git | Temporary bare-repository tests covered pinned clone, idempotent publication, conflicts, and partial success. A private GitHub run completed nine authorization-system tasks, published feature branches, and confirmed remote readback. Private repository names, PR URLs, and commit IDs are not public interfaces or test defaults. |
| Outputs and attachments | Go tests cover text, PNG, Git files, sealed non-Git files, PR readback, connector downloads, and upload reconciliation. Real Docker runs covered Git file plus text and attachment-to-file delivery without a repository. |
| Tool gateway | Docker `model-only` runs covered `find_tools`, `call_tool`, model-assisted search, authorization denial, and separation of agent and tool credentials. Apple `container`, Podman, and gVisor each received single-task checks. |
| Structured human input | A Docker + OpenCode run used `turnyard_input/request_input` to enter `needs_input` and resumed the same native session after a reply, finishing a two-repository task. Other runtimes and sandboxes were not all retested after this change. |

During remote Git validation, one cloud-model usage-limit 429 was recorded as `unknown / AGENT_FAILED`. It succeeded only after checkpoint reconciliation, a credential change, and retry of the same task; the failed invocation was not counted. The two related sessions had nine verified tasks, 12 invocation records, and 122 lifecycle events. One supervisor-interrupted invocation lacks a raw log, so no complete per-invocation transcript exists. Logs also exclude the cloud provider's internal reasoning. Later direct Codex edits and commit-message cleanup in the private validation project are outside Turnyard's verified tasks.

## Sandbox and external-service limits

- Real Docker `model-only` probes covered model-gateway reachability, blocked direct agent egress, inability of an ordinary bridge container to use the gateway, credential redaction, and timeout cleanup. Regular networking allows broader egress; host administrators can inspect gateway credentials.
- gVisor on nested Linux with standard cgroup v2 passed resource-limit checks, boundary probes, and one cloud-model task. Parent/child delegation in that setup stopped twice while the parent connected to the model; **it did not pass**. Earlier non-root runs using `--ignore-cgroups` cannot be combined with standard-cgroup checks into one deployment proof.
- Apple `container` passed a single tool-gateway task under default networking; this does not establish Docker's strict network boundary. Podman rootful + VFS passed single tasks. Its rootless container smoke test passed, but the full task failed because the nested environment did not delegate cgroup controllers. A macOS Podman VM failed to start and was not counted.
- A real OSS test uploaded an object and read it back twice with matching SHA-256. Deletion returned `AccessDenied`, so **the full E2E failed and the test object still needs cleanup by an authorized account**. No dedicated RAM key was created. This does not establish idempotency for real external-write tools.
- Private-repository validation established clone and feature-branch push, not protected-branch behavior, PR merging, or every remote-timeout case. The chatroom example validates routing, not authentication or production-grade private-message confidentiality.

## Reproduce

Base checks need no cloud model:

```sh
go generate ./internal/store
go test -race ./...
go vet ./...
go test -tags integration -run '^$' ./test/...
```

The last command checks compilation with the `integration` tag only; **it is not a live agent, OCI, or OSS acceptance run**.

Live tests are opt-in. Set `TURNYARD_E2E_PROVIDER` and `TURNYARD_E2E_MODEL`, and set `TURNYARD_E2E_CREDENTIAL_ENV` to the name of an existing credential variable before running, for example:

```sh
TURNYARD_E2E=1 go test -tags integration -run TestMultiRepoWorkflow -v -count=1 -timeout 25m ./test/e2e
TURNYARD_DELEGATION_E2E=1 TURNYARD_E2E_BACKEND=docker go test -tags integration -run TestManagedDelegation -v -count=1 -timeout 15m ./test/e2e
TURNYARD_DELEGATION_RECOVERY_E2E=1 TURNYARD_E2E_BACKEND=docker go test -tags integration -run TestDelegationCrashRecovery -v -count=1 -timeout 15m ./test/e2e
```

Use `TURNYARD_E2E_RUNTIME`, `TURNYARD_E2E_BACKEND`, and `TURNYARD_E2E_IMAGE` to select a path. Recovery tests also require `TURNYARD_DELEGATION_CHILD_PROVIDER`, `TURNYARD_DELEGATION_CHILD_MODEL`, and `TURNYARD_DELEGATION_CHILD_CREDENTIAL_ENV`. Chatroom tests need `TURNYARD_CHAT_PROVIDER`, `TURNYARD_CHAT_MODEL`, and `TURNYARD_CHAT_CREDENTIAL_ENV`; model ranking needs `TURNYARD_MATCHER_ENDPOINT`, `TURNYARD_MATCHER_MODEL`, and `TURNYARD_MATCHER_CREDENTIAL_ENV`; PR readback needs `TURNYARD_GITHUB_PR_URL` and `TURNYARD_GITHUB_REPOSITORY_URL`. Prepare the sandbox image and required permissions first. Keep failed runs distinct from skipped tests and successful validation.
