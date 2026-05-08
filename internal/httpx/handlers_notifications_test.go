package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"

	fnostr "fiatjaf.com/nostr"
	"github.com/coder/websocket"
)

func saveNotificationTestEvent(t *testing.T, ctx context.Context, st testEventSaver, event nostrx.Event) {
	t.Helper()
	if err := st.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
}

type testEventSaver interface {
	SaveEvent(context.Context, nostrx.Event) error
}

func newNotificationsRelay(t *testing.T, taggedPubkey string, allowedAuthors []string, events []fnostr.Event) *httptest.Server {
	t.Helper()
	allowed := make(map[string]struct{}, len(allowedAuthors))
	for _, author := range allowedAuthors {
		allowed[author] = struct{}{}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
		ctx := context.Background()
		_, msg, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var envelope []json.RawMessage
		if err := json.Unmarshal(msg, &envelope); err != nil || len(envelope) < 3 {
			return
		}
		var subID string
		if err := json.Unmarshal(envelope[1], &subID); err != nil {
			return
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(envelope[2], &raw); err != nil {
			return
		}
		var authors []string
		var pTags []string
		_ = json.Unmarshal(raw["authors"], &authors)
		_ = json.Unmarshal(raw["#p"], &pTags)
		if len(pTags) != 1 || pTags[0] != taggedPubkey {
			_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EOSE",%q]`, subID)))
			return
		}
		for _, event := range events {
			eventPubkey := event.PubKey.Hex()
			if _, ok := allowed[eventPubkey]; !ok {
				continue
			}
			if !containsNotificationTestString(authors, eventPubkey) {
				continue
			}
			encoded, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EVENT",%q,%s]`, subID, string(encoded))))
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EOSE",%q]`, subID)))
	}))
}

func containsNotificationTestString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestHandleNotificationsLoginCTAWhenNoPubkey(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	rr := httptest.NewRecorder()
	srv.handleNotifications(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Login to view notifications") {
		t.Fatalf("expected login CTA in body")
	}
}

func TestHandleNotificationsListsMentionWhenPubkeySet(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	view := strings.Repeat("ee", 32)
	other := strings.Repeat("ff", 32)
	ev := nostrx.Event{
		ID:        "notif1",
		PubKey:    other,
		CreatedAt: 50,
		Kind:      nostrx.KindTextNote,
		Tags:      [][]string{{"p", view}},
		Content:   "hello you",
		Sig:       "sig",
	}
	saveNotificationTestEvent(t, ctx, st, ev)
	req := httptest.NewRequest(http.MethodGet, "/notifications?pubkey="+view, nil)
	rr := httptest.NewRecorder()
	srv.handleNotifications(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "hello you") {
		t.Fatalf("expected mention content in body")
	}
}

func TestHandleNotificationsWoTHidesMentionFromOutsideGraph(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	view := strings.Repeat("ee", 32)
	other := strings.Repeat("ff", 32)
	ev := nostrx.Event{
		ID:        "notif-wot-hide",
		PubKey:    other,
		CreatedAt: 50,
		Kind:      nostrx.KindTextNote,
		Tags:      [][]string{{"p", view}},
		Content:   "outside wot graph",
		Sig:       "sig",
	}
	saveNotificationTestEvent(t, ctx, st, ev)
	req := httptest.NewRequest(http.MethodGet, "/notifications?pubkey="+view+"&wot=1", nil)
	rr := httptest.NewRecorder()
	srv.handleNotifications(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "outside wot graph") {
		t.Fatalf("did not expect mention from author outside WoT graph")
	}
	if !strings.Contains(body, "No mentions from within 1 degrees of your follow graph yet.") {
		t.Fatalf("expected WoT-aware empty state")
	}
}

func TestHandleNotificationsWoTShowsMentionWhenAuthorFollowed(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	view := strings.Repeat("ee", 32)
	other := strings.Repeat("ff", 32)
	follow := nostrx.Event{
		ID:        "fl-wot",
		PubKey:    view,
		CreatedAt: 10,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", other}},
		Content:   "",
		Sig:       "sig",
	}
	saveNotificationTestEvent(t, ctx, st, follow)
	ev := nostrx.Event{
		ID:        "notif-wot-show",
		PubKey:    other,
		CreatedAt: 50,
		Kind:      nostrx.KindTextNote,
		Tags:      [][]string{{"p", view}},
		Content:   "inside wot graph",
		Sig:       "sig",
	}
	saveNotificationTestEvent(t, ctx, st, ev)
	req := httptest.NewRequest(http.MethodGet, "/notifications?pubkey="+view+"&wot=1", nil)
	rr := httptest.NewRecorder()
	srv.handleNotifications(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "inside wot graph") {
		t.Fatalf("expected mention from followed author when WoT is on")
	}
}

