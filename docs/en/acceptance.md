# Task acceptance boundaries

[User guide](README.md) · [中文](../zh/acceptance.md)

Turnyard runs the commands named by `Work.checks` against a fixed candidate and verifies outputs declared in `Work.deliverables`. It reads Git files from the candidate commit and other files from sealed copies; images also receive a file-header check, short text is checked for format and digest, and pull requests are read back for remote state and candidate head. A task needs at least one check or deliverable and becomes `verified` only after every declared machine condition passes. `Work.acceptance` is goal text for the agent and reviewer; Turnyard does not evaluate each sentence automatically. Compilation, tests, output presence, and pull-request readback do not by themselves establish that a business requirement is met.

## Independent acceptance code

Place acceptance code in a separate Git repository at a pinned commit. Register it in `Session.repositories`, give the product repository `write` scope and the acceptance repository `read` scope, then run a trusted check such as:

```json
{
  "id": "independent-acceptance",
  "argv": ["/workspace/oracle/verify", "/workspace/product"],
  "repositories": ["product", "oracle"],
  "timeout_seconds": 600
}
```

The agent can only read `oracle`; checks mount both repositories read-only. The pinned acceptance commit is part of the session repository version vector. A check result records its ID, `spec_digest` of its definition, exit code, timeout status, and raw log path. Prefer assertions against public interfaces or the built artifact, including success, denial, and relevant failure paths.

The task author or reviewer should maintain and pin this repository before agent execution. Passing it still leaves uncoded requirements for human review. Live providers, browsers, deployments, and merge state need their own evidence. Tests kept solely in an agent-writable product repository are useful but do not count as independent acceptance.
