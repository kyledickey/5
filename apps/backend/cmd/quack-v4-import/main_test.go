package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckScopeRejectsCollisionAndPostMigrationDirectCommands(t *testing.T) {
	if err := checkScope([]string{"--v4", "case,warn", "--v5", "case"}); err == nil {
		t.Fatal("expected command collision")
	}
	if err := checkScope([]string{"--v4", "warn", "--v5", "case", "--after-migration"}); err == nil {
		t.Fatal("expected legacy command rejection")
	}
	if err := checkScope([]string{"--v4", "ticket", "--v5", "case"}); err != nil {
		t.Fatalf("unexpected isolated scopes failure: %v", err)
	}
}

// TestExportRequiresExplicitSourceAndMapping ensures export cannot accidentally
// fall back to the target DATABASE_DSN or silently choose a guild.
func TestExportRequiresExplicitSourceAndMapping(t *testing.T) {
	t.Setenv("DATABASE_DSN", "must-not-be-opened")
	t.Setenv("V4_DATABASE_DSN", "")
	for _, args := range [][]string{{"export"}, {"export", "--legacy-guild", "3001", "--guild", "01J40000000000000000000001", "--file", filepath.Join(t.TempDir(), "cases.jsonl")}} {
		var output bytes.Buffer
		err := run(context.Background(), args, &output)
		if err == nil || (!strings.Contains(err.Error(), "required") && !strings.Contains(err.Error(), "are required")) {
			t.Fatalf("missing explicit source/mapping was not rejected: %v", err)
		}
	}
}

// TestExportInvalidBoundsPrecedeSourceAccess rejects invalid paging even when
// no source DSN exists, leaving the requested destination untouched.
func TestExportInvalidBoundsPrecedeSourceAccess(t *testing.T) {
	t.Setenv("V4_DATABASE_DSN", "")
	for _, bounds := range [][]string{{"--limit", "-1"}, {"--limit", "100001"}, {"--offset", "1"}, {"--limit", "2", "--offset", "-1"}} {
		args := append([]string{"export", "--legacy-guild", "3001", "--guild", "01J40000000000000000000001", "--file", filepath.Join(t.TempDir(), "page.jsonl")}, bounds...)
		err := run(context.Background(), args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "export requires --limit") {
			t.Fatalf("%v: %v", bounds, err)
		}
	}
}
