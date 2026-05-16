package nostrx

import (
	"errors"
	"strings"
	"time"
)

// KindHTTPAuth is NIP-98 (HTTP auth) — signed token proving control of a pubkey.
const KindHTTPAuth = 27235

// DefaultNIP98Skew is the maximum |now - event.created_at| allowed for NIP-98 tokens.
const DefaultNIP98Skew = 60 * time.Second

// ValidateNIP98HTTPAuth checks a signed NIP-98 event for HTTP requests.
// canonicalURL must match the client's ["u", ...] tag exactly (typically scheme://host/path with no query).
// This helper rejects any "payload" tag (NIP-98 body hash); callers that need POST/body auth should use a different validator or extend this one.
func ValidateNIP98HTTPAuth(ev Event, method, canonicalURL string, now time.Time, skew time.Duration) error {
	if skew <= 0 {
		skew = DefaultNIP98Skew
	}
	if err := ValidateSignedEvent(ev); err != nil {
		return err
	}
	if ev.Kind != KindHTTPAuth {
		return errors.New("nip98: event kind must be 27235")
	}
	evTime := time.Unix(ev.CreatedAt, 0)
	if delta := now.Sub(evTime); delta > skew || delta < -skew {
		return errors.New("nip98: created_at outside allowed skew")
	}
	if canonicalURL == "" {
		return errors.New("nip98: empty canonical url")
	}
	if got := ev.FirstTagValue("u"); got != canonicalURL {
		return errors.New("nip98: u tag mismatch")
	}
	if want, got := strings.ToUpper(strings.TrimSpace(method)), strings.ToUpper(strings.TrimSpace(ev.FirstTagValue("method"))); got != want {
		return errors.New("nip98: method tag mismatch")
	}
	for _, tag := range ev.Tags {
		if len(tag) >= 1 && tag[0] == "payload" {
			return errors.New("nip98: payload tag not allowed for this request")
		}
	}
	return nil
}
