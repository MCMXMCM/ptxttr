package httpx

import (
	"strings"

	"ptxt-nstr/internal/nostrx"
)

// tryNip19Redirect attempts to interpret a single-segment URL path as a
// Nostr identifier and returns the canonical internal route to redirect to.
// Supported inputs (with optional leading "nostr:"):
//
//   - npub1... / nprofile1... -> /u/<pubkey-hex>
//   - note1...  / nevent1...  -> /thread/<event-id-hex> or /reads/<id> when
//     the nevent kind TLV is NIP-23 (30023)
//   - bare 64-hex string      -> /thread/<hex> (heuristic; we cannot tell hex
//     event id from hex pubkey on the wire, so we follow njump's convention
//     and treat raw hex as an event id; users with a hex pubkey should use
//     the npub or /u/<hex> form)
//
// Returns "" and false when the segment is not a recognizable Nostr code,
// when it is empty, or when it contains characters that indicate it's not a
// single bare identifier (slashes, dots, query separators).
func tryNip19Redirect(segment string) (string, bool) {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return "", false
	}
	// Reject anything that doesn't look like a single identifier segment so
	// we don't accidentally swallow paths like "/.well-known/foo" or
	// "favicon.ico" that have already failed to match a real handler.
	if strings.ContainsAny(segment, "/?#") {
		return "", false
	}
	// Strip an optional "nostr:" prefix the same way DecodeNIP27Reference does
	// so we can run our own routing checks on the underlying code.
	code := segment
	if lower := strings.ToLower(code); strings.HasPrefix(lower, "nostr:") {
		code = strings.TrimSpace(code[6:])
		if code == "" {
			return "", false
		}
	}
	// File extensions almost certainly aren't Nostr identifiers.
	if strings.ContainsRune(code, '.') {
		return "", false
	}
	if ref, err := nostrx.DecodeNIP27Reference(code); err == nil {
		switch ref.Kind {
		case nostrx.NIP27KindNPub, nostrx.NIP27KindNProfile:
			if ref.PubKey != "" {
				return "/u/" + ref.PubKey, true
			}
		case nostrx.NIP27KindNote, nostrx.NIP27KindNEvent:
			if ref.Event != "" {
				if ref.EventKind == nostrx.KindLongForm {
					return "/reads/" + ref.Event, true
				}
				return "/thread/" + ref.Event, true
			}
		}
	}
	if isBare64Hex(code) {
		return "/thread/" + strings.ToLower(code), true
	}
	return "", false
}

// isBare64Hex returns true when value is exactly 64 lowercase or uppercase
// hex characters, the length and alphabet of a Nostr event id or pubkey.
func isBare64Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
