package httpx

import (
	"testing"
	"time"

	"ptxt-nstr/internal/store"
)

func TestHydrationTouchCacheDebouncesAndAllowsPriorityEscalation(t *testing.T) {
	cache := newHydrationTouchCache(time.Minute, 32)
	now := time.Unix(1700000000, 0)
	targets := []store.HydrationTarget{
		{EntityType: "profile", EntityID: "alice", Priority: 1},
		{EntityType: "profile", EntityID: "alice", Priority: 3},
		{EntityType: "followGraph", EntityID: "alice", Priority: 2},
	}

	first := cache.filter(targets, now)
	if len(first) != 2 {
		t.Fatalf("expected two initial hydration touches, got %#v", first)
	}
	if first[0].EntityType != "followGraph" || first[1].EntityType != "profile" || first[1].Priority != 3 {
		t.Fatalf("unexpected coalesced hydration touches: %#v", first)
	}

	second := cache.filter(targets, now.Add(30*time.Second))
	if len(second) != 0 {
		t.Fatalf("expected debounce to suppress repeated touches, got %#v", second)
	}

	escalated := cache.filter([]store.HydrationTarget{
		{EntityType: "profile", EntityID: "alice", Priority: 4},
	}, now.Add(45*time.Second))
	if len(escalated) != 1 || escalated[0].Priority != 4 {
		t.Fatalf("expected higher priority touch to bypass debounce, got %#v", escalated)
	}

	expired := cache.filter([]store.HydrationTarget{
		{EntityType: "profile", EntityID: "alice", Priority: 1},
	}, now.Add(2*time.Minute))
	if len(expired) != 1 {
		t.Fatalf("expected expired touch to be retried, got %#v", expired)
	}
}
