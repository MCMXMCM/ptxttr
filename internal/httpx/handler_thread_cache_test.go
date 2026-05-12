package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

// /thread/<id> SSR is publicly cached at the CDN (CloudFront /thread*
// behavior keys only on URL + query string). The handler must therefore NOT
// bake the requesting viewer's own reaction state into the HTML, otherwise
// the first cache fill would leak that viewer's votes to everyone who hits
// the same URL afterwards. Aggregate `data-ascii-reaction-total` is fine
// because totals are viewer-agnostic.
func TestHandleThreadDocStripsViewerReactionState(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	note := signedMutationEvent(t, nostrx.KindTextNote, "hello thread", nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatalf("save note: %v", err)
	}
	reaction := signedMutationEvent(t, nostrx.KindReaction, "+", [][]string{
		{"e", note.ID},
		{"p", note.PubKey},
	})
	if err := st.SaveEvent(ctx, reaction); err != nil {
		t.Fatalf("save reaction: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+note.ID, nil)
	req.Header.Set(headerViewerPubkey, nostrx.EncodeNPub(reaction.PubKey))
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=\n%s", rr.Code, truncateForLog(rr.Body.String(), 600))
	}
	body := rr.Body.String()

	// Viewer-specific reaction state must NOT be baked into the cacheable
	// SSR. Empty `data-ascii-reaction-viewer=""` is fine; the client refresh
	// in thread.js will populate it after paint.
	for _, leak := range []string{
		`data-ascii-reaction-viewer="+"`,
		`data-ascii-reaction-viewer="-"`,
	} {
		if strings.Contains(body, leak) {
			t.Fatalf("thread SSR leaked viewer reaction state (%s) into cacheable HTML:\n%s",
				leak, truncateForLog(body, 1200))
		}
	}

	// Aggregate totals are viewer-agnostic and SHOULD ride along with the
	// cached response so the viewer-agnostic counters are correct on first
	// paint without a round trip.
	if !strings.Contains(body, `data-ascii-reaction-total="1"`) {
		t.Fatalf("expected data-ascii-reaction-total=\"1\" in SSR body, got:\n%s", truncateForLog(body, 1200))
	}

	if etag := rr.Header().Get("ETag"); etag == "" {
		t.Fatalf("expected ETag header to be preserved after viewer-stripping; got empty")
	}
	cc := rr.Header().Get("Cache-Control")
	if !strings.HasPrefix(cc, "public, max-age=300, s-maxage=300") {
		t.Fatalf("Cache-Control = %q, want prefix \"public, max-age=300, s-maxage=300\"", cc)
	}
}
