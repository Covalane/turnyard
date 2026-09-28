# State migration and retention

[Documentation index](../../README.md) · [中文](../zh/state-evolution.md)

Turnyard provides offline backup, verification, restore, and backup-verified pruning of individual terminal sessions. SQLite schema changes now use versioned migrations. Automatic retention and a hard disk quota remain future work.

## Database migration

`internal/store/migrations/001_baseline.sql` freezes the current Ent schema. `OpenStore` no longer calls Ent automatic schema creation. On startup, it checks the recorded database version and SQL checksums, then applies only bundled migrations in order. A fresh database receives the baseline. A known unversioned database is backed up with SQLite `VACUUM INTO` and checked for integrity before additive legacy columns and `task_repositories` bases are adopted and version 1 is recorded in a transaction. Unknown schemas, newer or modified migration versions, and failed checks stop startup. Future upgrades from a recorded version also back up first and apply SQL and version records transactionally.

The supervisor holds the exclusive state lock before opening the database. A failed migration retains the original database and backup; rollback restores the full backup offline to a new directory, without relying on reverse SQL. Database versions are separate from external JSON `schemaVersion` values. Each later schema change needs a new numbered SQL file, an old-version fixture, and upgrade tests; published migration files must remain unchanged.

Current tests cover fresh and unversioned databases, legacy repository bases, repeated opens, unknown or modified versions, failure rollback, and agreement between the Ent schema and migration baseline. Large real historical databases and a future version-2 upgrade still need separate release rehearsal.

## Retention and capacity

Only the host operator sets retention. First provide a report of eligible sessions and bytes; add scheduled pruning only after that policy is reviewed. Only `completed` or `cancelled` sessions qualify. Active, paused, and unknown work, including managed children, remains. Prune child sessions before parents.

Keep the existing exclusive lock, verified backup, and per-session prune semantics. Record the session ID, terminal time, backup location, reclaimed bytes, and outcome. Account for logs, checkpoints, and attachments separately. If disk space becomes critical, reject new work while preserving review and recovery. The current implementation only warns about state usage; it has no hard disk quota or automatic deletion.

Acceptance: active and unknown work is excluded; backup drift blocks pruning; symlinks cannot widen the deletion boundary; a failed prune can be recovered from backup; crossing a threshold never deletes unbacked data.
