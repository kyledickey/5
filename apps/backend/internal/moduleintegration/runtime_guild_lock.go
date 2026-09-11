package moduleintegration

import (
	"context"
	"sync"
)

// lockGuildOperation serializes one family of guild operations (for example
// honeypot setup and warning refresh, which share Runtime.honeypotWarningLocks)
// before configuration is read. Waiting honours ctx; other guilds are
// unaffected. Locks live for the Runtime's lifetime so a waiter can never
// acquire a detached replacement lock for the same guild. The returned func
// releases the lock and must be called exactly once.
func lockGuildOperation(ctx context.Context, locks *sync.Map, guildID string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	candidate := make(chan struct{}, 1)
	candidate <- struct{}{}
	value, _ := locks.LoadOrStore(guildID, candidate)
	gate := value.(chan struct{})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-gate:
		if err := ctx.Err(); err != nil {
			gate <- struct{}{}
			return nil, err
		}
		return func() { gate <- struct{}{} }, nil
	}
}
