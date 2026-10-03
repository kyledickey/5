package main

import (
	"strings"
	"testing"
)

func TestRunRejectsArguments(t *testing.T) {
	for _, args := range [][]string{{"up"}, {"sideways"}, {"up", "down"}} {
		err := run(args)
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("run(%v) expected usage error, got %v", args, err)
		}
	}
}

// TestRunRequiresDSN verifies the command reaches connection validation before
// touching a database.
func TestRunRequiresDSN(t *testing.T) {
	t.Setenv("DATABASE_DSN", "")
	if err := run(nil); err == nil || err.Error() != "DATABASE_DSN is required" {
		t.Fatalf("expected DSN validation, got %v", err)
	}
}
