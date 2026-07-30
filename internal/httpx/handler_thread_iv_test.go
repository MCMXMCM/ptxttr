package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestHandleThreadRendersTelegramInstantViewForLongContent(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("e", 64)
	id := strings.Repeat("1", 64)
	long := strings.Repeat("Long-form Nostr article content. ", 30) // > 650 chars
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        id,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   long,
		Sig:       "sig",
	}); err != nil {
		t.Fatal(err)
	}
	allowAnonymousAuthors(t, st, author)
	req := httptest.NewRequest(http.MethodGet, "/thread/"+id, nil)
	req.Header.Set("User-Agent", "TelegramBot (like TwitterBot)")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<article>") {
		t.Fatalf("expected telegram instant view article, got:\n%s", truncateForLog(body, 500))
	}
}

func TestHandleThreadSkipsIVForShortContent(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("f", 64)
	id := strings.Repeat("2", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        id,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   "short note",
		Sig:       "sig",
	}); err != nil {
		t.Fatal(err)
	}
	allowAnonymousAuthors(t, st, author)
	req := httptest.NewRequest(http.MethodGet, "/thread/"+id, nil)
	req.Header.Set("User-Agent", "TelegramBot")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected SSR thread document, got:\n%s", truncateForLog(body, 500))
	}
	if strings.Contains(body, "<article>\n    <h1>") {
		t.Fatalf("expected regular thread body, got IV body:\n%s", truncateForLog(body, 500))
	}
}

func TestHandleThreadIVForcedByQuery(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("a", 64)
	id := strings.Repeat("3", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        id,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   "short note",
		Sig:       "sig",
	}); err != nil {
		t.Fatal(err)
	}
	allowAnonymousAuthors(t, st, author)
	req := httptest.NewRequest(http.MethodGet, "/thread/"+id+"?tgiv=true", nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "<article>") {
		t.Fatalf("expected forced instant view body, got:\n%s", truncateForLog(body, 500))
	}
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
