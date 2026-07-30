package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"

	fnostr "fiatjaf.com/nostr"
	"github.com/coder/websocket"
)

func fnostrToNostrxEvent(external fnostr.Event) nostrx.Event {
	tags := make([][]string, 0, len(external.Tags))
	for _, tag := range external.Tags {
		tags = append(tags, []string(tag))
	}
	return nostrx.Event{
		ID:        external.ID.Hex(),
		PubKey:    external.PubKey.Hex(),
		CreatedAt: int64(external.CreatedAt),
		Kind:      int(external.Kind),
		Tags:      tags,
		Content:   external.Content,
		Sig:       fmt.Sprintf("%x", external.Sig[:]),
	}
}

type testServerOptions struct {
	prefix                       string
	requestTimeout               time.Duration
	relayTimeout                 time.Duration
	serverMode                   string
	disableTransitionalFallbacks bool
}

func newTestServer(t *testing.T, opts testServerOptions) (*Server, *store.Store) {
	t.Helper()
	if opts.prefix == "" {
		opts.prefix = "test"
	}
	if opts.requestTimeout == 0 {
		opts.requestTimeout = time.Second
	}
	if opts.relayTimeout == 0 {
		opts.relayTimeout = time.Millisecond
	}
	root := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(root, opts.prefix+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv, err := New(config.Config{
		RequestTimeout:                   opts.requestTimeout,
		WOTMaxAuthors:                    240,
		HydrationEnabled:                 false,
		SeedCrawlerEnabled:               false,
		ViewerCrawlerEnabled:             false,
		ServerMode:                       opts.serverMode,
		ShareServerTransitionalFallbacks: !opts.disableTransitionalFallbacks,
	}, st, nostrx.NewClient(nil, opts.relayTimeout))
	if err != nil {
		t.Fatal(err)
	}
	// Cleanup is LIFO: stop and join server workers before closing the SQLite
	// store they depend on. The previous order let warm/user-async goroutines
	// continue issuing queries against a closed database between tests.
	t.Cleanup(srv.Close)
	return srv, st
}

func testServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	return newTestServer(t, testServerOptions{})
}

func markTestRequestLoggedIn(req *http.Request) {
	req.Header.Set(headerViewerPubkey, strings.Repeat("f", 64))
}

func allowAnonymousAuthors(t *testing.T, st *store.Store, authors ...string) {
	t.Helper()
	ctx := context.Background()
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil {
		t.Fatalf("decode default logged-out seed: %v", err)
	}
	tags := make([][]string, 0, len(authors))
	seen := map[string]struct{}{}
	for _, author := range authors {
		if author == "" {
			continue
		}
		if _, ok := seen[author]; ok {
			continue
		}
		seen[author] = struct{}{}
		tags = append(tags, []string{"p", author})
		profileID := testEventID("profile", author)
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        profileID,
			PubKey:    author,
			Kind:      nostrx.KindProfileMetadata,
			CreatedAt: 1700000000,
			Content:   `{"name":"Cached Test User"}`,
			Sig:       strings.Repeat("1", 128),
		}); err != nil {
			t.Fatalf("save cached profile for %s: %v", author, err)
		}
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        testEventID("gigi-follow", strings.Join(authorMembershipKeys(newAuthorMembership(authors)), ",")),
		PubKey:    seed,
		Kind:      nostrx.KindFollowList,
		CreatedAt: 1700000001,
		Tags:      tags,
		Content:   "",
		Sig:       strings.Repeat("2", 128),
	}); err != nil {
		t.Fatalf("save Gigi follow list: %v", err)
	}
	resolved := uniqueNonEmptyStable(appendDefaultLoggedOutPinnedPubkeys(append(authors, seed)))
	for _, depth := range []int{defaultLoggedOutWOTDepth, defaultLoggedOutThreadWOTDepth} {
		key := resolvedAuthorsCacheKey(seed, webOfTrustOptions{Enabled: true, Depth: depth})
		if err := st.SetResolvedAuthorsDurable(ctx, key, resolved, time.Now().Unix()); err != nil {
			t.Fatalf("cache anonymous authors at depth %d: %v", depth, err)
		}
	}
}

func saveTestFollowList(t *testing.T, st *store.Store, owner string, follows []string, createdAt int64) {
	t.Helper()
	tags := make([][]string, 0, len(follows))
	for _, follow := range follows {
		if follow != "" {
			tags = append(tags, []string{"p", follow})
		}
	}
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        testEventID("follow-list", owner, strings.Join(follows, ","), fmt.Sprint(createdAt)),
		PubKey:    owner,
		Kind:      nostrx.KindFollowList,
		CreatedAt: createdAt,
		Tags:      tags,
		Sig:       strings.Repeat("3", 128),
	}); err != nil {
		t.Fatalf("save follow list for %s: %v", owner, err)
	}
}

func testEventID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("%x", sum[:])
}

// newTestRelayREQEventsByIDs is a minimal Nostr REQ relay: on the first
// subscription, for each id in the filter's ids list it sends an EVENT when
// byID contains that id, then EOSE.
func newTestRelayREQEventsByIDs(ctx context.Context, byID map[string]fnostr.Event) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
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
		var ids []string
		if err := json.Unmarshal(raw["ids"], &ids); err == nil {
			for _, id := range ids {
				if ev, ok := byID[id]; ok {
					encoded, err := json.Marshal(ev)
					if err != nil {
						continue
					}
					_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EVENT",%q,%s]`, subID, string(encoded))))
				}
			}
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EOSE",%q]`, subID)))
	}))
}

// newTestRelayREQEventWhenIDsContain responds with a single EVENT when wantIDHex is in the REQ ids.
func newTestRelayREQEventWhenIDsContain(ctx context.Context, wantIDHex string, ev fnostr.Event) *httptest.Server {
	return newTestRelayREQEventsByIDs(ctx, map[string]fnostr.Event{wantIDHex: ev})
}

func newSlowEOSERelay(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
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
		if err := json.Unmarshal(msg, &envelope); err != nil || len(envelope) < 2 {
			return
		}
		var subID string
		if err := json.Unmarshal(envelope[1], &subID); err != nil {
			return
		}
		time.Sleep(delay)
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EOSE",%q]`, subID)))
	}))
}

func relaysForAuthor(groups []outboxRouteGroup, author string) []string {
	for _, group := range groups {
		for _, item := range group.authors {
			if item == author {
				return group.relays
			}
		}
	}
	return nil
}
