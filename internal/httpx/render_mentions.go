package httpx

import (
	"encoding/json"
	"html/template"
	"strings"

	"ptxt-nstr/internal/nostrx"
)

// MentionLink is the client-friendly resolution of a single NIP-27 reference
// found in note content. Label is the visible text we substitute into the
// note body (e.g. `@PaulKeating` or `note:abc123de`); Href is the in-app URL
// the client should turn the label into.
type MentionLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
	Title string `json:"title,omitempty"`
}

// RewriteASCIIMentions replaces NIP-27 references in `content` with short,
// display-friendly labels (using `profiles` for display names) and returns
// both the rewritten text and a deduped list of {label, href} records the
// client can use to re-linkify those labels in the rendered DOM.
//
// We rewrite in the source string itself (rather than only annotating) for
// two reasons:
//  1. The server-rendered ASCII path (asciiTextLines/asciiBoxLines) wraps the
//     raw text by columns. Long bech32 codes get hard-split, so the client
//     cannot detect them after rewrap. Short labels wrap cleanly.
//  2. Display names appear immediately in the no-JS / SSR fallback view.
func RewriteASCIIMentions(content string, profiles map[string]nostrx.Profile) (string, []MentionLink) {
	refs := nostrx.ExtractNIP27References(content)
	if len(refs) == 0 {
		return content, nil
	}
	var out strings.Builder
	out.Grow(len(content))
	cursor := 0
	seen := make(map[string]bool, len(refs))
	mentions := make([]MentionLink, 0, len(refs))
	for _, ref := range refs {
		if ref.Start < cursor || ref.Start >= len(content) || ref.End > len(content) {
			continue
		}
		label, href, title := mentionLabelHref(ref, profiles)
		if label == "" || href == "" {
			continue
		}
		out.WriteString(content[cursor:ref.Start])
		out.WriteString(label)
		cursor = ref.End
		key := label + "\x00" + href
		if seen[key] {
			continue
		}
		seen[key] = true
		mentions = append(mentions, MentionLink{Label: label, Href: href, Title: title})
	}
	out.WriteString(content[cursor:])
	return out.String(), mentions
}

func mentionLabelHref(ref nostrx.NIP27Reference, profiles map[string]nostrx.Profile) (label, href, title string) {
	switch ref.Kind {
	case nostrx.NIP27KindNPub, nostrx.NIP27KindNProfile:
		if ref.PubKey == "" {
			return "", "", ""
		}
		name := short(ref.PubKey)
		if profile, ok := profiles[ref.PubKey]; ok {
			name = nostrx.DisplayName(profile)
		}
		return "@" + name, "/u/" + ref.PubKey, ref.Code
	case nostrx.NIP27KindNEvent, nostrx.NIP27KindNote:
		if ref.Event == "" {
			return "", "", ""
		}
		return "note:" + short(ref.Event), nip27EventListHref(ref), ref.Code
	}
	return "", "", ""
}

// asciiMentionsJSON marshals mentions for embedding in a data-* attribute.
// Returns an empty string when there are no mentions, so callers can omit
// the attribute altogether.
func asciiMentionsJSON(content string, profiles map[string]nostrx.Profile) template.JSStr {
	_, mentions := RewriteASCIIMentions(content, profiles)
	if len(mentions) == 0 {
		return ""
	}
	encoded, err := json.Marshal(mentions)
	if err != nil {
		return ""
	}
	return template.JSStr(encoded)
}

// asciiMentionContent returns the rewritten content suitable for both the
// SSR ASCII wrap path and the JS source template.
func asciiMentionContent(content string, profiles map[string]nostrx.Profile) string {
	rewritten, _ := RewriteASCIIMentions(content, profiles)
	return rewritten
}

// asciiMentionsJSONFor merges mentions extracted from any number of source
// strings (note + referenced/quoted content) into a single JSON payload
// suitable for the data-ascii-mentions attribute.
func asciiMentionsJSONFor(profiles map[string]nostrx.Profile, sources ...string) template.JSStr {
	if len(sources) == 0 {
		return ""
	}
	merged := make([]MentionLink, 0, 8)
	seen := make(map[string]bool, 8)
	for _, src := range sources {
		_, mentions := RewriteASCIIMentions(src, profiles)
		for _, m := range mentions {
			key := m.Label + "\x00" + m.Href
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, m)
		}
	}
	if len(merged) == 0 {
		return ""
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return ""
	}
	return template.JSStr(encoded)
}
