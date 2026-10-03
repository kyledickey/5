// Package store implements the persistence ports declared in internal/quack
// (ports.go) over GORM and Redis. MySQL is the production database; tests use
// SQLite through internal/testutil. Redis holds only auth sessions, OAuth
// state, and command-hash caches, and may be nil when those features are unused.
//
// There is one schema definition and no migration ledger. The domain structs in
// internal/quack/model carry the GORM tags and are passed straight to
// Create/Find/Model, and each optional module contributes its own tables through
// SchemaTypes(). InitializeSchema (schema.go) AutoMigrates that combined list
// under the MySQL advisory lock and then applies the invariants portable tags
// cannot express. It is additive and idempotent; see docs/migrations.md.
//
// Because the model structs are the query structs, a non-zero gorm "default"
// on one of them would let GORM substitute the database default for a
// deliberately zero Go value. Keep defaults out of those tags.
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
