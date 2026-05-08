package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestHandleUserPostsNewerFragmentCountAndBody(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("c", 64)
	ev1 := nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    author,
		CreatedAt: 1000,
		Kind:      nostrx.KindTextNote,
		Content:   "older",
		Sig:       "sig",
	}
	ev2 := nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    author,
		CreatedAt: 2000,
		Kind:      nostrx.KindTextNote,
		Content:   "newer-only",
		Sig:       "sig",
	}
	if err := st.SaveEvent(ctx, ev1); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, ev2); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/u/"+author+"?fragment=posts-newer&since=1000&since_id="+ev1.ID, nil)
	rr := httptest.NewRecorder()
	srv.handleUser(rr, req)
	if got := rr.Header().Get("X-Ptxt-New-Count"); got != "1" {
		t.Fatalf("X-Ptxt-New-Count = %q, want 1", got)
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("count-only status = %d, want 204", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/u/"+author+"?fragment=posts-newer&since=1000&since_id="+ev1.ID+"&body=1", nil)
	rr2 := httptest.NewRecorder()
	srv.handleUser(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("body status = %d, want 200", rr2.Code)
	}
	body := rr2.Body.String()
	if !strings.Contains(body, "newer-only") {
		t.Fatalf("expected newer note content in fragment body")
	}
}

func TestFetchAuthorsPageProfileHeadRefreshDoesNotPanic(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("d", 64)
	ev := nostrx.Event{
		ID:        strings.Repeat("e", 64),
		PubKey:    author,
		CreatedAt: 1,
		Kind:      nostrx.KindTextNote,
		Content:   "thin-cache",
		Sig:       "sig",
	}
	if err := st.SaveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	_, _ = srv.fetchAuthorsPage(ctx, "", []string{author}, 0, "", 30, nil, "profile", author)
}
