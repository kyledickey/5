# V5 Storage Backup and Recovery Rehearsal

Run this procedure only against operator-created isolated MySQL databases and
Redis instances. It is a verification harness, not permission to access or
change production.

## MySQL

Quack requires transactional InnoDB storage, `utf8mb4`, automated encrypted
backups, point-in-time recovery appropriate to the deployment, and a restore
rehearsal before release. Capture a content-minimizing manifest before backup:

```sh
DATABASE_DSN='source isolated DSN' go run ./apps/backend/cmd/quack-storage-verify mysql-capture \
  > quack-recovery-manifest.json
mysqldump --single-transaction --routines --triggers --hex-blob \
  --databases quack_v5 > quack-v5.sql
```

Create a clean isolated database, restore the dump with normal MySQL operator
tools, reconcile the schema, then verify through stdin:

```sh
mysql < quack-v5.sql
DATABASE_DSN='restored isolated DSN' go run ./apps/backend/cmd/quack-migrate
DATABASE_DSN='restored isolated DSN' go run ./apps/backend/cmd/quack-storage-verify mysql-verify \
  < quack-recovery-manifest.json
```

The manifest compares counts and deterministic digests for
guild settings/staff, complete template definitions, cases and their
event timelines, action executions/attempts, evidence/attachments,
notifications, appeals and their event/outbox state, optional modules, audit,
and v4 import ledgers. Verification also rejects duplicate guild case numbers,
action idempotency keys, and v4 source identities. After verification, repeat
one v4 source import and one already-completed action lookup; neither may create
new execution work. Destroy the isolated source, dump, manifest, and restored
target according to the operator's secure-data policy.

Schema creation is additive: it adds tables, columns, and indexes and never
rewrites IDs, case numbers, template snapshots, actions, appeals, evidence,
module rows, or audit history. A failed schema run must be handled with the
procedure in `docs/migrations.md`, never ad-hoc schema editing.

## Redis

Redis is cache/session/coordination state, not moderation history. Use a
dedicated v5 prefix and instance or database during v4 coexistence. Configure
persistence and replication according to the loss tolerance for authenticated
sessions and Discord interaction deduplication. Recovery may invalidate
sessions and rebuild command caches, but it must not cause a database action to
execute twice.

For an isolated persistence rehearsal, set a random namespace and token, write
the expiring probe, perform the operator-controlled Redis snapshot/restart or
failover, then verify and clean it up:

```sh
REDIS_URL='isolated Redis URL' QUACK_RECOVERY_NAMESPACE='rehearsal-id' \
  QUACK_RECOVERY_TOKEN='random token' go run ./apps/backend/cmd/quack-storage-verify redis-write

REDIS_URL='isolated Redis URL' QUACK_RECOVERY_NAMESPACE='rehearsal-id' \
  QUACK_RECOVERY_TOKEN='random token' go run ./apps/backend/cmd/quack-storage-verify redis-verify
```

The second command must use a newly opened client and deletes the probe after a
successful comparison. A missing probe is an expected recovery signal, not a
reason to recreate database action work. Expired OAuth/session/dedupe keys must
remain bounded by TTL and normal adapter cleanup.
