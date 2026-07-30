package httpx

import (
	"context"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestThreadHydrationRelaysMergesAuthorAndDefaults(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{})
	ctx := context.Background()

	rootAuthor := strings.Repeat("a", 64)
	root := nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    rootAuthor,
		CreatedAt: 100,
		Kind:      nostrx.KindTextNote,
		Tags:      [][]string{{"r", "wss://tag-relay.example"}},
	}
	if err := st.SaveEvent(ctx, root); err != nil {
		t.Fatal(err)
	}
	relayList := nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    rootAuthor,
		CreatedAt: 200,
		Kind:      nostrx.KindRelayListMetadata,
		Tags: [][]string{
			{"r", "wss://read-relay.example", "read"},
			{"r", "wss://write-relay.example", "write"},
		},
	}
	if err := st.SaveEvent(ctx, relayList); err != nil {
		t.Fatal(err)
	}

	srv.cfg.DefaultRelays = []string{"wss://default.example"}
	srv.cfg.MetadataRelays = []string{"wss://metadata.example"}
	srv.cfg.ThreadMaxRelays = 16

	relays := srv.threadHydrationRelays(ctx, "", &root, &root, []string{"wss://request.example"})
	joined := strings.Join(relays, ",")
	for _, want := range []string{
		"wss://request.example",
		"wss://default.example",
		"wss://read-relay.example",
		"wss://write-relay.example",
		"wss://tag-relay.example",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("threadHydrationRelays missing %q in %v", want, relays)
		}
	}
}

func TestThreadHydrationRelaysIncludesMentionedAuthorHints(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{})
	ctx := context.Background()

	rootAuthor := strings.Repeat("a", 64)
	replyAuthor := strings.Repeat("b", 64)
	parentAuthor := strings.Repeat("c", 64)
	root := nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    rootAuthor,
		CreatedAt: 100,
		Kind:      nostrx.KindTextNote,
	}
	selected := nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    replyAuthor,
		CreatedAt: 200,
		Kind:      nostrx.KindTextNote,
		Tags: [][]string{
			{"e", root.ID, "", "root"},
			{"e", strings.Repeat("3", 64), "", "reply"},
			{"p", parentAuthor},
			{"p", replyAuthor},
		},
	}
	parentRelayList := nostrx.Event{
		ID:        strings.Repeat("4", 64),
		PubKey:    parentAuthor,
		CreatedAt: 300,
		Kind:      nostrx.KindRelayListMetadata,
		Tags: [][]string{
			{"r", "wss://parent-read.example", "read"},
		},
	}
	selectedRelayList := nostrx.Event{
		ID:        strings.Repeat("5", 64),
		PubKey:    replyAuthor,
		CreatedAt: 300,
		Kind:      nostrx.KindRelayListMetadata,
		Tags: [][]string{
			{"r", "wss://selected-read.example", "read"},
		},
	}
	if err := st.SaveEvent(ctx, parentRelayList); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, selectedRelayList); err != nil {
		t.Fatal(err)
	}

	srv.cfg.ThreadMaxRelays = 1
	relays := srv.threadHydrationRelays(ctx, "", &root, &selected, nil)
	joined := strings.Join(relays, ",")
	if !strings.Contains(joined, "wss://parent-read.example") {
		t.Fatalf("threadHydrationRelays missing mentioned parent author relay in %v", relays)
	}
	if strings.Contains(joined, "wss://selected-read.example") {
		t.Fatalf("threadHydrationRelays should prioritize mentioned parent author relay before selected author relay: %v", relays)
	}
}

func TestNip50RelaysUsesDedicatedTier(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{})
	srv.cfg.IndexerNIP50Relays = []string{
		"wss://relay.nostr.band",
		"wss://relay.primal.net",
	}
	srv.cfg.IndexerRelays = []string{
		"wss://relay.nostr.band",
		"wss://search.nos.today",
	}
	got := srv.nip50Relays()
	joined := strings.Join(got, ",")
	if strings.Contains(joined, "search.nos.today") {
		t.Fatalf("nip50Relays must not include search-only relays: %v", got)
	}
	if !strings.Contains(joined, "relay.nostr.band") {
		t.Fatalf("nip50Relays missing nostr.band: %v", got)
	}
}

func TestTrendingSearchRelaysUseDedicatedTier(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{})
	srv.cfg.TrendingSearchRelays = []string{
		"wss://relay.nostr.band",
		"wss://search.nos.today",
		"wss://relay.ditto.pub",
	}
	srv.cfg.TrendingSearchMaxRelays = 2
	got := srv.trendingSearchRelays()
	if len(got) != 2 {
		t.Fatalf("trendingSearchRelays len = %d, want 2: %v", len(got), got)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "search.nos.today") {
		t.Fatalf("trendingSearchRelays should keep search relays: %v", got)
	}
}

func TestIndexerRelaysUsesConfigTier(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{})
	srv.cfg.IndexerRelays = []string{
		"wss://relay.nostr.band",
		"wss://search.nos.today",
		"wss://extra.example",
	}
	srv.cfg.IndexerMaxRelays = 2
	got := srv.indexerRelays()
	if len(got) != 2 {
		t.Fatalf("indexerRelays len = %d, want 2: %v", len(got), got)
	}
}
