# Database Schema

Quack v5 has one schema definition and no migration ledger. The Go structs are
the schema: `internal/quack/model` carries the GORM column, size, and index tags
for the moderation core, and each optional module exports its own tables through
a `SchemaTypes()` function (`modules.SchemaTypes`, `tickets.SchemaTypes`,
`honeypot.SchemaTypes`).

`Store.InitializeSchema` (called by `Store.Migrate` on every startup) takes the
MySQL advisory lock, runs `AutoMigrate` over that combined type list, and then
applies the invariants that portable struct tags cannot express:

- at most one default level per case template
- at most one enforcement action per template level
- the composite read indexes for member case history, case evidence lookup,
  audit cursor paging, and action claim recovery

It is additive and idempotent. Rerunning it on an already-initialized database
is expected, and an interrupted run can simply be retried, because MySQL may
commit DDL even when a later statement in the same run fails.

## Applying the schema

Startup does this automatically. To prepare a database first:

```sh
DATABASE_DSN='...' go run ./apps/backend/cmd/quack-migrate
```

The command opens MySQL, creates or reconciles the schema, and exits.

## Changing the schema

Edit the struct. Add a column by adding a field; add an index by adding a tag.
`AutoMigrate` adds new tables, columns, and indexes on the next startup.

`AutoMigrate` never drops or renames anything, so a rename or a destructive
change is a manual operation: back up MySQL, apply the DDL yourself, and ship
the matching struct change in the same release.

Two rules protect the data:

- **Never rename a table or column.** Raw SQL in `internal/store` names columns
  directly (`storage_recovery.go`, `ops_health.go`, `audit_mirror.go`, the
  statistics aggregates), and `model.Case.Validity` is already pinned to the
  `status` column by a `gorm:"column:status"` override.
- **Do not put a non-zero `default:` on a model struct.** The store queries
  these structs directly, and GORM omits a zero-valued field that declares a
  default, so the database value would silently replace a deliberate zero (for
  example `CaseActionExecution.SafeForRetry = false` or an imported case's
  `TemplateVersion = 0`).

Backup, restore, and coexistence procedures live in `storage-recovery.md` and
`v4-historical-import.md`.