func TestNotificationsDataWoTPaginatesAcrossFilteredMentions(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	view := strings.Repeat("e", 64)
	inside := strings.Repeat("f", 64)
	outside := strings.Repeat("a", 64)
	saveNotificationTestEvent(t, ctx, st, nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    view,
		CreatedAt: 10,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", inside}},
		Sig:       "sig",
	})
	for i := 0; i < 60; i++ {
		saveNotificationTestEvent(t, ctx, st, nostrx.Event{
			ID:        fmt.Sprintf("%064x", i+1),
			PubKey:    outside,
			CreatedAt: int64(500 - i),
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"p", view}},
			Content:   fmt.Sprintf("outside-%02d", i),
			Sig:       "sig",
		})
	}
	for i := 0; i < notificationPageLimit+1; i++ {
		saveNotificationTestEvent(t, ctx, st, nostrx.Event{
			ID:        fmt.Sprintf("%064x", 1000+i),
			PubKey:    inside,
			CreatedAt: int64(300 - i),
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"p", view}},
			Content:   fmt.Sprintf("inside-%02d", i),
			Sig:       "sig",
		})
	}

	first := srv.notificationsData(ctx, view, "", nil, 0, "", false, webOfTrustOptions{Enabled: true, Depth: 1})
	if len(first.Items) != notificationPageLimit {
		t.Fatalf("first page items = %d, want %d", len(first.Items), notificationPageLimit)
	}
	if !first.HasMore {
		t.Fatalf("expected first page to have more matches after filtered scan")
	}
	for _, item := range first.Items {
		if item.PubKey != inside {
			t.Fatalf("unexpected outside author %q in first page", item.PubKey)
		}
	}
	if first.Cursor == 0 || first.CursorID == "" {
		t.Fatalf("expected next cursor after filtered first page")
	}

	second := srv.notificationsData(ctx, view, "", nil, first.Cursor, first.CursorID, false, webOfTrustOptions{Enabled: true, Depth: 1})
	if len(second.Items) != 1 {
		t.Fatalf("second page items = %d, want 1", len(second.Items))
	}
	if second.HasMore {
		t.Fatalf("did not expect more pages after final in-graph mention")
	}
	if second.Items[0].PubKey != inside || second.Items[0].Content != "inside-30" {
		t.Fatalf("unexpected second page item = %#v", second.Items[0])
	}
}

func TestNotificationsDataRefreshFromRelaysScopesAuthorsWhenWoTEnabled(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{relayTimeout: 200 * time.Millisecond})
	ctx := context.Background()
	view := strings.Repeat("1", 64)
	externalRelayEvent := fnostr.Event{
		CreatedAt: fnostr.Timestamp(500),
		Kind:      fnostr.KindTextNote,
		Tags:      fnostr.Tags{{"p", view}},
		Content:   "fetched from scoped relay query",
	}
	relaySecret := fnostr.Generate()
	if err := externalRelayEvent.Sign(relaySecret); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	inside := externalRelayEvent.PubKey.Hex()
	saveNotificationTestEvent(t, ctx, st, nostrx.Event{
		ID:        strings.Repeat("3", 64),
		PubKey:    view,
		CreatedAt: 10,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", inside}},
		Sig:       "sig",
	})
	relayEvent := fnostrToNostrxEvent(externalRelayEvent)
	relay := newNotificationsRelay(t, view, []string{inside}, []fnostr.Event{externalRelayEvent})
	defer relay.Close()
	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")

	data := srv.notificationsData(ctx, view, "", []string{relayURL}, 0, "", true, webOfTrustOptions{Enabled: true, Depth: 1})
	if len(data.Items) != 1 {
		t.Fatalf("notifications items = %d, want 1", len(data.Items))
	}
	if data.Items[0].ID != relayEvent.ID {
		t.Fatalf("notification id = %q, want %q", data.Items[0].ID, relayEvent.ID)
	}
	if data.Items[0].PubKey != inside {
		t.Fatalf("notification pubkey = %q, want %q", data.Items[0].PubKey, inside)
	}
}

func TestNotificationsDataPaginatesReactionRollups(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	authorSecret := fnostr.Generate()
	authorProbe := fnostr.Event{CreatedAt: 1, Kind: fnostr.KindTextNote, Content: "probe"}
	if err := authorProbe.Sign(authorSecret); err != nil {
		t.Fatal(err)
	}
	view := authorProbe.PubKey.Hex()

	for i := 0; i < notificationPageLimit+1; i++ {
		reactorSecret := fnostr.Generate()
		note := fnostr.Event{
			CreatedAt: fnostr.Timestamp(1000 - i),
			Kind:      fnostr.KindTextNote,
			Content:   fmt.Sprintf("note-%02d", i),
		}
		if err := note.Sign(authorSecret); err != nil {
			t.Fatal(err)
		}
		noteEvent := fnostrToNostrxEvent(note)
		saveNotificationTestEvent(t, ctx, st, noteEvent)

		reaction := fnostr.Event{
			CreatedAt: fnostr.Timestamp(2000 - i),
			Kind:      fnostr.KindReaction,
			Content:   "+",
			Tags:      fnostr.Tags{{"e", noteEvent.ID}, {"p", view}},
		}
		if err := reaction.Sign(reactorSecret); err != nil {
			t.Fatal(err)
		}
		saveNotificationTestEvent(t, ctx, st, fnostrToNostrxEvent(reaction))
	}

	first := srv.notificationsData(ctx, view, "", nil, 0, "", false, webOfTrustOptions{})
	if len(first.Entries) != notificationPageLimit {
		t.Fatalf("first page entries = %d, want %d", len(first.Entries), notificationPageLimit)
	}
	for _, entry := range first.Entries {
		if entry.Type != "reaction_rollup" {
			t.Fatalf("unexpected entry type on first page: %#v", entry)
		}
	}
	if !first.HasMore {
		t.Fatalf("expected rollup-only first page to have more")
	}
	if first.Cursor == 0 || first.CursorID == "" {
		t.Fatalf("expected cursor after first rollup page")
	}

	second := srv.notificationsData(ctx, view, "", nil, first.Cursor, first.CursorID, false, webOfTrustOptions{})
	if len(second.Entries) != 1 {
		t.Fatalf("second page entries = %d, want 1", len(second.Entries))
	}
	if second.HasMore {
		t.Fatalf("did not expect more pages after final rollup")
	}
	if second.Entries[0].Type != "reaction_rollup" {
		t.Fatalf("unexpected second page entry = %#v", second.Entries[0])
	}
}
