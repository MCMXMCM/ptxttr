package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestHandleEventAPIReturnsEventJSON(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	id := strings.Repeat("a", 64)
	author := strings.Repeat("b", 64)
	event := nostrx.Event{
		ID:        id,
		PubKey:    author,
		CreatedAt: 1234567890,
		Kind:      nostrx.KindTextNote,
		Content:   "hello world",
		Sig:       "sig",
	}
	if err := st.SaveEvent(ctx, event); err != nil {
		t.Fatalf("save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/event/"+id, nil)
	rr := httptest.NewRecorder()
	srv.handleEventAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("Cache-Control = %q, want immutable", got)
	}
	if got := rr.Header().Get("ETag"); got != `"`+id+`"` {
		t.Fatalf("ETag = %q, want quoted id", got)
	}
	var payload struct {
		Event nostrx.Event `json:"event"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Event.ID != id {
		t.Fatalf("event id = %q, want %q", payload.Event.ID, id)
	}
	if payload.Event.Content != "hello world" {
		t.Fatalf("event content = %q", payload.Event.Content)
	}
}

func TestHandleEventAPI404ForMissingEvent(t *testing.T) {
	srv, _ := testServer(t)
	id := strings.Repeat("c", 64)
	req := httptest.NewRequest(http.MethodGet, "/api/event/"+id, nil)
	rr := httptest.NewRecorder()
	srv.handleEventAPI(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=300") {
		t.Fatalf("Cache-Control = %q, want short negative cache", got)
	}
}

func TestHandleEventAPI400ForInvalidID(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/event/not-a-hex-id", nil)
	rr := httptest.NewRecorder()
	srv.handleEventAPI(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleEventAPI304WhenIfNoneMatch(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	id := strings.Repeat("d", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        id,
		PubKey:    strings.Repeat("e", 64),
		CreatedAt: 1,
		Kind:      nostrx.KindTextNote,
		Content:   "x",
		Sig:       "sig",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/event/"+id, nil)
	req.Header.Set("If-None-Match", `"`+id+`"`)
	rr := httptest.NewRecorder()
	srv.handleEventAPI(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("body len = %d, want empty 304 body", rr.Body.Len())
	}
}
