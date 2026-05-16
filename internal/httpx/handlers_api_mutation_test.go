package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"

	fnostr "fiatjaf.com/nostr"
	"github.com/coder/websocket"
)

func TestHandleEventsPublishesAndPersistsKind0(t *testing.T) {
	srv, st := mutationTestServer(t)
	relay := mutationRelayServer(t, true, "saved")
	defer relay.Close()

	event := signedMutationEvent(t, nostrx.KindProfileMetadata, `{"name":"mutated","display_name":"Mutated","website":"https://example.com"}`, nil)
	requestBody, _ := json.Marshal(map[string]any{
		"event":  event,
		"relays": []string{wsURL(relay.URL)},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Accepted  int  `json:"accepted"`
		Persisted bool `json:"persisted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response JSON decode failed: %v", err)
	}
	if response.Accepted != 1 || !response.Persisted {
		t.Fatalf("response = %#v, want accepted=1 persisted=true", response)
	}
	latest, err := st.LatestReplaceable(context.Background(), event.PubKey, nostrx.KindProfileMetadata)
	if err != nil {
		t.Fatalf("LatestReplaceable() error = %v", err)
	}
	if latest == nil || latest.ID != event.ID {
		t.Fatalf("latest kind0 = %#v, want id %s", latest, event.ID)
	}
}

func TestHandleEventsPublishesAndPersistsKind10003(t *testing.T) {
	srv, st := mutationTestServer(t)
	relay := mutationRelayServer(t, true, "saved")
	defer relay.Close()
	eventID := strings.Repeat("a", 64)
	event := signedMutationEvent(t, nostrx.KindBookmarkList, "", [][]string{
		{"e", eventID, "wss://relay.example"},
	})
	requestBody, _ := json.Marshal(map[string]any{
		"event":  event,
		"relays": []string{wsURL(relay.URL)},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	latest, err := st.LatestReplaceable(context.Background(), event.PubKey, nostrx.KindBookmarkList)
	if err != nil {
		t.Fatalf("LatestReplaceable() error = %v", err)
	}
	if latest == nil || latest.ID != event.ID {
		t.Fatalf("latest kind10003 = %#v, want id %s", latest, event.ID)
	}
}

func TestHandleEventsReactionRejectsMismatchedPTag(t *testing.T) {
	srv, st := mutationTestServer(t)
	relay := mutationRelayServer(t, true, "saved")
	defer relay.Close()
	root := signedMutationEvent(t, nostrx.KindTextNote, "root", nil)
	if err := st.SaveEvent(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	wrongAuthor := strings.Repeat("c", 64)
	if wrongAuthor == root.PubKey {
		wrongAuthor = strings.Repeat("d", 64)
	}
	react := signedMutationEvent(t, nostrx.KindReaction, "+", [][]string{
		{"e", root.ID},
		{"p", wrongAuthor},
	})
	requestBody, _ := json.Marshal(map[string]any{
		"event":  react,
		"relays": []string{wsURL(relay.URL)},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEventsReactionPublishesAndPersistsKind7(t *testing.T) {
	srv, st := mutationTestServer(t)
	relay := mutationRelayServer(t, true, "saved")
	defer relay.Close()
	root := signedMutationEvent(t, nostrx.KindTextNote, "root", nil)
	if err := st.SaveEvent(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	react := signedMutationEvent(t, nostrx.KindReaction, "+", [][]string{
		{"e", root.ID},
		{"p", root.PubKey},
		{"k", "1"},
	})
	requestBody, _ := json.Marshal(map[string]any{
		"event":  react,
		"relays": []string{wsURL(relay.URL)},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	stats, viewers, err := st.ReactionStatsByNoteIDs(context.Background(), []string{root.ID}, react.PubKey)
	if err != nil {
		t.Fatal(err)
	}
	if stats[root.ID].Total != 1 || stats[root.ID].Up != 1 {
		t.Fatalf("stats = %+v, want one upvote", stats[root.ID])
	}
	if viewers[root.ID] != "+" {
		t.Fatalf("viewer = %q, want +", viewers[root.ID])
	}
}

func TestHandleReactionsAPI_ReturnsReactors(t *testing.T) {
	srv, st := mutationTestServer(t)
	ctx := context.Background()
	root := signedMutationEvent(t, nostrx.KindTextNote, "root for reactions api", nil)
	if err := st.SaveEvent(ctx, root); err != nil {
		t.Fatal(err)
	}
	react := signedMutationEvent(t, nostrx.KindReaction, "+", [][]string{
		{"e", root.ID},
		{"p", root.PubKey},
		{"k", "1"},
	})
	if err := st.SaveEvent(ctx, react); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/reactions?note_id="+root.ID, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Reactions []struct {
			Pubkey      string `json:"pubkey"`
			DisplayName string `json:"display_name"`
			Vote        string `json:"vote"`
		} `json:"reactions"`
		Truncated bool `json:"truncated"`
		Limit     int  `json:"limit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if payload.Truncated || len(payload.Reactions) != 1 {
		t.Fatalf("reactions = %#v", payload)
	}
	if payload.Limit != store.MaxReactionReactorsList {
		t.Fatalf("limit = %d, want %d", payload.Limit, store.MaxReactionReactorsList)
	}
	if payload.Reactions[0].Pubkey != react.PubKey || payload.Reactions[0].Vote != "up" {
		t.Fatalf("row = %#v", payload.Reactions[0])
	}
}

func TestHandleReactionsAPI_NotFound(t *testing.T) {
	srv, _ := mutationTestServer(t)
	missing := strings.Repeat("f", 64)
	req := httptest.NewRequest(http.MethodGet, "/api/reactions?note_id="+missing, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEventsReactionAcceptsUppercaseHexInTags(t *testing.T) {
	srv, st := mutationTestServer(t)
	relay := mutationRelayServer(t, true, "saved")
	defer relay.Close()
	root := signedMutationEvent(t, nostrx.KindTextNote, "root", nil)
	if err := st.SaveEvent(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	react := signedMutationEvent(t, nostrx.KindReaction, "+", [][]string{
		{"e", strings.ToUpper(root.ID)},
		{"p", strings.ToUpper(root.PubKey)},
		{"k", "1"},
	})
	requestBody, _ := json.Marshal(map[string]any{
		"event":  react,
		"relays": []string{wsURL(relay.URL)},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEventsPublishesAndPersistsKind10002(t *testing.T) {
	srv, st := mutationTestServer(t)
	relay := mutationRelayServer(t, true, "saved")
	defer relay.Close()
	event := signedMutationEvent(t, nostrx.KindRelayListMetadata, "", [][]string{
		{"r", "wss://relay.primal.net", "write"},
		{"r", "wss://relay.damus.io"},
	})
	requestBody, _ := json.Marshal(map[string]any{
		"event":  event,
		"relays": []string{wsURL(relay.URL)},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	latest, err := st.LatestReplaceable(context.Background(), event.PubKey, nostrx.KindRelayListMetadata)
	if err != nil {
		t.Fatalf("LatestReplaceable() error = %v", err)
	}
	if latest == nil || latest.ID != event.ID {
		t.Fatalf("latest kind10002 = %#v, want id %s", latest, event.ID)
	}
}

func TestHandleEventsPublishesAndPersistsKind6(t *testing.T) {
	srv, st := mutationTestServer(t)
	relay := mutationRelayServer(t, true, "saved")
	defer relay.Close()
	originalID := strings.Repeat("c", 64)
	author := strings.Repeat("b", 64)
	event := signedMutationEvent(t, nostrx.KindRepost, "", [][]string{
		{"e", originalID, "wss://relay.example"},
		{"p", author},
	})
	requestBody, _ := json.Marshal(map[string]any{
		"event":  event,
		"relays": []string{wsURL(relay.URL)},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	saved, err := st.GetEvent(context.Background(), event.ID)
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}
	if saved == nil || saved.ID != event.ID || saved.Kind != nostrx.KindRepost {
		t.Fatalf("saved kind6 event = %#v", saved)
	}
}

func TestHandleEventsPublishesAndPersistsKind1111(t *testing.T) {
	srv, st := mutationTestServer(t)
	relay := mutationRelayServer(t, true, "saved")
	defer relay.Close()
	parentID := strings.Repeat("d", 64)
	parentPubkey := strings.Repeat("e", 64)
	event := signedMutationEvent(t, nostrx.KindComment, "great read", [][]string{
		{"A", "30023:" + parentPubkey + ":intro", "wss://relay.example"},
		{"K", "30023"},
		{"P", parentPubkey},
		{"a", "30023:" + parentPubkey + ":intro", "wss://relay.example"},
		{"e", parentID, "wss://relay.example", parentPubkey},
		{"k", "30023"},
		{"p", parentPubkey},
	})
	requestBody, _ := json.Marshal(map[string]any{
		"event":  event,
		"relays": []string{wsURL(relay.URL)},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	saved, err := st.GetEvent(context.Background(), event.ID)
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}
	if saved == nil || saved.ID != event.ID || saved.Kind != nostrx.KindComment {
		t.Fatalf("saved kind1111 event = %#v", saved)
	}
}

func TestHandleEventsRejectsUnsupportedKind(t *testing.T) {
	srv, _ := mutationTestServer(t)
	event := signedMutationEvent(t, 22242, "[]", nil)
	requestBody, _ := json.Marshal(map[string]any{"event": event})
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// When relays accept the event but the local SQLite store fails to persist
// it, the handler must still report success so the publisher's cache-bust
// window opens in the browser (recordPublishedAt fires only on a 2xx
// response). Closing the store mid-test is the lowest-friction way to make
// SaveEvent fail without disturbing the relay publish path.
func TestHandleEventsSucceedsWhenPersistFails(t *testing.T) {
	srv, st := mutationTestServer(t)
	relay := mutationRelayServer(t, true, "saved")
	defer relay.Close()

	event := signedMutationEvent(t, nostrx.KindTextNote, "publish ok, persist will fail", nil)
	requestBody, _ := json.Marshal(map[string]any{
		"event":  event,
		"relays": []string{wsURL(relay.URL)},
	})

	// Close the store after the server is wired but before the request fires
	// so the relay round trip succeeds and only the SaveEvent call inside
	// handleEvents hits a "database is closed" error.
	if err := st.Close(); err != nil {
		t.Fatalf("st.Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 on partial-persist failure: %s", rec.Code, rec.Body.String())
	}
	var response publishEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response JSON decode failed: %v", err)
	}
	if response.EventID != event.ID {
		t.Fatalf("response.EventID = %q, want %q", response.EventID, event.ID)
	}
	if response.Accepted < 1 {
		t.Fatalf("response.Accepted = %d, want >= 1 (relay accepted)", response.Accepted)
	}
	if response.Persisted {
		t.Fatalf("response.Persisted = true, want false when SaveEvent failed")
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q, want empty (200 OK does not surface error string)", response.Error)
	}
}

func TestHandleProfileAPIReturnsCachedMetadata(t *testing.T) {
	srv, st := mutationTestServer(t)
	event := signedMutationEvent(t, nostrx.KindProfileMetadata, `{"name":"seed","display_name":"Seed","website":"https://seed.example","nip05":"seed@example.com"}`, nil)
	if err := st.SaveEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/profile?pubkey="+event.PubKey, nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response JSON decode failed: %v", err)
	}
	if response["display_name"] != "Seed" {
		t.Fatalf("display_name = %#v, want Seed", response["display_name"])
	}
	if response["website"] != "https://seed.example" {
		t.Fatalf("website = %#v, want https://seed.example", response["website"])
	}
}

func TestHandleRelayInsightAPIReturnsPublishedRelayHints(t *testing.T) {
	srv, st := mutationTestServer(t)
	event := signedMutationEvent(t, nostrx.KindRelayListMetadata, "", [][]string{
		{"r", "wss://relay.primal.net", "write"},
		{"r", "wss://relay.damus.io", "read"},
	})
	if err := st.SaveEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/relay-insight?pubkey="+event.PubKey, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response struct {
		PubKey          string `json:"pubkey"`
		PublishedRelays []struct {
			URL   string `json:"url"`
			Usage string `json:"usage"`
		} `json:"published_relays"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response JSON decode failed: %v", err)
	}
	if response.PubKey != event.PubKey {
		t.Fatalf("pubkey = %q, want %q", response.PubKey, event.PubKey)
	}
	if len(response.PublishedRelays) == 0 {
		t.Fatal("expected published relay hints in insight payload")
	}
}

func TestHandleBookmarksAPIReturnsBookmarkEntries(t *testing.T) {
	srv, st := mutationTestServer(t)
	eventID := strings.Repeat("a", 64)
	bookmark := signedMutationEvent(t, nostrx.KindBookmarkList, "", [][]string{
		{"e", eventID, "wss://relay.example"},
		{"e", eventID, "wss://relay.example"},
	})
	if err := st.SaveEvent(context.Background(), bookmark); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/bookmarks?pubkey="+bookmark.PubKey, nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var response struct {
		Count   int      `json:"count"`
		IDs     []string `json:"ids"`
		Entries []struct {
			ID    string `json:"id"`
			Relay string `json:"relay"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response JSON decode failed: %v", err)
	}
	if response.Count != 1 || len(response.IDs) != 1 || response.IDs[0] != eventID {
		t.Fatalf("bookmark ids = %#v, want [%s]", response.IDs, eventID)
	}
	if len(response.Entries) != 1 || response.Entries[0].Relay != "wss://relay.example" {
		t.Fatalf("bookmark entries = %#v", response.Entries)
	}
}

func TestHandleMentionsAPIReturnsContactAndThreadCandidates(t *testing.T) {
	srv, st := mutationTestServer(t)
	viewerFollowed := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	followList := signedMutationEvent(t, nostrx.KindFollowList, "", [][]string{
		{"p", viewerFollowed, "wss://relay.primal.net"},
	})
	root := signedMutationEvent(t, nostrx.KindTextNote, "root", nil)
	reply := signedMutationEvent(t, nostrx.KindTextNote, "reply", [][]string{
		{"e", root.ID, "wss://relay.primal.net"},
		{"e", root.ID, "wss://relay.primal.net", "root"},
	})
	for _, event := range []nostrx.Event{followList, root, reply} {
		if err := st.SaveEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/mentions?pubkey="+followList.PubKey+"&root_id="+root.ID, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Candidates []struct {
			PubKey string `json:"pubkey"`
			NPub   string `json:"npub"`
			NRef   string `json:"nref"`
			Source string `json:"source"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response JSON decode failed: %v", err)
	}
	if len(response.Candidates) == 0 {
		t.Fatal("expected mention candidates")
	}
	var foundContact bool
	var foundThread bool
	for _, candidate := range response.Candidates {
		if candidate.PubKey == viewerFollowed && candidate.Source == "contact" {
			foundContact = strings.HasPrefix(candidate.NRef, "nprofile") || strings.HasPrefix(candidate.NRef, "npub")
		}
		if candidate.PubKey == root.PubKey && candidate.Source == "thread" {
			foundThread = candidate.NPub != ""
		}
	}
	if !foundContact {
		t.Fatalf("contact candidate missing from response: %#v", response.Candidates)
	}
	if !foundThread {
		t.Fatalf("thread candidate missing from response: %#v", response.Candidates)
	}
}

func mutationTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	return newTestServer(t, testServerOptions{
		prefix:         "mutation",
		requestTimeout: 2 * time.Second,
		relayTimeout:   2 * time.Second,
	})
}

func assertMuteListPrivateNoStore(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-store") || !strings.Contains(cc, "private") {
		t.Fatalf("Cache-Control = %q, want private and no-store", cc)
	}
}

func TestHandleMuteListAPI_BadRequestWithoutViewer(t *testing.T) {
	srv, _ := mutationTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/mute-list", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	assertMuteListPrivateNoStore(t, rec)
}

func TestHandleMuteListAPI_OKMutedPubkeysOnly(t *testing.T) {
	srv, st := mutationTestServer(t)
	ctx := context.Background()
	secret := fnostr.Generate()
	target := strings.Repeat("d", 64)
	muteEv := signNostrEvent(t, secret, nostrx.KindMuteList, "privdata", [][]string{{"p", target}})
	url := "http://example.com/api/mute-list?pubkey=" + muteEv.PubKey
	if err := st.SaveEvent(ctx, muteEv); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	assertMuteListPrivateNoStore(t, rec)
	if strings.Contains(rec.Body.String(), "privdata") {
		t.Fatal("response must not include mute list event content")
	}
	var payload struct {
		Pubkey        string   `json:"pubkey"`
		MutedPubkeys  []string `json:"muted_pubkeys"`
		Tags          [][]string
		Content       string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Pubkey != muteEv.PubKey {
		t.Fatalf("pubkey %q want %q", payload.Pubkey, muteEv.PubKey)
	}
	if len(payload.MutedPubkeys) != 1 || payload.MutedPubkeys[0] != target {
		t.Fatalf("muted_pubkeys = %#v, want [%q]", payload.MutedPubkeys, target)
	}
	if len(payload.Tags) != 0 || payload.Content != "" {
		t.Fatalf("unexpected tags/content in response: tags=%#v content=%q", payload.Tags, payload.Content)
	}
}

func TestHandleMuteListAPI_OKWithViewerHeader(t *testing.T) {
	srv, st := mutationTestServer(t)
	ctx := context.Background()
	secret := fnostr.Generate()
	target := strings.Repeat("c", 64)
	muteEv := signNostrEvent(t, secret, nostrx.KindMuteList, "privdata", [][]string{{"p", target}})
	if err := st.SaveEvent(ctx, muteEv); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/mute-list", nil)
	req.Header.Set(headerViewerPubkey, muteEv.PubKey)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	assertMuteListPrivateNoStore(t, rec)
	var payload struct {
		Pubkey       string   `json:"pubkey"`
		MutedPubkeys []string `json:"muted_pubkeys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Pubkey != muteEv.PubKey {
		t.Fatalf("pubkey %q want %q", payload.Pubkey, muteEv.PubKey)
	}
	if len(payload.MutedPubkeys) != 1 || payload.MutedPubkeys[0] != target {
		t.Fatalf("muted_pubkeys = %#v, want [%q]", payload.MutedPubkeys, target)
	}
}

func signNostrEvent(t *testing.T, secret fnostr.SecretKey, kind int, content string, tags [][]string) nostrx.Event {
	t.Helper()
	external := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(kind),
		Content:   content,
	}
	external.Tags = make(fnostr.Tags, 0, len(tags))
	for _, tag := range tags {
		external.Tags = append(external.Tags, fnostr.Tag(tag))
	}
	if err := external.Sign(secret); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return fnostrToNostrxEvent(external)
}

func signedMutationEvent(t *testing.T, kind int, content string, tags [][]string) nostrx.Event {
	t.Helper()
	return signNostrEvent(t, fnostr.Generate(), kind, content, tags)
}

func mutationRelayServer(t *testing.T, accepted bool, message string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
		ctx := context.Background()
		msgType, payload, err := conn.Read(ctx)
		if err != nil || msgType != websocket.MessageText {
			return
		}
		var envelope []json.RawMessage
		if err := json.Unmarshal(payload, &envelope); err != nil || len(envelope) < 2 {
			return
		}
		var typ string
		if err := json.Unmarshal(envelope[0], &typ); err != nil || typ != "EVENT" {
			return
		}
		var event fnostr.Event
		if err := json.Unmarshal(envelope[1], &event); err != nil {
			return
		}
		response := fmt.Sprintf(`["OK",%q,%t,%q]`, event.ID.Hex(), accepted, message)
		_ = conn.Write(ctx, websocket.MessageText, []byte(response))
	}))
}

func wsURL(httpURL string) string {
	if strings.HasPrefix(httpURL, "https://") {
		return "wss://" + strings.TrimPrefix(httpURL, "https://")
	}
	return "ws://" + strings.TrimPrefix(httpURL, "http://")
}
