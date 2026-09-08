package store

import (
	"context"
	"reflect"
	"testing"

	"gorm.io/gorm"
)

// TestAdoptCurrentSchemaPreservesHistory exercises the supported transition on
// both database engines and compares stored history before and after startup.
func TestAdoptCurrentSchemaPreservesHistory(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			if err := runMigrations(db, registeredMigrations()[:1]); err != nil {
				t.Fatal(err)
			}
			insertRepresentativeHistory(t, db)
			if err := runMigrations(db, registeredMigrations()); err != nil {
				t.Fatal(err)
			}
			repository := New(db, nil)
			before, err := repository.BuildRecoveryManifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := repository.AdoptCurrentSchema(); err != nil {
				t.Fatal(err)
			}
			if err := repository.Migrate(); err != nil {
				t.Fatalf("startup after adoption failed: %v", err)
			}
			after, err := repository.BuildRecoveryManifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			delete(before.Tables, "quack_schema_migrations")
			delete(after.Tables, "quack_current_schema")
			if !reflect.DeepEqual(before.Tables, after.Tables) || !reflect.DeepEqual(before.GuildCaseHighWater, after.GuildCaseHighWater) {
				t.Fatal("adoption changed preserved history or case numbering")
			}
			if !db.Migrator().HasTable(&schemaMigration{}) {
				t.Fatal("adoption deleted the old ledger")
			}
		})
	}
}

// TestAdoptionRejectsIncompleteHistory ensures the new marker cannot hide a
// partial or edited migration sequence from startup validation.
func TestAdoptionRejectsIncompleteHistory(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := runMigrations(db, registeredMigrations()[:1]); err != nil {
		t.Fatal(err)
	}
	if err := New(db, nil).AdoptCurrentSchema(); err == nil || db.Migrator().HasTable(&currentSchema{}) {
		t.Fatal("partial history was adopted")
	}
}
