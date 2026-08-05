// Package eventbus is the in-process pub/sub used by V2 memory plugins.
// It is intentionally minimal: synchronous dispatch (callers choose to publish
// from goroutines), topic-filtered, ordered per subscriber.
package eventbus

import (
	"context"
	"log/slog"
	"sync"
)

// Event is the universal envelope. Topic routes to subscribers; Payload is
// any typed value the publisher/subscriber pair agrees on.
type Event struct {
	Topic   string
	Payload any
}

// Handler processes one event. ctx carries cancellation; returning an error
// logs the failure but does NOT stop other subscribers.
type Handler func(ctx context.Context, e Event) error

// Bus is a single-process event bus. Safe for concurrent use.
type Bus struct {
	mu   sync.RWMutex
	subs map[string][]Handler
	log  *slog.Logger
}

// New constructs a Bus. nil logger falls back to slog.Default().
func New(log *slog.Logger) *Bus {
	if log == nil {
		log = slog.Default()
	}
	return &Bus{subs: make(map[string][]Handler), log: log}
}

// Subscribe registers h for the given topic. Returns an unsubscribe function.
// Subscribers are invoked in registration order.
func (b *Bus) Subscribe(topic string, h Handler) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], h)
	idx := len(b.subs[topic]) - 1
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.subs[topic]; ok && idx < len(subs) {
			b.subs[topic] = append(subs[:idx], subs[idx+1:]...)
		}
	}
}

// Publish dispatches e to all subscribers of e.Topic in order.
// Subscribers that panic are isolated; their error is logged.
func (b *Bus) Publish(ctx context.Context, e Event) {
	b.mu.RLock()
	subs := make([]Handler, len(b.subs[e.Topic]))
	copy(subs, b.subs[e.Topic])
	b.mu.RUnlock()

	for _, h := range subs {
		if err := safeInvoke(ctx, h, e); err != nil {
			b.log.Warn("eventbus subscriber error", "topic", e.Topic, "error", err)
		}
	}
}

// Topic constants — canonical strings used by plugins to subscribe.
const (
	TopicTurnStart          = "memory.turn.start"
	TopicTurnEnd            = "memory.turn.end"
	TopicNodeCreated        = "memory.node.created"
	TopicNodeUpdated        = "memory.node.updated"
	TopicNodeAccessed       = "memory.node.accessed"
	TopicEdgeCreated        = "memory.edge.created"
	TopicConsolidationStart = "memory.consolidation.start"
	TopicConsolidationDone  = "memory.consolidation.done"
	TopicUserActive         = "memory.user.active"
	TopicUserInactive       = "memory.user.inactive"
)

func safeInvoke(ctx context.Context, h Handler, e Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = recoverError{r}
		}
	}()
	return h(ctx, e)
}

type recoverError struct{ v any }

func (e recoverError) Error() string { return "eventbus handler panic" }
