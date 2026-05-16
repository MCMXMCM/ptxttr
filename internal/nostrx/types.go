package nostrx

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

const (
	KindProfileMetadata = 0
	KindTextNote        = 1
	KindComment         = 1111
	KindFollowList      = 3
	KindRepost          = 6
	KindReaction        = 7
	KindMuteList           = 10000
	// MaxMuteListTagRows caps tag rows on NIP-51 kind-10000 (publish validation and server-side mute projection reads).
	MaxMuteListTagRows     = 2000
	KindRelayListMetadata  = 10002
	KindBookmarkList       = 10003
	KindLongForm           = 30023
	MaxRelayQueryLimit     = 200
	DefaultRelayQueryLimit = 50
	MaxRelays              = 8
)

// ClampRelayQueryLimit returns limit when 1 <= limit <= MaxRelayQueryLimit; otherwise DefaultRelayQueryLimit.
func ClampRelayQueryLimit(limit int) int {
	if limit <= 0 || limit > MaxRelayQueryLimit {
		return DefaultRelayQueryLimit
	}
	return limit
}

// IsReplaceablePruneSlotKind is true for replaceable slots that support optional
// history pruning (kinds 0, 3, 10002).
func IsReplaceablePruneSlotKind(kind int) bool {
	switch kind {
	case KindProfileMetadata, KindFollowList, KindRelayListMetadata:
		return true
	default:
		return false
	}
}

type Event struct {
	ID        string     `json:"id"`
	PubKey    string     `json:"pubkey"`
	CreatedAt int64      `json:"created_at"`
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
	Sig       string     `json:"sig"`
	RelayURL  string     `json:"-"`
}

func (event Event) FirstTagValue(name string) string {
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != name {
			continue
		}
		return strings.TrimSpace(tag[1])
	}
	return ""
}

type Profile struct {
	PubKey  string
	Name    string
	Display string
	About   string
	Picture string
	Website string
	NIP05   string
	Event   *Event
}

