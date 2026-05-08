package httpx

import (
	"strconv"
	"testing"
	"time"
)

func wotOpts(depth int) webOfTrustOptions {
	return webOfTrustOptions{Enabled: true, Depth: depth}
}

func TestActiveViewersTouchAndSnapshot(t *testing.T) {
	v := newActiveViewers()
	now := time.Unix(1_700_000_000, 0)

	v.Touch("alice", wotOpts(3), now.Add(-9*time.Minute))
	v.Touch("bob", wotOpts(3), now.Add(-1*time.Minute))
	v.Touch("carol", wotOpts(3), now.Add(-30*time.Second))

	snap := v.Snapshot(now, 10*time.Minute)
	if len(snap) != 3 {
		t.Fatalf("Snapshot len = %d, want 3 (%v)", len(snap), snap)
	}
	if snap[0].Viewer != "carol" || snap[1].Viewer != "bob" || snap[2].Viewer != "alice" {
		t.Fatalf("Snapshot order viewers = [%s %s %s], want newest-first [carol bob alice]",
			snap[0].Viewer, snap[1].Viewer, snap[2].Viewer)
	}
}

func TestActiveViewersDistinctWoTKeysAreTrackedSeparately(t *testing.T) {
	v := newActiveViewers()
	now := time.Unix(1_700_000_000, 0)

	v.Touch("alice", wotOpts(1), now.Add(-2*time.Minute))
	v.Touch("alice", wotOpts(3), now.Add(-1*time.Minute))

	snap := v.Snapshot(now, 10*time.Minute)
	if len(snap) != 2 {
		t.Fatalf("Snapshot len = %d, want 2 distinct WoT entries (%v)", len(snap), snap)
	}
	depths := []int{snap[0].WoT.Depth, snap[1].WoT.Depth}
	if (depths[0] != 3 || depths[1] != 1) && (depths[0] != 1 || depths[1] != 3) {
		t.Fatalf("Snapshot depths = %v, want both 1 and 3", depths)
	}
}

func TestActiveViewersSnapshotEvictsStale(t *testing.T) {
	v := newActiveViewers()
	now := time.Unix(1_700_000_000, 0)

	v.Touch("stale", wotOpts(3), now.Add(-30*time.Minute))
	v.Touch("fresh", wotOpts(3), now.Add(-1*time.Minute))

	snap := v.Snapshot(now, 10*time.Minute)
	if len(snap) != 1 || snap[0].Viewer != "fresh" {
		t.Fatalf("Snapshot = %v, want [fresh]", snap)
	}
	if v.Len() != 1 {
		t.Fatalf("Len after Snapshot = %d, want 1 (stale should have been evicted)", v.Len())
	}
}

func TestActiveViewersTouchEmptyIsNoop(t *testing.T) {
	v := newActiveViewers()
	v.Touch("", wotOpts(3), time.Now())
	if v.Len() != 0 {
		t.Fatalf("Len after empty Touch = %d, want 0", v.Len())
	}
}

func TestActiveViewersHardCapDropsOldest(t *testing.T) {
	v := newActiveViewers()
	base := time.Unix(1_700_000_000, 0)

	// Insert a known oldest entry first so it is the deterministic eviction target.
	v.Touch("oldest", wotOpts(3), base)

	// Fill above the cap with progressively newer timestamps.
	for i := 0; i < activeViewersMaxLen+5; i++ {
		v.Touch("p"+strconv.Itoa(i), wotOpts(3), base.Add(time.Duration(i+1)*time.Second))
	}

	if v.Len() > activeViewersMaxLen {
		t.Fatalf("Len = %d, want <= %d", v.Len(), activeViewersMaxLen)
	}

	// Use a wide window so nothing is dropped for staleness — only cap-driven
	// eviction should be visible. The deterministic-oldest entry must be gone.
	snap := v.Snapshot(base.Add(time.Hour), time.Hour)
	for _, e := range snap {
		if e.Viewer == "oldest" {
			t.Fatalf("expected oldest entry to be evicted by hard cap, got snapshot len=%d", len(snap))
		}
	}
}

func TestActiveViewersTouchUpdatesLastSeen(t *testing.T) {
	v := newActiveViewers()
	now := time.Unix(1_700_000_000, 0)

	v.Touch("alice", wotOpts(3), now.Add(-30*time.Minute))
	if snap := v.Snapshot(now, 10*time.Minute); len(snap) != 0 {
		t.Fatalf("Snapshot before re-touch = %v, want empty", snap)
	}

	v.Touch("alice", wotOpts(3), now.Add(-1*time.Minute))
	snap := v.Snapshot(now, 10*time.Minute)
	if len(snap) != 1 || snap[0].Viewer != "alice" {
		t.Fatalf("Snapshot after re-touch = %v, want [alice]", snap)
	}
}
