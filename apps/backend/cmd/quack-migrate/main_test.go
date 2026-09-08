package main

import (
	"strings"
	"testing"
)

func TestRunRejectsUnknownMigrationDirection(t *testing.T) {
	for _, args := range [][]string{nil, {"sideways"}, {"up", "down"}} {
		err := run(args)
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("run(%v) expected usage error, got %v", args, err)
		}
	}
}

// TestRunAcceptsExplicitSchemaOperations verifies command parsing before any
// connection, including the historical replay that normal startup cannot invoke.
func TestRunAcceptsExplicitSchemaOperations(t *testing.T) {
	t.Setenv("DATABASE_DSN", "")
	for _, operation := range []string{"init", "up", "adopt", "legacy-up", "down"} {
		if err := run([]string{operation}); err == nil || err.Error() != "DATABASE_DSN is required" {
			t.Fatalf("operation %q did not reach connection validation: %v", operation, err)
		}
	}
}
