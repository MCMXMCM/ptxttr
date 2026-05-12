package nostrx

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDefaultRelayDialOptionsCompression(t *testing.T) {
	t.Run("default enables context takeover", func(t *testing.T) {
		t.Setenv(relayWSCompressionEnv, "")
		opts := defaultRelayDialOptions()
		if opts == nil || opts.CompressionMode != websocket.CompressionContextTakeover {
			t.Fatalf("CompressionMode = %v, want CompressionContextTakeover", opts)
		}
	})
	t.Run("zero disables compression", func(t *testing.T) {
		t.Setenv(relayWSCompressionEnv, "0")
		opts := defaultRelayDialOptions()
		if opts == nil || opts.CompressionMode != websocket.CompressionDisabled {
			t.Fatalf("CompressionMode = %v, want CompressionDisabled", opts)
		}
	})
	t.Run("user agent set", func(t *testing.T) {
		t.Setenv(relayWSCompressionEnv, "")
		opts := defaultRelayDialOptions()
		ua := opts.HTTPHeader.Get("User-Agent")
		if !strings.HasPrefix(ua, "ptxt-nstr") {
			t.Fatalf("User-Agent = %q, want ptxt-nstr prefix", ua)
		}
	})
}

func TestPublishToWithCompressionDisabledEnv(t *testing.T) {
	t.Setenv(relayWSCompressionEnv, "0")
	acceptedRelay := publishRelayServer(t, true, "saved")
	defer acceptedRelay.Close()

	client := NewClient(nil, 2*time.Second)
	event := signedTestEvent(t, KindTextNote, "hello", nil)
	result, err := client.PublishTo(context.Background(), []string{wsURL(acceptedRelay.URL)}, event)
	if err != nil {
		t.Fatalf("PublishTo() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].Accepted {
		t.Fatalf("expected accepted publish, got %#v", result.Results)
	}
}
