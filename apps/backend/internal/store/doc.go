// Package store implements the persistence ports declared in internal/quack
// (ports.go) over GORM and Redis. MySQL is the production database; tests use
// SQLite through internal/testutil. Redis holds only auth sessions, OAuth
// state, and command-hash caches, and may be nil when those features are unused.
//
// Schema is managed by two separate mechanisms that must not be conflated:
//
//   - InitializeSchema runs on every startup (Store.Migrate). It AutoMigrates
//     the *Record types plus the runtime-only tables, installs the constraints
//     in schema_constraints.go, and marks the database with quack_current_schema.
//     It refuses an unmarked database that already has tables.
//   - The frozen migration ledger (migration_0001 .. migration_0410) is replayed
//     only by the quack-migrate CLI (MigrateLegacySchema, AdoptCurrentSchema).
//     Each migration's Go source is embedded and hashed into its ledger row, so
//     editing any migration_*.go file changes its checksum and is a breaking
//     change for already-migrated databases.
//
// The domain structs in internal/quack/model are the GORM query structs: this
// package passes them straight to Create/Find/Model and relies on GORM's default
// column naming. The *Record types in schema_records.go carry the column, size,
// and index tags and exist only for DDL (AutoMigrate). Every Record must stay
// field-for-field in sync with its model; schema_records_drift_test.go enforces
// that, and documents the three template records that deliberately keep retired
// compatibility columns.
//
// Audit rows are append-only: New installs a GORM callback that rejects UPDATE
// and DELETE on audit_log_entries, and createAuditLogEntry writes the audit row
// and its mirror-delivery receipt inside the caller's transaction so a decision
// and its evidence commit together. Worker-owned rows (action executions, case
// and appeal notifications) are claimed with a lease token and expiry; every
// completion is fenced on that token so a stale worker cannot overwrite a newer
// claim. An expired lease proves only that a worker stopped reporting, never
// that the external Discord request failed, so unsafe recoveries fail closed
// into the staff review queue instead of repeating the request.
package store
