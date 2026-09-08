package generallogging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
)

// ErrQueueFull reports deliberate general-log shedding under gateway pressure.
var ErrQueueFull = errors.New("general logging queue is full")

// DeliveryQueue is a bounded concurrent worker queue isolated from moderation execution.
type DeliveryQueue struct {
	service *Service
	events  chan Event
	mu      sync.RWMutex
	closed  bool
	wg      sync.WaitGroup
}

// NewDeliveryQueue starts bounded workers whose failures remain visible through module status.
func NewDeliveryQueue(ctx context.Context, service *Service, capacity, workers int) *DeliveryQueue {
	if capacity < 1 {
		capacity = 1000
	}
	if workers < 1 {
		workers = 1
	}
	queue := &DeliveryQueue{service: service, events: make(chan Event, capacity)}
	for range workers {
		queue.wg.Add(1)
		go func() {
			defer queue.wg.Done()
			for event := range queue.events {
				queue.process(ctx, event)
			}
		}()
	}
	return queue
}

// Submit enqueues without blocking Discord gateway handling and sheds when full.
func (q *DeliveryQueue) Submit(event Event) error {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return errors.New("general logging queue is closed")
	}
	select {
	case q.events <- cloneEvent(event):
		return nil
	default:
		return ErrQueueFull
	}
}

// cloneEvent copies mutable payload fields before handing them to another goroutine.
func cloneEvent(event Event) Event {
	event.Attachments = append([]AttachmentMetadata(nil), event.Attachments...)
	event.BeforeAttachments = append([]AttachmentMetadata(nil), event.BeforeAttachments...)
	event.EmbedTypes = append([]string(nil), event.EmbedTypes...)
	if event.Metadata != nil {
		metadata := make(map[string]string, len(event.Metadata))
		for key, value := range event.Metadata {
			metadata[key] = value
		}
		event.Metadata = metadata
	}
	return event
}

// Close drains accepted events and waits for workers without affecting other module lifecycles.
func (q *DeliveryQueue) Close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.events)
	}
	q.mu.Unlock()
	q.wg.Wait()
}

// process contains adapter failures within one job so a bad event cannot stop
// moderation or take down unrelated workers. Message contents are never logged.
func (q *DeliveryQueue) process(ctx context.Context, event Event) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(ctx, "General logging worker panicked", "guild_id", event.GuildID,
				"panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
		}
	}()
	if err := q.service.Handle(ctx, event); err != nil {
		if errors.Is(err, ErrDisabled) || errors.Is(err, ErrNoDestination) || (ctx.Err() != nil && errors.Is(err, ctx.Err())) {
			return
		}
		slog.ErrorContext(ctx, "General logging delivery failed", "guild_id", event.GuildID, "error_type", fmt.Sprintf("%T", err))
	}
}
