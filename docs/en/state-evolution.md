# State migration and retention proposal

[Documentation index](../../README.md) · [中文](../zh/state-evolution.md)

This is a proposal for later implementation. The existing CLI already provides offline backup, verification, restore, and backup-verified pruning of individual terminal sessions. Automatic retention and versioned database migrations are not implemented.

## Database migration

1. Baseline the current Ent schema with reviewed, versioned migration files, checksums, and fixtures from older databases. Stop applying unreviewed schema changes to production state on every `OpenStore`.
2. At startup, accept only known database versions. Apply migrations in order, recording each version in the same SQLite transaction. Reject a database newer than the binary or missing a migration.
3. Hold the exclusive state lock and create a verified full backup before upgrading. Preserve it on failure. Rollback restores the full backup into a new state directory rather than assuming reverse SQL can restore data.
4. Version external JSON inputs separately from the internal database schema.

Acceptance: old database copies retain sessions, candidates, and checkpoints; reopening does not duplicate work; injected migration failure remains recoverable; an older binary rejects a newer database; a second supervisor cannot migrate concurrently.

## Retention and capacity

Only the host operator sets retention. First provide a report of eligible sessions and bytes; add scheduled pruning only after that policy is reviewed. Only `completed` or `cancelled` sessions qualify. Active, paused, and unknown work, including managed children, remains. Prune child sessions before parents.

Keep the existing exclusive lock, verified backup, and per-session prune semantics. Record the session ID, terminal time, backup location, reclaimed bytes, and outcome. Account for logs, checkpoints, and attachments separately. If disk space becomes critical, reject new work while preserving review and recovery. The current implementation only warns about state usage; it has no hard disk quota or automatic deletion.

Acceptance: active and unknown work is excluded; backup drift blocks pruning; symlinks cannot widen the deletion boundary; a failed prune can be recovered from backup; crossing a threshold never deletes unbacked data.
