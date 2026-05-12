package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

	fnostr "fiatjaf.com/nostr"

	"ptxt-nstr/internal/nostrx"
)

func negentropyReferenceIDs(events []fnostr.Event, f fnostr.Filter) []string {
	matches := make([]fnostr.Event, 0, len(events))
	for _, ev := range events {
		if f.Matches(ev) {
			matches = append(matches, ev)
		}
	}
	slices.SortFunc(matches, func(a, b fnostr.Event) int {
		if a.CreatedAt != b.CreatedAt {
			if a.CreatedAt > b.CreatedAt {
				return -1
			}
			return 1
		}
		return strings.Compare(b.ID.Hex(), a.ID.Hex())
	})
	if f.Limit > 0 && len(matches) > f.Limit {
		matches = matches[:f.Limit]
	}
	out := make([]string, 0, len(matches))
	for _, ev := range matches {
		out = append(out, ev.ID.Hex())
	}
	sort.Strings(out)
	return out
}

func TestNegentropyQueryEventsKindsSince(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	defer func() { _ = st.Close() }()

	ev := nostrx.Event{
		ID:        "a" + strings.Repeat("1", 63),
		PubKey:    "b" + strings.Repeat("2", 63),
		CreatedAt: 100,
		Kind:      1,
		Content:   "hello",
		Sig:       strings.Repeat("3", 128),
	}
	if err := st.SaveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	n, err := st.NegentropyLocalMatchCount(ctx, fnostr.Filter{
		Kinds: []fnostr.Kind{1},
		Since: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count = %d want 1", n)
	}

	var ids []string
	for e := range st.NegentropyQueryEvents(ctx, fnostr.Filter{Kinds: []fnostr.Kind{1}, Since: 50}) {
		ids = append(ids, e.ID.Hex())
	}
	if len(ids) != 1 || ids[0] != ev.ID {
		t.Fatalf("ids = %v", ids)
	}

	n2, err := st.NegentropyLocalMatchCount(ctx, fnostr.Filter{Kinds: []fnostr.Kind{1}, Since: 200})
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("count2 = %d want 0", n2)
	}
}

