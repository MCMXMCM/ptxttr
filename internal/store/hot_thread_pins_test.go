package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPinHotThreadCapsEventsAndEvictsOldestRoots(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	events := make([]string, 0, 120)
	for i := 0; i < 120; i++ {
		events = append(events, fmt.Sprintf("event-%03d", i))
	}
	if err := st.PinHotThread(ctx, "root-a", events, time.Hour, 2); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_pins WHERE reason = ?`, hotThreadPinReasonPrefix+"root-a").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != hotThreadMaxEvents {
		t.Fatalf("root-a pins = %d, want %d", count, hotThreadMaxEvents)
	}

	if _, err := st.db.ExecContext(ctx, `UPDATE hot_thread_pins SET pinned_at = 1 WHERE root_id = 'root-a'`); err != nil {
		t.Fatal(err)
	}
	if err := st.PinHotThread(ctx, "root-b", []string{"b"}, time.Hour, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE hot_thread_pins SET pinned_at = 2 WHERE root_id = 'root-b'`); err != nil {
		t.Fatal(err)
	}
	if err := st.PinHotThread(ctx, "root-c", []string{"c"}, time.Hour, 2); err != nil {
		t.Fatal(err)
	}

	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hot_thread_pins`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("hot roots = %d, want 2", count)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_pins WHERE reason = ?`, hotThreadPinReasonPrefix+"root-a").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("evicted root-a retained %d pins", count)
	}
}
