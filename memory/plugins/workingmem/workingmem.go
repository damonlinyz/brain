// Package workingmem implements the WorkingMemory plugin — a short-lived,
// in-process map keyed by (user_id, slot). Capacity bound; LRU eviction when
// full; entries expire after TTL.
package workingmem

import (
	"context"
	"container/list"
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Slot is the key WorkingMemory indexes by. The same user can hold many slots.
type Slot struct {
	UserID uuid.UUID
	Key    string // caller-chosen (e.g. "current_topic", "pending_task")
}

// Entry is one working-memory item.
type Entry struct {
	Value     string
	Weight    float64
	CreatedAt time.Time
	LastTouch time.Time
}

// Plugin is the WorkingMemory. Safe for concurrent use.
type Plugin struct {
	mu       sync.Mutex
	items    map[Slot]*list.Element
	lru      *list.List
	capacity int
	ttl      time.Duration
}

// Defaults
const (
	DefaultCapacity = 9
	DefaultTTL      = 5 * time.Minute
)

func New() *Plugin {
	return &Plugin{
		items:    make(map[Slot]*list.Element),
		lru:      list.New(),
		capacity: DefaultCapacity,
		ttl:      DefaultTTL,
	}
}

func (p *Plugin) Name() string                   { return "WorkingMemory" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEdge }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["capacity"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.capacity = n
		}
	}
	if v, ok := cfg["ttlSeconds"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.ttl = time.Duration(n) * time.Second
		}
	}
	if v, ok := cfg["ttl"]; ok {
		if s, err := toString(v); err == nil {
			if d, err := time.ParseDuration(s); err == nil {
				p.ttl = d
			}
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// Put adds or replaces an entry. Evicts LRU + expired entries first.
func (p *Plugin) Put(ctx context.Context, slot Slot, value string, weight float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictExpiredLocked(time.Now())
	if e, ok := p.items[slot]; ok {
		p.lru.MoveToFront(e)
		entry := e.Value.(Entry)
		entry.Value = value
		entry.Weight = weight
		entry.LastTouch = time.Now()
		e.Value = entry
		return
	}
	if p.lru.Len() >= p.capacity {
		p.evictLRILocked()
	}
	entry := Entry{
		Value:     value,
		Weight:    weight,
		CreatedAt: time.Now(),
		LastTouch: time.Now(),
	}
	p.items[slot] = p.lru.PushFront(entry)
}

// Get returns the entry for slot, refreshing LRU and skipping expired entries.
func (p *Plugin) Get(ctx context.Context, slot Slot) (Entry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.evictExpiredLocked(now)
	e, ok := p.items[slot]
	if !ok {
		return Entry{}, false
	}
	entry := e.Value.(Entry)
	if now.Sub(entry.LastTouch) > p.ttl {
		p.removeElementLocked(e)
		return Entry{}, false
	}
	p.lru.MoveToFront(e)
	return entry, true
}

// List returns all live entries for a user (no LRU mutation).
func (p *Plugin) List(ctx context.Context, userID uuid.UUID) []Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.evictExpiredLocked(now)
	out := []Entry{}
	for slot, e := range p.items {
		if slot.UserID != userID {
			continue
		}
		entry := e.Value.(Entry)
		if now.Sub(entry.LastTouch) > p.ttl {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// Clear removes all entries for a user.
func (p *Plugin) Clear(ctx context.Context, userID uuid.UUID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for slot, e := range p.items {
		if slot.UserID == userID {
			p.removeElementLocked(e)
			delete(p.items, slot)
			n++
		}
	}
	return n
}

func (p *Plugin) evictExpiredLocked(now time.Time) {
	for slot, e := range p.items {
		entry := e.Value.(Entry)
		if now.Sub(entry.LastTouch) > p.ttl {
			p.removeElementLocked(e)
			delete(p.items, slot)
		}
	}
}

func (p *Plugin) evictLRILocked() {
	back := p.lru.Back()
	if back == nil {
		return
	}
	entry := back.Value.(Entry)
	_ = entry
	// Reconstruct slot by scanning — O(n) but capacity is small.
	for slot, e := range p.items {
		if e == back {
			delete(p.items, slot)
			break
		}
	}
	p.lru.Remove(back)
}

func (p *Plugin) removeElementLocked(e *list.Element) {
	p.lru.Remove(e)
	for slot, elem := range p.items {
		if elem == e {
			delete(p.items, slot)
			return
		}
	}
}

func toInt(v any) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case float64:
		return int(x), nil
	}
	return 0, errInvalid
}

func toString(v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	return "", errInvalid
}

var errInvalid = &pluginError{"workingmem: invalid config value"}

type pluginError struct{ msg string }

func (e *pluginError) Error() string { return e.msg }
