package moduleintegration

import (
	"context"
	"sync"
)

// lockGuildOperation serializes one family of guild operations before reading
// configuration. Each caller family owns its lock map; waiting is cancellable and
// other guilds remain independent. Locks live for the runtime lifetime so waiters
// can never acquire a detached replacement lock for the same guild.
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
