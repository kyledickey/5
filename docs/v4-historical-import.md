# V4 historical moderation import

The explicit operator CLI exports actual v4 MySQL `cases` rows into
`quack-v4-case-jsonl/v1`, then imports them as historical v5 cases. It never
executes the old punishments or sends DMs. Imported cases appear in native user
history and case detail with the original action label, reason, timestamp,
moderator, and available context link. They do not count toward template escalation.

The six v4 numeric types map to `warning` (0), `ban` (1), `kick` (2), `unban` (3),
`timeout` (4), and `message_delete` (5). The original string case ID becomes the
stable `source_id`. The v4 SQL schema has no numeric case number, so extraction
leaves `case_number` zero and v5 allocates a guild number. Original identity and
action type remain in historical metadata. No expiration, moderator display name,
or departed-member status is invented when the source does not contain it.

## Disposable rehearsal first

Run Go commands from `apps/backend`. Use separate source and target databases;
never point v5 schema tooling at the legacy source. For the first rehearsal use
the checked-in legacy SQL schema and synthetic fixtures, then repeat against an
authorized restored real v4 backup. Tests do not establish live Discord acceptance.

The automated rehearsal creates independent source/target databases and covers
all six types, nullable links, Unicode/multiline reasons, stable extraction,
guild isolation, preview, idempotent application, rollback, preserved fields,
and absence of action executions, notifications, evidence and appeals:

```sh
go test ./internal/v4import ./cmd/quack-v4-import
go test ./internal/store -run '^TestV4SQLExportImportRehearsal$' -count=1
# Set QUACK_TEST_MYSQL_DSN explicitly to a disposable MySQL test server.
# This test creates and drops randomized quack_migration_* databases only.
go test ./internal/store -run '^TestMySQLV4SQLExportImportRehearsal$' -count=1
```

Source fixtures: `Legacy/SQL/cases.sql` and
`apps/backend/internal/v4import/testdata/legacy_cases.sql`. The older
`historical_cases.jsonl` fixture additionally covers optional transformed fields.

## Export and import one guild

1. Restore the legacy snapshot into an isolated source database. Prefer an account
   with SELECT permission only. Determine the legacy **Discord guild ID** and the
   matching target **v5 guild ULID** explicitly; they are different identifiers.
2. Initialize an empty target with `go run ./cmd/quack-migrate init`, supplying its
   isolated `DATABASE_DSN` explicitly. Current schema includes import ledgers;
   do not replay historical migrations. Existing unmarked prerelease databases
   require the separate adoption procedure in [migrations.md](migrations.md).
3. Provision the target guild through the normal v5 guild lifecycle in the test
   environment and read its `guilds.id` by matching `guilds.discord_guild_id`.
   Schema initialization alone does not create guilds; this CLI does not create
   or guess them. Keep any test bot isolated from the production bot.
4. Export into a new private file:

   ```sh
   V4_DATABASE_DSN='readonly:password@tcp(test-host:3306)/v4_snapshot' \
     go run ./cmd/quack-v4-import export \
     --legacy-guild 123456789012345678 --guild 01J40000000000000000000001 \
     --file /private/operator/guild.jsonl
   ```

   Export reads a repeatable, read-only SQL transaction, normalizes MySQL
   TIMESTAMP values in a UTC session, and orders by timestamp and original ID.
   It never reads `.env` or `DATABASE_DSN`. Output uses mode 0600 and refuses to
   overwrite an existing file. Failed row validation emits no partial export.
   Verify an independent checksum, for example `shasum -a 256` on the file.
5. Validate against the isolated target:

   ```sh
   DATABASE_DSN='operator:password@tcp(test-host:3306)/v5_rehearsal?parseTime=true' \
     go run ./cmd/quack-v4-import import --dry-run \
     --file /private/operator/guild.jsonl --source final-v4-snapshot \
     --guild 01J40000000000000000000001 --actor 123456789012345678
   ```

   Review counts, checksum, line classifications and case-number remaps. Reports
   also contain source IDs and target mappings; keep reports and JSONL private.
   Use a stable source name for subsequent exports from the same legacy database.
6. Run the same command without `--dry-run`; preserve its batch ID. Repeat it:
   every row must be already imported and zero cases created. Reconcile source
   counts and all six action types before considering any real cutover.
7. Inspect native history and case detail for active and departed members. Verify
   original content, context links and action labels, staff access, cross-guild
   denial, and that imported rows do not affect the next template level.

Malformed rows, unknown action types, or duplicate source IDs within a file abort
validation. Reusing an existing source identity with changed content is a hard
collision. Existing numeric case-number collisions in manually transformed JSONL
are remapped and reported. The format accepts optional departed/missing flags,
moderator display name and old action expiry; expired actions are only warnings,
never replayed. A source is bounded to 64 MiB, with each JSONL row below 2 MiB;
there is currently no automatic chunking. Oversized sources require a separate
bounded-export workflow before cutover, rather than truncation or skipped rows.

Required JSONL fields are `format`, `source_id`, `guild_id` (the target ULID),
`target_discord_user_id`, `reason`, `action_type`, and `created_at`. Preserve the
source snapshot and investigate rejected rows instead of silently dropping them.

## Rollback and module setup

An untouched historical batch can be removed explicitly:

```sh
DATABASE_DSN='operator supplied isolated target DSN' \
  go run ./cmd/quack-v4-import rollback \
  --guild 01J40000000000000000000001 --batch v4-BATCH-ID --actor 123456789012345678
```

Rollback refuses cases with dependent v5 actions, notifications, appeals or
evidence; import audit history remains. Preserve a target backup before applying
real history. The source database is unchanged throughout extraction/import.

Module settings translation is not wired to an operator CLI. Configure tickets,
honeypot, appeals and the single general-log destination through native `/setup`
in v5. This explicit resetup is permitted by interview Q71. Old ticket panels,
DM controls and module history are not migrated by this case-history tool.

## Controlled cutover

During rehearsal keep v4/v5 databases, Redis namespaces, processes, application
IDs and command scopes separate. `check-scope` only validates the lists supplied
by the operator; it does not fetch Discord registrations or remove commands:

```sh
go run ./cmd/quack-v4-import check-scope --v4 ticket --v5 case
go run ./cmd/quack-v4-import check-scope \
  --v4 warn,timeout,kick,ban --v5 case --after-migration
# The second example intentionally fails because direct v4 commands remain.
```

At the authorized cutover, disable v4 synchronization and remove its direct
moderation commands. Verify actual Discord registrations independently. A service
rollback uses the unchanged v4 snapshot/process and disables v5 synchronization;
never point either version at the other's schema. No import command starts a bot.