func TestNegentropyQueryEventsIDs(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	defer func() { _ = st.Close() }()

	id1, _ := fnostr.IDFromHex("c" + strings.Repeat("1", 63))
	id2, _ := fnostr.IDFromHex("d" + strings.Repeat("2", 63))
	for _, ev := range []nostrx.Event{
		{ID: id1.Hex(), PubKey: "e" + strings.Repeat("3", 63), CreatedAt: 1, Kind: 1, Content: "a", Sig: strings.Repeat("0", 128)},
		{ID: id2.Hex(), PubKey: "f" + strings.Repeat("4", 63), CreatedAt: 2, Kind: 1, Content: "b", Sig: strings.Repeat("1", 128)},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	filter := fnostr.Filter{IDs: []fnostr.ID{id1}}
	got := slices.Collect(st.NegentropyQueryEvents(ctx, filter))
	if len(got) != 1 || got[0].ID != id1 {
		t.Fatalf("got %#v", got)
	}
}

func TestNegentropyLocalMatchCountUnsupportedError(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	defer func() { _ = st.Close() }()

	_, err := st.NegentropyLocalMatchCount(ctx, fnostr.Filter{Search: "x"})
	if !errors.Is(err, nostrx.ErrNegentropyUnsupportedFilter) {
		t.Fatalf("err = %v", err)
	}
}

func TestNegentropyQueryEventsMatchesReferenceFilter(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	defer func() { _ = st.Close() }()

	pkA := "a" + strings.Repeat("1", 63)
	pkB := "b" + strings.Repeat("2", 63)
	events := []nostrx.Event{
		{ID: "c" + strings.Repeat("3", 63), PubKey: pkA, CreatedAt: 100, Kind: 1, Content: "n1", Sig: strings.Repeat("4", 128)},
		{ID: "d" + strings.Repeat("4", 63), PubKey: pkA, CreatedAt: 200, Kind: 7, Content: "n2", Sig: strings.Repeat("5", 128)},
		{ID: "e" + strings.Repeat("5", 63), PubKey: pkB, CreatedAt: 150, Kind: 1, Content: "n3", Sig: strings.Repeat("6", 128)},
		{ID: "f" + strings.Repeat("6", 63), PubKey: pkB, CreatedAt: 50, Kind: 0, Content: "{}", Sig: strings.Repeat("7", 128)},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	referenceIDs := func(f fnostr.Filter) []string {
		all := make([]fnostr.Event, 0, len(events))
		rows, err := st.db.QueryContext(ctx, `SELECT raw_json FROM events`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var ev fnostr.Event
			if err := json.Unmarshal([]byte(raw), &ev); err != nil {
				t.Fatal(err)
			}
			all = append(all, ev)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return negentropyReferenceIDs(all, f)
	}

	sortedQuerierIDs := func(f fnostr.Filter) []string {
		var out []string
		for e := range st.NegentropyQueryEvents(ctx, f) {
			out = append(out, e.ID.Hex())
		}
		sort.Strings(out)
		return out
	}

	pkAKey, _ := fnostr.PubKeyFromHex(pkA)
	pkBKey, _ := fnostr.PubKeyFromHex(pkB)
	id0, _ := fnostr.IDFromHex(events[0].ID)

	tests := []struct {
		name   string
		filter fnostr.Filter
	}{
		{"kinds_only", fnostr.Filter{Kinds: []fnostr.Kind{1}}},
		{"authors_only", fnostr.Filter{Authors: []fnostr.PubKey{pkAKey}}},
		{"since_until", fnostr.Filter{Kinds: []fnostr.Kind{1}, Since: 120, Until: 180}},
		{"ids", fnostr.Filter{IDs: []fnostr.ID{id0}}},
		{"compound", fnostr.Filter{Authors: []fnostr.PubKey{pkBKey}, Kinds: []fnostr.Kind{1, 0}}},
		{"limit", fnostr.Filter{Kinds: []fnostr.Kind{0, 1, 7}, Limit: 2}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !nostrx.NegentropySupportedFilter(tc.filter) {
				t.Fatal("filter should be supported")
			}
			want := referenceIDs(tc.filter)
			got := sortedQuerierIDs(tc.filter)
			if !slices.Equal(want, got) {
				t.Fatalf("want %v got %v", want, got)
			}
		})
	}
}

func TestNegentropyLocalMatchCountHonorsLimit(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	defer func() { _ = st.Close() }()

	pk := "a" + strings.Repeat("1", 63)
	input := []nostrx.Event{
		{ID: "b" + strings.Repeat("1", 63), PubKey: pk, CreatedAt: 100, Kind: 1, Content: "1", Sig: strings.Repeat("2", 128)},
		{ID: "c" + strings.Repeat("2", 63), PubKey: pk, CreatedAt: 200, Kind: 1, Content: "2", Sig: strings.Repeat("3", 128)},
		{ID: "d" + strings.Repeat("3", 63), PubKey: pk, CreatedAt: 300, Kind: 1, Content: "3", Sig: strings.Repeat("4", 128)},
	}
	events := make([]fnostr.Event, 0, len(input))
	for _, ev := range input {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
		id, err := fnostr.IDFromHex(ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, fnostr.Event{
			ID:        id,
			CreatedAt: fnostr.Timestamp(ev.CreatedAt),
			Kind:      fnostr.Kind(ev.Kind),
		})
	}

	filter := fnostr.Filter{Kinds: []fnostr.Kind{1}, Limit: 2}
	n, err := st.NegentropyLocalMatchCount(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(len(negentropyReferenceIDs(events, filter))); n != want {
		t.Fatalf("count = %d want %d", n, want)
	}

	got := slices.Collect(st.NegentropyQueryEvents(ctx, filter))
	if len(got) != 2 {
		t.Fatalf("len(got) = %d want 2", len(got))
	}
}
