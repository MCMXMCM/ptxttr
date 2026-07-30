package nostrx

import (
	"encoding/json"
	"strings"
)

// ParseEmbeddedRepost decodes the NIP-18 embedded note JSON from a kind-6
// repost content field. When expectedID is non-empty, the embedded event id
// must match (after hex normalization).
func ParseEmbeddedRepost(content string, expectedID string) (Event, bool) {
	content = strings.TrimSpace(content)
	if content == "" || !strings.HasPrefix(content, "{") {
		return Event{}, false
	}
	var embedded Event
	if err := json.Unmarshal([]byte(content), &embedded); err != nil {
		return Event{}, false
	}
	switch embedded.Kind {
	case KindTextNote, KindComment:
	default:
		return Event{}, false
	}
	id := CanonicalHex64(strings.TrimSpace(embedded.ID))
	if id == "" {
		return Event{}, false
	}
	if expectedID != "" {
		want := CanonicalHex64(expectedID)
		if want != "" && id != want {
			return Event{}, false
		}
	}
	embedded.ID = id
	embedded.PubKey = CanonicalHex64(strings.TrimSpace(embedded.PubKey))
	return embedded, true
}
