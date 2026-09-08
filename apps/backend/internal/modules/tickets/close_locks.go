package tickets

import (
	"context"
	"sync"
)

// ticketCloseLocks serializes one ticket's external close pipeline in the single
// bot process. Different tickets proceed independently and idle entries are removed.
type ticketCloseLocks struct {
	mu      sync.Mutex
	entries map[string]*ticketCloseGate
}

// ticketCloseGate counts holders and waiters so cancellation cannot detach a lock
// still in use and accidentally permit a second pipeline for the same ticket.
type ticketCloseGate struct {
	token      chan struct{}
	references int
}

// acquire waits with cancellation, returning a release function only on success.
func (l *ticketCloseLocks) acquire(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*ticketCloseGate)
	}
	gate := l.entries[key]
	if gate == nil {
		gate = &ticketCloseGate{token: make(chan struct{}, 1)}
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
