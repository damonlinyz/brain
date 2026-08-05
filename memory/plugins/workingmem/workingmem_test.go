package workingmem

import (
	"context"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

func TestPutAndGet_Roundtrip(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	ctx := context.Background()
	uid := uuid.New()
	slot := Slot{UserID: uid, Key: "topic"}

	p.Put(ctx, slot, "flight booking", 0.8)
	entry, ok := p.Get(ctx, slot)
	if !ok {
		t.Fatal("expected hit")
	}
	if entry.Value != "flight booking" {
		t.Fatalf("value mismatch: %s", entry.Value)
	}
	if entry.Weight != 0.8 {
		t.Fatalf("weight mismatch: %f", entry.Weight)
	}
	_ = types.SalienceNormal
}

func TestLRUEviction_WhenCapacityHit(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"capacity": 3})
	ctx := context.Background()
	uid := uuid.New()

	for i := 0; i < 5; i++ {
		p.Put(ctx, Slot{UserID: uid, Key: string(rune('a' + i))}, "v", 0.5)
	}
	items := p.List(ctx, uid)
	if len(items) != 3 {
		t.Fatalf("expected capacity 3, got %d", len(items))
	}
}

func TestTTLExpiry(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"ttlSeconds": 1})
	ctx := context.Background()
	uid := uuid.New()
	slot := Slot{UserID: uid, Key: "x"}

	p.Put(ctx, slot, "ephemeral", 0.5)
	time.Sleep(1500 * time.Millisecond)
	_, ok := p.Get(ctx, slot)
	if ok {
		t.Fatal("expected TTL expiry")
	}
}

func TestLRU_MoveToFrontOnGet(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"capacity": 2})
	ctx := context.Background()
	uid := uuid.New()

	slotA := Slot{UserID: uid, Key: "a"}
	slotB := Slot{UserID: uid, Key: "b"}
	p.Put(ctx, slotA, "v1", 0.5)
	p.Put(ctx, slotB, "v2", 0.5)

	// Touch A so B becomes LRU
	_, _ = p.Get(ctx, slotA)
	// Add C → evict B (LRU)
	p.Put(ctx, Slot{UserID: uid, Key: "c"}, "v3", 0.5)

	if _, ok := p.Get(ctx, slotA); !ok {
		t.Fatal("A should still be present (was touched)")
	}
	if _, ok := p.Get(ctx, slotB); ok {
		t.Fatal("B should have been evicted")
	}
}

func TestClear_RemovesAllForUser(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	ctx := context.Background()
	uid1 := uuid.New()
	uid2 := uuid.New()

	p.Put(ctx, Slot{UserID: uid1, Key: "a"}, "v", 0.5)
	p.Put(ctx, Slot{UserID: uid2, Key: "a"}, "v", 0.5)

	n := p.Clear(ctx, uid1)
	if n != 1 {
		t.Fatalf("expected 1 cleared, got %d", n)
	}
	if len(p.List(ctx, uid1)) != 0 {
		t.Fatal("uid1 should be empty")
	}
	if len(p.List(ctx, uid2)) != 1 {
		t.Fatal("uid2 should be untouched")
	}
}
