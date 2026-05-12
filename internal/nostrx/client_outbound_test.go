package nostrx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testAuthorHex = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestClientOutboundUnlimitedByDefault(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, time.Second)
	m := c.Metrics()
	if m.Outbound.MaxSlots != 0 {
		t.Fatalf("expected unlimited outbound (max_slots=0), got %d", m.Outbound.MaxSlots)
	}
}

func TestSetRelayMaxOutboundConnsFirstCallWins(t *testing.T) {
	t.Parallel()
	c := NewClient(nil, time.Second)
	c.SetRelayMaxOutboundConns(1)
	c.SetRelayMaxOutboundConns(99)
	if got := c.Metrics().Outbound.MaxSlots; got != 1 {
		t.Fatalf("expected first cap to win (max_slots=1), got %d", got)
	}
}

func newSlowREQRelay(t *testing.T, delay time.Duration) *httptest.Server {
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
		var typ string
		if err := json.Unmarshal(envelope[0], &typ); err != nil || typ != "REQ" {
			return
		}
		var subID string
		if err := json.Unmarshal(envelope[1], &subID); err != nil {
			return
		}
		time.Sleep(delay)
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EOSE","%s"]`, subID)))
	}))
}

func TestClientRelayMaxOutboundConnsSerializesRelayWork(t *testing.T) {
	const delay = 80 * time.Millisecond
	s1 := newSlowREQRelay(t, delay)
	t.Cleanup(s1.Close)
	s2 := newSlowREQRelay(t, delay)
	t.Cleanup(s2.Close)

	c := NewClient(nil, 5*time.Second)
	c.SetRelayMaxOutboundConns(1)

	ctx := context.Background()
	start := time.Now()
	_, err := c.FetchFrom(ctx, []string{wsURL(s1.URL), wsURL(s2.URL)}, Query{
		Authors: []string{testAuthorHex},
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("FetchFrom: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < delay+delay/2 {
		t.Fatalf("expected serialized relay fetches to take at least ~%v, got %v", delay+delay/2, elapsed)
	}
}

func TestClientOutboundAcquireCanceledOnContext(t *testing.T) {
	const slowDelay = 300 * time.Millisecond
	slow := newSlowREQRelay(t, slowDelay)
	t.Cleanup(slow.Close)
	fast := newSlowREQRelay(t, time.Millisecond)
	t.Cleanup(fast.Close)

	c := NewClient(nil, 5*time.Second)
	c.SetRelayMaxOutboundConns(1)

	ctx := context.Background()
	var started atomic.Bool
	go func() {
		started.Store(true)
		_, _ = c.FetchFrom(ctx, []string{wsURL(slow.URL)}, Query{
			Authors: []string{testAuthorHex},
			Limit:   5,
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !started.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !started.Load() {
		t.Fatal("slow FetchFrom did not start")
	}
	time.Sleep(20 * time.Millisecond)

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer shortCancel()
	_, err := c.FetchFrom(shortCtx, []string{wsURL(fast.URL)}, Query{
		Authors: []string{testAuthorHex},
		Limit:   5,
	})
	if err == nil {
		t.Fatal("expected error while waiting for outbound slot")
	}
	if c.Metrics().Outbound.AcquireCanceled == 0 {
		t.Fatal("expected acquire canceled metric > 0")
	}
}
