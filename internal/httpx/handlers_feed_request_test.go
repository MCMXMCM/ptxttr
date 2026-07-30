package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFeedRequestForcesCanonicalLoggedOutWebOfTrust(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/feed?wot=0&wot_depth=3&seed_pubkey=npub1invalid", nil)

	got := srv.feedRequestFromHTTP(req)

	if !got.WoT.Enabled {
		t.Fatal("logged-out feed Web of Trust was disabled by request parameters")
	}
	if got.WoT.Depth != defaultLoggedOutWOTDepth {
		t.Fatalf("logged-out feed depth = %d, want %d", got.WoT.Depth, defaultLoggedOutWOTDepth)
	}
	if got.SeedPubkey != defaultLoggedOutWOTSeedNPub {
		t.Fatalf("logged-out feed seed = %q, want canonical Gigi seed", got.SeedPubkey)
	}
}
