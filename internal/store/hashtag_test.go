package store

import (
	"context"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestHashtagNoteSummaries_ByTTag(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	kinds := []int{nostrx.KindTextNote, nostrx.KindRepost}

	withTag := event("ht-a", "alice", 100, nostrx.KindTextNote, [][]string{{"t", "bitcoin"}})
	noTag := event("ht-b", "alice", 99, nostrx.KindTextNote, nil)
	wrongTag := event("ht-c", "alice", 98, nostrx.KindTextNote, [][]string{{"t", "ethereum"}})
	for _, ev := range []nostrx.Event{withTag, noTag, wrongTag} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	res, err := st.HashtagNoteSummaries(ctx, HashtagNotesQuery{
		Tag:     "bitcoin",
		Authors: nil,
		Kinds:   kinds,
		Before:  200,
		BeforeID: "",
		Limit:   20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 1 || res.Events[0].ID != "ht-a" {
		t.Fatalf("events = %#v", res.Events)
	}
	if res.HasMore {
		t.Fatalf("HasMore = true, want false")
	}

	res2, err := st.HashtagNoteSummaries(ctx, HashtagNotesQuery{
		Tag:      "bitcoin",
		Authors:  []string{"bob"},
		Kinds:    kinds,
		Before:   200,
		BeforeID: "",
		Limit:    20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Events) != 0 {
		t.Fatalf("scoped to bob: want no rows, got %#v", res2.Events)
	}
}

func TestHashtagNoteSummaries_EmptyTag(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	res, err := st.HashtagNoteSummaries(ctx, HashtagNotesQuery{
		Tag:     "",
		Kinds:   []int{nostrx.KindTextNote},
		Before:  100,
		BeforeID: "",
		Limit:   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 0 {
		t.Fatalf("want empty, got %#v", res.Events)
	}
}
