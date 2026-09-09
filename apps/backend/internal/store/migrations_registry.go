package store

import _ "embed"

// migration0001Source binds migration 0001's executable logic and frozen schema to its ledger checksum.
//
//go:embed migration_0001_initial_v5_schema.go
var migration0001Source string

// migration0002Source binds migration 0002's executable compatibility logic to its ledger checksum.
//
//go:embed migration_0002_simplify_template_model.go
var migration0002Source string

// migration0003Source binds migration 0003's case validity compatibility logic to its ledger checksum.
//
//go:embed migration_0003_case_validity.go
var migration0003Source string

// migration0004Source binds migration 0004's guild setup schema and reversible compatibility logic to its ledger checksum.
//
//go:embed migration_0004_guild_settings.go
var migration0004Source string

// migration0005Source binds migration 0005's core moderation runtime schema to its ledger checksum.
//
//go:embed migration_0005_core_moderation.go
var migration0005Source string

// migration0006Source binds logical module migration 0100 to the central ledger checksum.
//
//go:embed migration_0006_optional_module_registry.go
var migration0006Source string

// migration0007Source binds logical module migration 0110 to the central ledger checksum.
//
//go:embed migration_0007_ticket_lifecycle.go
var migration0007Source string

// migration0008Source binds logical module migration 0300 to the central ledger checksum.
//
//go:embed migration_0008_honeypot_triggers.go
var migration0008Source string

// migration0012Source binds the additive appeal review setting to the central ledger checksum.
//
//go:embed migration_0012_appeal_review_reasons.go
var migration0012Source string

// registeredMigrations returns the immutable ordered production migration registry.
func registeredMigrations() []migration {
	return []migration{
		migration0001InitialV5Schema(),
		migration0002SimplifyTemplateModel(),
		migration0003CaseValidity(),
		migration0004GuildSettings(),
		migration0005CoreModeration(),
		migration0006OptionalModuleRegistry(),
		migration0007TicketLifecycle(),
		migration0008HoneypotTriggers(),
		migration0200Appeals(9),
		migration0400V4HistoricalImport(10),
		migration0410FinalStorageConstraints(11),
		migration0012AppealReviewReasons(),
	}
}
