package tickets

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestCloseLockCancellationAndIndependentTickets verifies a waiting request can
// expire without blocking unrelated tickets or leaking lock bookkeeping.
func TestCloseLockCancellationAndIndependentTickets(t *testing.T) {
	var locks ticketCloseLocks
	release, err := locks.acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := locks.acquire(ctx, "first"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not cancel: %v", err)
	}
	other, err := locks.acquire(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	if len(locks.entries) != 0 {
		t.Fatal("idle close lock retained")
	}
	again, err := locks.acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	again()
}
