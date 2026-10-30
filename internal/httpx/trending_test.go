package httpx

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ptxt-nstr/internal/store"
)

func TestRecomputeTrendingHonorsMinRecomputeAboveTenMinutes(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	computedAt := time.Now().Add(-15 * time.Minute).Unix()
	for _, timeframe := range []string{trending24h, trending1w} {
		if err := st.WriteTrendingCache(ctx, timeframe, "", []store.TrendingItem{{NoteID: fmt.Sprintf("%064x", 1), Score: 1}}, computedAt); err != nil {
			t.Fatal(err)
		}
	}
	recomputed := func(timeframe string) bool {
		t.Helper()
		_, got, err := st.ReadTrendingCache(ctx, timeframe, "")
		if err != nil {
			t.Fatal(err)
		}
		return got != computedAt
	}

	srv.cfg.TrendingMinRecompute = 20 * time.Minute
	srv.recomputeTrending(ctx)
	for _, timeframe := range []string{trending24h, trending1w} {
		if recomputed(timeframe) {
			t.Fatalf("%s: a 15-minute-old cache was recomputed under a 20-minute floor", timeframe)
		}
	}

	srv.cfg.TrendingMinRecompute = 10 * time.Minute
	srv.recomputeTrending(ctx)
	for _, timeframe := range []string{trending24h, trending1w} {
		if !recomputed(timeframe) {
			t.Fatalf("%s: a 15-minute-old cache was not recomputed under a 10-minute floor", timeframe)
		}
	}
}
