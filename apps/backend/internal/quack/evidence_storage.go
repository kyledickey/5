package quack

import (
	"context"
	"sync"
	"time"
)

// captureStorage makes one bounded repair attempt per attachment batch. Failure
// leaves metadata-only evidence with the existing explicit warning, never a new
// requirement to preserve files before moderation can proceed.
func (s *EvidenceService) captureStorage(ctx context.Context, discordGuildID, fallback string) string {
	if s == nil || s.store == nil {
		return fallback
	}
	repairCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	channelID, err := s.RepairDiscordGuildEvidenceChannel(repairCtx, discordGuildID)
	if err != nil {
		return ""
	}
	return channelID
}

// evidenceStorageLocks serializes lookup/create/receipt for each guild in this
// process. Idle entries are removed; uploads never hold this creation lock.
type evidenceStorageLocks struct {
	mu      sync.Mutex
	entries map[string]*evidenceStorageGate
}

// evidenceStorageGate counts callers so cancelled waiters cannot detach a gate
// still held by another repair.
type evidenceStorageGate struct {
	token      chan struct{}
	references int
}

// acquire returns a cancellation-aware release function for one guild repair.
func (l *evidenceStorageLocks) acquire(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*evidenceStorageGate)
	}
	gate := l.entries[key]
	if gate == nil {
		gate = &evidenceStorageGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		l.entries[key] = gate
	}
	gate.references++
	l.mu.Unlock()
	drop := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		gate.references--
		if gate.references == 0 {
			delete(l.entries, key)
		}
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-gate.token:
		if err := ctx.Err(); err != nil {
			gate.token <- struct{}{}
			drop()
			return nil, err
		}
		return func() { gate.token <- struct{}{}; drop() }, nil
	}
}
