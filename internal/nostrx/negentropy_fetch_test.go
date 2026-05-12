package nostrx

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	fnostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip77"
	"fiatjaf.com/nostr/nip77/negentropy"
	"fiatjaf.com/nostr/nip77/negentropy/storage/vector"
	"github.com/coder/websocket"
)

func TestFetchFromUsesNegentropyAndPersistsMissingEvents(t *testing.T) {
	t.Setenv("PTXT_NEGENTROPY", "1")

	local := signedTestEvent(t, KindTextNote, "local", nil)
	remote := signedTestEvent(t, KindTextNote, "remote", nil)
	localExt, err := toExternalEvent(local)
	if err != nil {
		t.Fatal(err)
	}
	remoteExt, err := toExternalEvent(remote)
	if err != nil {
		t.Fatal(err)
	}

	relay := negentropyTestRelay(t, []fnostr.Event{localExt, remoteExt})
	defer relay.Close()

	cache := &mockFetchNegentropyCache{local: []fnostr.Event{localExt}}
	client := NewClient(nil, 2*time.Second)
	client.SetNegentropyCache(cache)
	negFilter, err := NegentropyFilterFromQuery(Query{Kinds: []int{KindTextNote}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := cache.NegentropyLocalMatchCount(context.Background(), negFilter); err != nil || n != 1 {
		t.Fatalf("local count = %d err = %v filter = %#v", n, err, negFilter)
	}
	if !client.negentropyPrefetch(context.Background(), negFilter) {
		t.Fatal("expected negentropy prefetch to be eligible")
	}

	events, err := client.FetchFrom(context.Background(), []string{wsURL(relay.URL)}, Query{
		Kinds: []int{KindTextNote},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("FetchFrom() error = %v", err)
	}
	if len(events) != 1 || events[0].ID != remote.ID {
		t.Fatalf("FetchFrom() events = %#v, want only remote event", events)
	}
	if len(cache.saved) != 1 || cache.saved[0].ID != remote.ID {
		t.Fatalf("saved events = %#v, want remote event", cache.saved)
	}

	metrics := client.Metrics().Negentropy
	if metrics.Attempt != 1 || metrics.Ok != 1 || metrics.Fallback != 0 || metrics.ErrorTotal != 0 {
		t.Fatalf("unexpected negentropy metrics: %#v", metrics)
	}
}

func TestFetchFromFallsBackWhenNegentropyPublishFails(t *testing.T) {
	t.Setenv("PTXT_NEGENTROPY", "1")

	local := signedTestEvent(t, KindTextNote, "local", nil)
	remote := signedTestEvent(t, KindTextNote, "remote", nil)
	localExt, err := toExternalEvent(local)
	if err != nil {
		t.Fatal(err)
	}
	remoteExt, err := toExternalEvent(remote)
	if err != nil {
		t.Fatal(err)
	}

	relay := negentropyTestRelay(t, []fnostr.Event{localExt, remoteExt})
	defer relay.Close()

	cache := &mockFetchNegentropyCache{
		local:   []fnostr.Event{localExt},
		saveErr: errors.New("save failed"),
	}
	client := NewClient(nil, 2*time.Second)
	client.SetNegentropyCache(cache)
	negFilter, err := NegentropyFilterFromQuery(Query{Kinds: []int{KindTextNote}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := cache.NegentropyLocalMatchCount(context.Background(), negFilter); err != nil || n != 1 {
		t.Fatalf("local count = %d err = %v filter = %#v", n, err, negFilter)
	}
	if !client.negentropyPrefetch(context.Background(), negFilter) {
		t.Fatal("expected negentropy prefetch to be eligible")
	}

	events, err := client.FetchFrom(context.Background(), []string{wsURL(relay.URL)}, Query{
		Kinds: []int{KindTextNote},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("FetchFrom() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("FetchFrom() len = %d, want 2 after REQ fallback", len(events))
	}
	if !slices.ContainsFunc(events, func(ev Event) bool { return ev.ID == remote.ID }) {
		t.Fatalf("FetchFrom() missing remote event after fallback: %#v", events)
	}

	metrics := client.Metrics().Negentropy
	if metrics.Attempt != 1 || metrics.Ok != 0 || metrics.Fallback != 1 || metrics.ErrorTotal != 1 {
		t.Fatalf("unexpected negentropy metrics: %#v", metrics)
	}
}

type mockFetchNegentropyCache struct {
	mu      sync.Mutex
	local   []fnostr.Event
	saved   []Event
	saveErr error
}

func (m *mockFetchNegentropyCache) SaveEvent(_ context.Context, event Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saved = append(m.saved, event)
	external, err := toExternalEvent(event)
	if err != nil {
		return err
	}
	m.local = append(m.local, external)
	return nil
}

func (m *mockFetchNegentropyCache) NegentropyLocalMatchCount(_ context.Context, filter fnostr.Filter) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(filterNegentropyEvents(m.local, filter))), nil
}

func (m *mockFetchNegentropyCache) NegentropyQueryEvents(_ context.Context, filter fnostr.Filter) iter.Seq[fnostr.Event] {
	m.mu.Lock()
	selected := filterNegentropyEvents(m.local, filter)
	m.mu.Unlock()
	return func(yield func(fnostr.Event) bool) {
		for _, ev := range selected {
			if !yield(ev) {
				return
			}
		}
	}
}

func filterNegentropyEvents(events []fnostr.Event, filter fnostr.Filter) []fnostr.Event {
	selected := make([]fnostr.Event, 0, len(events))
	for _, ev := range events {
		if filter.Matches(ev) {
			selected = append(selected, ev)
		}
	}
	slices.SortFunc(selected, func(a, b fnostr.Event) int {
		if a.CreatedAt != b.CreatedAt {
			if a.CreatedAt > b.CreatedAt {
				return -1
			}
			return 1
		}
		return strings.Compare(b.ID.Hex(), a.ID.Hex())
	})
	if filter.Limit > 0 && len(selected) > filter.Limit {
		selected = selected[:filter.Limit]
	}
	return selected
}

func negentropyTestRelay(t *testing.T, events []fnostr.Event) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()

		ctx := context.Background()
		var serverNeg *negentropy.Negentropy

		for {
			msgType, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if msgType != websocket.MessageText {
				continue
			}
			if envelope := nip77.ParseNegMessage(string(payload)); envelope != nil {
				switch env := envelope.(type) {
				case *nip77.OpenEnvelope:
					serverNeg = buildRelayNegentropy(events, env.Filter)
					reply, err := serverNeg.Reconcile(env.Message)
					if err != nil {
						t.Fatalf("server negentropy reconcile failed: %v", err)
					}
					if reply != "" {
						writeRelayEnvelope(t, conn, ctx, nip77.MessageEnvelope{SubscriptionID: env.SubscriptionID, Message: reply})
					}
				case *nip77.MessageEnvelope:
					if serverNeg == nil {
						t.Fatal("received NEG-MSG before NEG-OPEN")
					}
					reply, err := serverNeg.Reconcile(env.Message)
					if err != nil {
						t.Fatalf("server negentropy reconcile failed: %v", err)
					}
					if reply != "" {
						writeRelayEnvelope(t, conn, ctx, nip77.MessageEnvelope{SubscriptionID: env.SubscriptionID, Message: reply})
					}
				case *nip77.CloseEnvelope:
					return
				}
				continue
			}

			var envelope []json.RawMessage
			if err := json.Unmarshal(payload, &envelope); err != nil || len(envelope) == 0 {
				continue
			}
			var typ string
			if err := json.Unmarshal(envelope[0], &typ); err != nil {
				continue
			}
			switch typ {
			case "REQ":
				if len(envelope) < 3 {
					continue
				}
				var subID string
				var filter fnostr.Filter
				if err := json.Unmarshal(envelope[1], &subID); err != nil {
					continue
				}
				if err := json.Unmarshal(envelope[2], &filter); err != nil {
					continue
				}
				for _, ev := range filterNegentropyEvents(events, filter) {
					writeRelayJSON(t, conn, ctx, []any{"EVENT", subID, ev})
				}
				writeRelayJSON(t, conn, ctx, []any{"EOSE", subID})
			case "CLOSE":
				return
			}
		}
	}))
}

func buildRelayNegentropy(events []fnostr.Event, filter fnostr.Filter) *negentropy.Negentropy {
	vec := vector.New()
	for _, ev := range filterNegentropyEvents(events, filter) {
		vec.Insert(ev.CreatedAt, ev.ID)
	}
	vec.Seal()
	return negentropy.New(vec, 1<<16, false, false)
}

func writeRelayEnvelope(t *testing.T, conn *websocket.Conn, ctx context.Context, env interface{ MarshalJSON() ([]byte, error) }) {
	t.Helper()
	payload, err := env.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("conn.Write() error = %v", err)
	}
}

func writeRelayJSON(t *testing.T, conn *websocket.Conn, ctx context.Context, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("conn.Write() error = %v", err)
	}
}
