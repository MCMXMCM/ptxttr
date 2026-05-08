package nostrx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fnostr "fiatjaf.com/nostr"
	"github.com/coder/websocket"
)

func TestPublishToReturnsRelayResults(t *testing.T) {
	acceptedRelay := publishRelayServer(t, true, "saved")
	defer acceptedRelay.Close()
	rejectedRelay := publishRelayServer(t, false, "blocked")
	defer rejectedRelay.Close()

	client := NewClient(nil, 2*time.Second)
	event := signedTestEvent(t, KindTextNote, "hello", nil)
	result, err := client.PublishTo(context.Background(), []string{wsURL(acceptedRelay.URL), wsURL(rejectedRelay.URL)}, event)
	if err != nil {
		t.Fatalf("PublishTo() error = %v", err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(result.Results))
	}
	if got := result.AcceptedCount(); got != 1 {
		t.Fatalf("AcceptedCount() = %d, want 1", got)
	}
	if !result.Results[0].Accepted && !result.Results[1].Accepted {
		t.Fatalf("expected at least one accepted relay result, got %#v", result.Results)
	}
}

func TestValidateSignedEventRejectsTamperedContent(t *testing.T) {
	event := signedTestEvent(t, KindTextNote, "before", nil)
	event.Content = "after"
	if err := ValidateSignedEvent(event); err == nil {
		t.Fatal("ValidateSignedEvent() error = nil, want verification error")
	}
}

func signedTestEvent(t *testing.T, kind int, content string, tags [][]string) Event {
	t.Helper()
	secret := fnostr.Generate()
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
		t.Fatalf("Sign() error = %v", err)
	}
	return fromExternalEvent(external)
}

func publishRelayServer(t *testing.T, accepted bool, message string) *httptest.Server {
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
