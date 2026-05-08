package nostrx

import (
	"errors"
	"strings"
	"unicode"

	fnostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

const (
	NIP27KindNPub     = "npub"
	NIP27KindNProfile = "nprofile"
	NIP27KindNEvent   = "nevent"
	NIP27KindNote     = "note"
)

type NIP27Reference struct {
	Raw    string
	Code   string
	Kind   string
	PubKey string
	Event  string
	// EventKind is the kind TLV from an nevent when present (0 when absent or for note).
	EventKind int
	Relays    []string
	Start     int
	End       int
}

func EncodeNProfile(pubkey string, relays []string) string {
	pk, err := fnostr.PubKeyFromHex(pubkey)
	if err != nil {
		return ""
	}
	return nip19.EncodeNprofile(pk, NormalizeRelayList(relays, MaxRelays))
}

func EncodeNProfileOrNPub(pubkey string, relays []string) string {
	if len(NormalizeRelayList(relays, MaxRelays)) == 0 {
		return EncodeNPub(pubkey)
	}
	if encoded := EncodeNProfile(pubkey, relays); encoded != "" {
		return encoded
	}
	return EncodeNPub(pubkey)
}

func DecodeNIP27Reference(raw string) (NIP27Reference, error) {
	code := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(code), "nostr:") {
		code = strings.TrimSpace(code[6:])
	}
	if code == "" {
		return NIP27Reference{}, errors.New("nostr reference is empty")
	}
	prefix, data, err := nip19.Decode(code)
	if err != nil {
		return NIP27Reference{}, err
	}
	ref := NIP27Reference{
		Raw:  raw,
		Code: code,
		Kind: strings.ToLower(prefix),
	}
	switch payload := data.(type) {
	case fnostr.PubKey:
		if ref.Kind != NIP27KindNPub {
			return NIP27Reference{}, errors.New("unexpected pubkey payload for " + ref.Kind)
		}
		normalized, err := NormalizePubKey(payload.Hex())
		if err != nil {
			return NIP27Reference{}, err
		}
		ref.PubKey = normalized
	case fnostr.ProfilePointer:
		if ref.Kind != NIP27KindNProfile {
			return NIP27Reference{}, errors.New("unexpected profile pointer payload for " + ref.Kind)
		}
		normalized, err := NormalizePubKey(payload.PublicKey.Hex())
		if err != nil {
			return NIP27Reference{}, err
		}
		ref.PubKey = normalized
		ref.Relays = NormalizeRelayList(payload.Relays, MaxRelays)
	case fnostr.ID:
		if ref.Kind != NIP27KindNote {
			return NIP27Reference{}, errors.New("unexpected id payload for " + ref.Kind)
		}
		ref.Event = strings.ToLower(strings.TrimSpace(payload.Hex()))
	case fnostr.EventPointer:
		if ref.Kind != NIP27KindNEvent {
			return NIP27Reference{}, errors.New("unexpected event pointer payload for " + ref.Kind)
		}
		ref.Event = strings.ToLower(strings.TrimSpace(payload.ID.Hex()))
		ref.EventKind = int(payload.Kind)
		if author := payload.Author.Hex(); author != "" {
			if normalized, err := NormalizePubKey(author); err == nil {
				ref.PubKey = normalized
			}
		}
		ref.Relays = NormalizeRelayList(payload.Relays, MaxRelays)
	default:
		return NIP27Reference{}, errors.New("unsupported nostr reference type")
	}
	if ref.Kind != NIP27KindNPub && ref.Kind != NIP27KindNProfile &&
		ref.Kind != NIP27KindNote && ref.Kind != NIP27KindNEvent {
		return NIP27Reference{}, errors.New("unsupported nostr reference type")
	}
	return ref, nil
}

func ExtractNIP27References(content string) []NIP27Reference {
	if !containsFold(content, "nostr:") {
		return nil
	}
	refs := make([]NIP27Reference, 0, 4)
	index := 0
	for {
		match := indexFold(content[index:], "nostr:")
		if match < 0 {
			break
		}
		start := index + match
		cursor := start + len("nostr:")
		for cursor < len(content) && isNIP27CodeRune(rune(content[cursor])) {
			cursor++
		}
		if cursor <= start+len("nostr:") {
			index = start + len("nostr:")
			continue
		}
		raw := content[start:cursor]
		ref, err := DecodeNIP27Reference(raw)
		if err == nil {
			ref.Raw = raw
			ref.Start = start
			ref.End = cursor
			refs = append(refs, ref)
		}
		index = cursor
	}
	return refs
}

func ExtractMentionPubKeys(content string) []string {
	refs := ExtractNIP27References(content)
	seen := make(map[string]bool, len(refs))
	pubkeys := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.PubKey == "" || seen[ref.PubKey] {
			continue
		}
		seen[ref.PubKey] = true
		pubkeys = append(pubkeys, ref.PubKey)
	}
	return pubkeys
}

func containsFold(s, substr string) bool {
	return indexFold(s, substr) >= 0
}

// indexFold finds substr in s using ASCII case-insensitive matching without
// allocating a lowercased copy of s. substr is assumed to already be ASCII
// lowercase by the caller.
func indexFold(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	if len(s) < len(substr) {
		return -1
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			a := s[i+j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if a != substr[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func isNIP27CodeRune(r rune) bool {
	if unicode.IsDigit(r) || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
		return true
	}
	return false
}