type RelayInfo struct {
	URL           string `json:"url"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	Software      string `json:"software,omitempty"`
	Version       string `json:"version,omitempty"`
	SupportedNIPs []int  `json:"supported_nips,omitempty"`
	Error         string `json:"error,omitempty"`
}

type RelayUsage string

const (
	RelayUsageAny   RelayUsage = "any"
	RelayUsageRead  RelayUsage = "read"
	RelayUsageWrite RelayUsage = "write"
)

type RelayHint struct {
	URL   string
	Read  bool
	Write bool
}

type ContactRelayHint struct {
	PubKey string
	Relay  string
}

func NormalizePubKey(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if len(value) == 64 && isHex(value) {
		return value, nil
	}
	return "", errors.New("expected 64-character hex public key")
}

func NormalizeRelayURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("relay URL is required")
	}
	if !strings.HasPrefix(value, "wss://") && !strings.HasPrefix(value, "ws://") {
		return "", errors.New("relay URL must start with ws:// or wss://")
	}
	return strings.TrimRight(value, "/"), nil
}

func ParseRelayParams(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	var relays []string
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			if value := strings.TrimSpace(part); value != "" {
				relays = append(relays, value)
			}
		}
	}
	return relays
}

func DedupeEvents(events []Event, limit int) []Event {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].CreatedAt == events[j].CreatedAt {
			return events[i].ID > events[j].ID
		}
		return events[i].CreatedAt > events[j].CreatedAt
	})
	seen := make(map[string]bool, len(events))
	out := make([]Event, 0, len(events))
	for _, event := range events {
		if event.ID == "" || seen[event.ID] {
			continue
		}
		seen[event.ID] = true
		out = append(out, event)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// MuteListPubkeys returns unique hex pubkeys from public "p" tags on a NIP-51
// kind-10000 mute list (private encrypted entries in content are ignored here).
func MuteListPubkeys(event *Event) []string {
	if event == nil || event.Kind != KindMuteList {
		return nil
	}
	seen := make(map[string]bool)
	var pubkeys []string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "p" {
			continue
		}
		normalized, err := NormalizePubKey(tag[1])
		if err != nil || seen[normalized] {
			continue
		}
		seen[normalized] = true
		pubkeys = append(pubkeys, normalized)
	}
	return pubkeys
}

func FollowPubkeys(event *Event) []string {
	if event == nil || event.Kind != KindFollowList {
		return nil
	}
	seen := make(map[string]bool)
	var pubkeys []string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "p" {
			continue
		}
		normalized, err := NormalizePubKey(tag[1])
		if err != nil || seen[normalized] {
			continue
		}
		seen[normalized] = true
		pubkeys = append(pubkeys, normalized)
	}
	return pubkeys
}

type BookmarkEntry struct {
	ID    string
	Relay string
}

// BookmarkEntries extracts unique e-tag entries from a NIP-51 bookmark list
// (kind 10003), preserving order and capturing the optional relay hint.
func BookmarkEntries(event *Event, max int) []BookmarkEntry {
	if event == nil || event.Kind != KindBookmarkList {
		return nil
	}
	seen := make(map[string]bool)
	entries := make([]BookmarkEntry, 0, len(event.Tags))
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "e" {
			continue
		}
		id := strings.TrimSpace(strings.ToLower(tag[1]))
		if len(id) != 64 || !isHex(id) || seen[id] {
			continue
		}
		seen[id] = true
		relay := ""
		if len(tag) >= 3 {
			relay = strings.TrimSpace(tag[2])
		}
		entries = append(entries, BookmarkEntry{ID: id, Relay: relay})
		if max > 0 && len(entries) >= max {
			break
		}
	}
	return entries
}

func BookmarkEventIDs(event *Event, max int) []string {
	entries := BookmarkEntries(event, max)
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func FollowRelayHints(event *Event, max int) []ContactRelayHint {
	if event == nil || event.Kind != KindFollowList {
		return nil
	}
	seen := make(map[string]bool)
	hints := make([]ContactRelayHint, 0, len(event.Tags))
	for _, tag := range event.Tags {
		if len(tag) < 3 || tag[0] != "p" {
			continue
		}
		pubkey, err := NormalizePubKey(tag[1])
		if err != nil {
			continue
		}
		relay, err := NormalizeRelayURL(tag[2])
		if err != nil {
			continue
		}
		key := pubkey + "|" + relay
		if seen[key] {
			continue
		}
		seen[key] = true
		hints = append(hints, ContactRelayHint{PubKey: pubkey, Relay: relay})
	}
	if max > 0 && len(hints) > max {
		return hints[:max]
	}
	return hints
}

func RelayHints(event *Event, max int) []RelayHint {
	if event == nil || event.Kind != KindRelayListMetadata {
		return nil
	}
	seen := make(map[string]int)
	hints := make([]RelayHint, 0, len(event.Tags))
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "r" {
			continue
		}
		url, err := NormalizeRelayURL(tag[1])
		if err != nil {
			continue
		}
		read, write := true, true
		if len(tag) >= 3 {
			switch strings.ToLower(strings.TrimSpace(tag[2])) {
			case "read":
				write = false
			case "write":
				read = false
			}
		}
		if index, exists := seen[url]; exists {
			hints[index].Read = hints[index].Read || read
			hints[index].Write = hints[index].Write || write
			continue
		}
		seen[url] = len(hints)
		hints = append(hints, RelayHint{
			URL:   url,
			Read:  read,
			Write: write,
		})
	}
	if max > 0 && len(hints) > max {
		return hints[:max]
	}
	return hints
}

func RelayURLs(event *Event, max int) []string {
	return RelayURLsForUsage(event, max, RelayUsageAny)
}

func RelayURLsForUsage(event *Event, max int, usage RelayUsage) []string {
	hints := RelayHints(event, 0)
	relays := make([]string, 0, len(hints))
	for _, hint := range hints {
		switch usage {
		case RelayUsageRead:
			if !hint.Read {
				continue
			}
		case RelayUsageWrite:
			if !hint.Write {
				continue
			}
		}
		relays = append(relays, hint.URL)
	}
	return NormalizeRelayList(relays, max)
}

func ParseProfile(pubkey string, event *Event) Profile {
	profile := Profile{PubKey: pubkey, Event: event}
	if event == nil {
		return profile
	}
	var raw struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		About       string `json:"about"`
		Picture     string `json:"picture"`
		Website     string `json:"website"`
		NIP05       string `json:"nip05"`
	}
	if err := json.Unmarshal([]byte(event.Content), &raw); err != nil {
		return profile
	}
	profile.Name = raw.Name
	profile.Display = raw.DisplayName
	profile.About = raw.About
	profile.Picture = raw.Picture
	profile.Website = raw.Website
	profile.NIP05 = raw.NIP05
	return profile
}

func DisplayName(profile Profile) string {
	if profile.Display != "" {
		return profile.Display
	}
	if profile.Name != "" {
		return profile.Name
	}
	if len(profile.PubKey) >= 12 {
		return profile.PubKey[:12]
	}
	return profile.PubKey
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

// CanonicalHex64 returns lowercase s when s is 64 hexadecimal characters
// (Nostr event ids and pubkeys). Otherwise it returns strings.TrimSpace(s).
func CanonicalHex64(s string) string {
	s = strings.TrimSpace(s)
	if len(s) != 64 {
		return s
	}
	lower := strings.ToLower(s)
	if !isHex(lower) {
		return s
	}
	return lower
}
