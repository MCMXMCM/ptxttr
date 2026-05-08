package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func flushBenchmarkEvents(b *testing.B, st *Store, ctx context.Context, evs *[]nostrx.Event) {
	b.Helper()
	if len(*evs) == 0 {
		return
	}
	if _, err := st.SaveEvents(ctx, *evs); err != nil {
		b.Fatal(err)
	}
	*evs = (*evs)[:0]
}

func seedBenchmarkSearchNotes(b *testing.B, st *Store, ctx context.Context, n int) {
	b.Helper()
	const batch = 500
	words := []string{"nostr", "bitcoin", "relay", "lightning", "wallet", "garden", "ocean", "signal", "zephyr", "coffee"}
	pk := "0000000000000000000000000000000000000000000000000000000000000001"
	evs := make([]nostrx.Event, 0, batch)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("bench-%08d", i)
		w1 := words[i%len(words)]
		w2 := words[(i/3)%len(words)]
		ev := event(id, pk, int64(1000+i), nostrx.KindTextNote, nil)
		ev.Content = fmt.Sprintf("hello %s world %s note", w1, w2)
		evs = append(evs, ev)
		if len(evs) >= batch {
			flushBenchmarkEvents(b, st, ctx, &evs)
		}
	}
	flushBenchmarkEvents(b, st, ctx, &evs)
}

func BenchmarkSearchNoteSummaries(b *testing.B) {
	ctx := context.Background()
	sizes := []int{2000, 10000}
	if testing.Short() {
		sizes = []int{2000}
	}
	q := SearchNotesQuery{
		Text:  PrepareSearch("nostr"),
		Kinds: []int{nostrx.KindTextNote, nostrx.KindRepost},
		Limit: 50,
	}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("events_%d", n), func(b *testing.B) {
			st, err := Open(ctx, filepath.Join(b.TempDir(), "searchbench.sqlite"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = st.Close() })
			seedBenchmarkSearchNotes(b, st, ctx, n)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := st.SearchNoteSummaries(ctx, q)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSearchNoteSummaries_authors80 models "network" scope: FTS plus
// pubkey IN (...) with as many authors as feed resolution allows.
func BenchmarkSearchNoteSummaries_authors80(b *testing.B) {
	ctx := context.Background()
	const (
		nAuthors       = 80
		notesPerAuthor = 150
	)
	st, err := Open(ctx, filepath.Join(b.TempDir(), "searchbench-net.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })

	authors := make([]string, nAuthors)
	evs := make([]nostrx.Event, 0, 500)
	for a := 0; a < nAuthors; a++ {
		pk := fmt.Sprintf("%064x", a+1)
		authors[a] = pk
		for j := 0; j < notesPerAuthor; j++ {
			id := fmt.Sprintf("net-%02d-%05d", a, j)
			ev := event(id, pk, int64(10_000+a*notesPerAuthor+j), nostrx.KindTextNote, nil)
			if j%5 == 0 {
				ev.Content = "nostr network scoped search benchmark token"
			} else {
				ev.Content = "other chatter unrelated benchmark filler text here"
			}
			evs = append(evs, ev)
			if len(evs) >= 500 {
				flushBenchmarkEvents(b, st, ctx, &evs)
			}
		}
	}
	flushBenchmarkEvents(b, st, ctx, &evs)

	q := SearchNotesQuery{
		Text:    PrepareSearch("nostr"),
		Authors: authors,
		Kinds:   []int{nostrx.KindTextNote, nostrx.KindRepost},
		Limit:   50,
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := st.SearchNoteSummaries(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}
