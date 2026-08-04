package httpx

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func newThreadWarmHTTPRequest(id, reason, viewer string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/thread-warm", bytes.NewBufferString(`{"id":"`+id+`","reason":"`+reason+`"}`))
	if viewer != "" {
		req.Header.Set(headerViewerPubkey, viewer)
	}
	return req
}

func desktopThreadWarmServer(t *testing.T, capacity int) (*Server, chan warmJob) {
	t.Helper()
	srv, _ := testServer(t)
	srv.cfg.DesktopMode = true
	queue := make(chan warmJob, capacity)
	srv.intentWarmer = &warmQueue{server: srv, ch: queue, pending: make(map[string]struct{}), interactive: true}
	return srv, queue
}

func TestHandleThreadWarmRequiresViewerAndValidIntent(t *testing.T) {
	srv, _ := desktopThreadWarmServer(t, 4)
	id := strings.Repeat("a", 64)

	rec := httptest.NewRecorder()
	srv.handleThreadWarm(rec, newThreadWarmHTTPRequest(id, "hover", ""))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing viewer status = %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.handleThreadWarm(rec, newThreadWarmHTTPRequest("not-an-event", "hover", strings.Repeat("f", 64)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.handleThreadWarm(rec, newThreadWarmHTTPRequest(id, "scroll", strings.Repeat("e", 64)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid reason status = %d, want 400", rec.Code)
	}
}

func TestHandleThreadWarmAcceptsAndDeduplicatesViewerIntent(t *testing.T) {
	srv, queue := desktopThreadWarmServer(t, 4)
	ctx := context.Background()
	viewer := strings.Repeat("f", 64)
	note := nostrx.Event{
		ID: strings.Repeat("a", 64), PubKey: strings.Repeat("b", 64),
		CreatedAt: 100, Kind: nostrx.KindTextNote, Content: "cold note",
	}
	if err := srv.store.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		srv.handleThreadWarm(rec, newThreadWarmHTTPRequest(note.ID, "pointer", viewer))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d status = %d, want 202; body=%q", i, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Ptxt-Thread-Cache"); got != "miss" {
			t.Fatalf("cache state = %q, want miss", got)
		}
	}
	if len(queue) != 1 {
		t.Fatalf("queued jobs = %d, want one deduplicated job", len(queue))
	}
	job := <-queue
	if job.viewer != viewer || len(job.eventIDs) != 1 || job.eventIDs[0] != note.ID {
		t.Fatalf("queued job = %#v, viewer intent was not isolated", job)
	}
}

func TestHandleThreadWarmReturnsReadyWithoutQueueing(t *testing.T) {
	srv, queue := desktopThreadWarmServer(t, 2)
	ctx := context.Background()
	root := nostrx.Event{
		ID: strings.Repeat("c", 64), PubKey: strings.Repeat("d", 64),
		CreatedAt: 200, Kind: nostrx.KindTextNote, Content: "ready root",
	}
	if err := srv.store.SaveEvent(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.BuildThreadGraphCache(ctx, root.ID, 500); err != nil {
		t.Fatal(err)
	}
	srv.markThreadHydrateContextWarmed(ctx, root.ID)
	srv.markThreadHydrateRepliesReady(ctx, root.ID)

	rec := httptest.NewRecorder()
	srv.handleThreadWarm(rec, newThreadWarmHTTPRequest(root.ID, "focus", strings.Repeat("f", 64)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Ptxt-Thread-Cache"); got != "ready" {
		t.Fatalf("cache state = %q, want ready", got)
	}
	if len(queue) != 0 {
		t.Fatalf("ready intent queued %d jobs, want zero", len(queue))
	}
}

func TestHandleThreadWarmReportsSaturation(t *testing.T) {
	srv, queue := desktopThreadWarmServer(t, 1)
	note := nostrx.Event{
		ID: strings.Repeat("8", 64), PubKey: strings.Repeat("9", 64),
		CreatedAt: 300, Kind: nostrx.KindTextNote, Content: "queued note",
	}
	if err := srv.store.SaveEvent(context.Background(), note); err != nil {
		t.Fatal(err)
	}
	queue <- warmJob{key: "occupied", kind: "threadMaterialize"}

	rec := httptest.NewRecorder()
	srv.handleThreadWarm(rec, newThreadWarmHTTPRequest(note.ID, "hover", strings.Repeat("f", 64)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("saturated response omitted Retry-After")
	}
}
