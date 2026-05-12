package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	prefix         string
	requestTimeout time.Duration
	relayTimeout   time.Duration
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
	srv, err := New(config.Config{RequestTimeout: opts.requestTimeout, WOTMaxAuthors: 240}, st, nostrx.NewClient(nil, opts.relayTimeout))
	if err != nil {
		t.Fatal(err)
	}
	return srv, st
}

func testServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	return newTestServer(t, testServerOptions{})
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
