package httpx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func BenchmarkHandleSearchCached(b *testing.B) {
	ctx := context.Background()
	root := b.TempDir()
	st, err := store.Open(ctx, filepath.Join(root, "bench.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	// High limits so the benchmark measures cache hits, not the /search rate limiter.
	srv, err := New(config.Config{
		RequestTimeout:   time.Second,
		WOTMaxAuthors:    240,
		SearchRateBurst:  1_000_000,
		SearchRatePerSec: 1_000_000,
	}, st, nostrx.NewClient(nil, time.Millisecond))
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 800; i++ {
		ev := nostrx.Event{
			ID:        fmt.Sprintf("%064x", i+1),
			PubKey:    fmt.Sprintf("%064x", i+2),
			CreatedAt: int64(1_713_000_000 + i),
			Kind:      nostrx.KindTextNote,
			Content:   "nostr benchmark token cache warm",
		}
		if err := st.SaveEvent(ctx, ev); err != nil {
			b.Fatal(err)
		}
	}
	handler := srv.Handler()
	// Prime page+store caches once.
	req := httptest.NewRequest(http.MethodGet, "/search?q=nostr", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		b.Fatalf("warm request status = %d, want 200", rec.Code)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/search?q=nostr", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200", rec.Code)
		}
	}
}
