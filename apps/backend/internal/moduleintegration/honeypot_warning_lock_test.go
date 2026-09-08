package moduleintegration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestHoneypotSetupAndCounterShareWarningLock checks runtime presentation paths
// use one guild gate while another guild and cancelled waiters remain independent.
func TestHoneypotSetupAndCounterShareWarningLock(t *testing.T) {
	runtime := &Runtime{}
	counter := &honeypotCounter{sharedLocks: &runtime.honeypotWarningLocks}
	release, err := lockHoneypotWarning(context.Background(), &runtime.honeypotWarningLocks, "guild")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := lockHoneypotWarning(ctx, counter.sharedLocks, "guild"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("counter bypassed setup lock: %v", err)
	}
	other, err := lockHoneypotWarning(context.Background(), counter.sharedLocks, "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	var wg sync.WaitGroup
	var count int
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := lockHoneypotWarning(context.Background(), counter.sharedLocks, "guild")
			if err != nil {
				t.Error(err)
				return
			}
			defer unlock()
			count++
		}()
	}
	wg.Wait()
	if count != 20 {
		t.Fatalf("lost serialized updates: %d", count)
	}
}
