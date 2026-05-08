package nostrx

import (
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

const relayWSCompressionEnv = "PTXT_RELAY_WS_COMPRESSION"

// defaultRelayDialOptions configures permessage-deflate (RFC 7692) with context
// takeover when the relay supports it. PTXT_RELAY_WS_COMPRESSION=0 disables compression.
func defaultRelayDialOptions() *websocket.DialOptions {
	mode := websocket.CompressionContextTakeover
	if strings.TrimSpace(os.Getenv(relayWSCompressionEnv)) == "0" {
		mode = websocket.CompressionDisabled
	}
	h := make(http.Header)
	h.Set("User-Agent", relayUserAgent())
	return &websocket.DialOptions{
		CompressionMode: mode,
		HTTPHeader:      h,
	}
}

var (
	relayUserAgentOnce sync.Once
	relayUserAgentStr  string
)

func relayUserAgent() string {
	relayUserAgentOnce.Do(func() {
		relayUserAgentStr = "ptxt-nstr"
		if info, ok := debug.ReadBuildInfo(); ok {
			if v := info.Main.Version; v != "" && v != "(devel)" {
				relayUserAgentStr = "ptxt-nstr/" + v
			}
		}
	})
	return relayUserAgentStr
}
