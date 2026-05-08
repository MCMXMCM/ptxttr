package httpx

import (
	"bytes"
	"html/template"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var (
	markdownOnce     sync.Once
	markdownRenderer goldmark.Markdown
	bioHashtagRE     = regexp.MustCompile(`(^|\s)#([\p{L}\p{N}_]+)`)
)

// bioLinkHTML escapes profile about text, then wraps #tokens in /tag/ links.
// Requests still go through parseTagFromRequestPath for stricter validation.
func bioLinkHTML(about string) template.HTML {
	if strings.TrimSpace(about) == "" {
		return ""
	}
	var buf bytes.Buffer
	last := 0
	for _, m := range bioHashtagRE.FindAllStringSubmatchIndex(about, -1) {
		buf.WriteString(template.HTMLEscapeString(about[last:m[0]]))
		prefix := about[m[2]:m[3]]
		tag := about[m[4]:m[5]]
		buf.WriteString(template.HTMLEscapeString(prefix))
		buf.WriteString(`<a href="/tag/` + url.PathEscape(tag) + `" data-relay-aware>`)
		buf.WriteString(template.HTMLEscapeString("#" + tag))
		buf.WriteString(`</a>`)
		last = m[1]
	}
	buf.WriteString(template.HTMLEscapeString(about[last:]))
	return template.HTML(buf.String())
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"relTime":                  relTime,
		"short":                    short,
		"displayName":              displayName,
		"authorLabel":              authorLabel,
		"avatarURL":                avatarURL,
		"avatarSrc":                avatarSrc,
		"avatarSrcURL":             avatarSrcFor,
		"npub":                     nostrx.EncodeNPub,
		"nevent":                   nostrx.EncodeNEvent,
		"contentLines":             contentLines,
		"dict":                     dict,
		"asciiBorder":              asciiBorder,
		"asciiFill":                asciiFill,
		"asciiNoteFooterFill":      asciiNoteFooterFill,
		"reactionBracketBlock":     reactionBracketBlock,
		"asciiAuthor":              asciiAuthor,
		"asciiBoxLine":             asciiBoxLine,
		"asciiBoxLines":            asciiBoxLines,
		"asciiTextLines":           asciiTextLines,
		"asciiMentionContent":      asciiMentionContent,
		"asciiMentionsJSON":        asciiMentionsJSON,
		"asciiMentionsJSONFor":     asciiMentionsJSONFor,
		"replyTextWidth":           replyTextWidth,
		"asciiReplyPadLine":        asciiReplyPadLine,
		"isLastIndex":              isLastIndex,
		"renderMarkdown":           renderMarkdown,
		"formatDate":               formatDate,
		"replyCountText":           replyCountText,
		"replyBadgeText":           replyBadgeText,
		"referencedEventID":        referencedEventID,
		"referenceEvent":           referenceEvent,
		"replyCountFor":            replyCountFor,
		"reactionTotalFor":         reactionTotalFor,
		"reactionViewerFor":        reactionViewerFor,
		"isSimpleRepost":           isSimpleRepost,
		"isQuotePost":              isQuotePost,
		"treeMediaFields":          treeMediaFields,
		"threadContinueThreadHref": threadContinueThreadHref,
		"hnPathIndentPx":           hnPathIndentPx,
		"sub":                      func(a, b int) int { return a - b },
		"threadMaxDepth":           func() int { return thread.MaxDepth },
		"readsLoadMoreURL":         readsLoadMoreURL,
		"replyContextVisible":      replyContextVisible,
		"replyContextHTML":         replyContextHTML,
		"repostContextHTML":        repostContextHTML,
		"bioLinkHTML":              bioLinkHTML,
	}
}

// readsLoadMoreURL builds the "Load more reads" link with proper URL encoding,
// keeping the template free of nested {{if}} chains and avoiding
// query-string-injection footguns when values contain unexpected characters.
func readsLoadMoreURL(data ReadsPageData) string {
	values := url.Values{}
	values.Set("cursor", strconv.FormatInt(data.Cursor, 10))
	if data.CursorID != "" {
		values.Set("cursor_id", data.CursorID)
	}
	if data.ReadsSort != "" {
		values.Set("sort", data.ReadsSort)
	}
	if data.ReadsTrendingTimeframe != "" {
		values.Set("reads_tf", data.ReadsTrendingTimeframe)
	}
	if data.UserPubKey != "" {
		values.Set("pubkey", data.UserPubKey)
	}
	if data.WebOfTrustEnabled {
		values.Set("wot", "1")
		values.Set("wot_depth", strconv.Itoa(data.WebOfTrustDepth))
		if data.WebOfTrustSeedPubkey != "" {
			values.Set("seed_pubkey", data.WebOfTrustSeedPubkey)
		}
	}
	return "/reads?" + values.Encode()
}

func isSimpleRepost(event nostrx.Event) bool {
	return event.Kind == nostrx.KindRepost
}

func isQuotePost(event nostrx.Event) bool {
	return event.Kind == nostrx.KindTextNote && event.FirstTagValue("q") != ""
}

func referenceEvent(referenced map[string]nostrx.Event, id string) nostrx.Event {
	return referenced[id]
}

func stringIntMapFromAny(v any) map[string]int {
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]int)
	if ok {
		return m
	}
	return nil
}

func stringStringMapFromAny(v any) map[string]string {
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]string)
	if ok {
		return m
	}
	return nil
}

func replyCountFor(counts any, id string) int {
	m := stringIntMapFromAny(counts)
	if m == nil {
		return 0
	}
	return m[id]
}

func lookupReactionIDCased[V any](m map[string]V, id string, zero V) V {
	if m == nil {
		return zero
	}
	key := nostrx.CanonicalHex64(id)
	if v, ok := m[key]; ok {
		return v
	}
	if v, ok := m[strings.TrimSpace(id)]; ok {
		return v
	}
	return zero
}

func reactionTotalFor(totals any, id string) int {
	return lookupReactionIDCased(stringIntMapFromAny(totals), id, 0)
}

func reactionViewerFor(viewers any, id string) string {
	return lookupReactionIDCased(stringStringMapFromAny(viewers), id, "")
}

func formatThousandsSpaced(n, minRunes int) string {
	if n < 0 {
		n = 0
	}
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		out := s
		for len(out) < minRunes {
			out = " " + out
		}
		return out
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	if s != "" {
		parts = append([]string{s}, parts...)
	}
	joined := strings.Join(parts, " ")
	for len(joined) < minRunes {
		joined = " " + joined
	}
	return joined
}

func relTime(ts int64) string {
	if ts == 0 {
		return ""
	}
	d := time.Since(time.Unix(ts, 0))
	if d < time.Minute {
		return "now"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	if d < 24*time.Hour {
		return strconv.Itoa(int(d.Hours())) + "h"
	}
	days := int(d.Hours() / 24)
	if days < 30 {
		return strconv.Itoa(days) + "d"
	}
	months := days / 30
	if months < 12 {
		return strconv.Itoa(months) + "mo"
	}
	years := days / 365
	if years < 1 {
		years = 1
	}
	return strconv.Itoa(years) + "y"
}

func replyCountText(count int) string {
	if count == 1 {
		return "1 reply"
	}
	return strconv.Itoa(count) + " replies"
}

// asciiDecimalPad formats n in decimal with at least minLen columns, leading
// spaces when shorter (counts with len ≥ minLen are unpadded).
func asciiDecimalPad(n, minLen int) string {
	s := strconv.Itoa(n)
	if len(s) >= minLen {
		return s
	}
	return strings.Repeat(" ", minLen-len(s)) + s
}

func replyBadgeText(count int) string {
	if count <= 0 {
		return ""
	}
	kind := "rpls"
	if count == 1 {
		kind = "rply"
	}
	return asciiDecimalPad(count, 3) + " " + kind
}

func formatDate(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format("2006-01-02")
}

func short(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:8] + "…" + value[len(value)-4:]
}

func displayName(profiles map[string]nostrx.Profile, pubkey string) string {
	if profile, ok := profiles[pubkey]; ok {
		return nostrx.DisplayName(profile)
	}
	return short(pubkey)
}

func authorLabel(profiles map[string]nostrx.Profile, pubkey string) string {
	if profile, ok := profiles[pubkey]; ok && (profile.Display != "" || profile.Name != "") {
		return nostrx.DisplayName(profile)
	}
	return displayName(profiles, pubkey)
}

func renderMarkdown(content string) template.HTML {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	content = linkifyNostrReferences(content)
	var out bytes.Buffer
	if err := markdown().Convert([]byte(content), &out); err != nil {
		return template.HTML("<p>" + template.HTMLEscapeString(content) + "</p>")
	}
	return template.HTML(out.String())
}

func linkifyNostrReferences(content string) string {
	refs := nostrx.ExtractNIP27References(content)
	if len(refs) == 0 {
		return content
	}
	var out strings.Builder
	out.Grow(len(content) + len(refs)*16)
	cursor := 0
	for _, ref := range refs {
		if ref.Start < cursor || ref.Start >= len(content) || ref.End > len(content) {
			continue
		}
		out.WriteString(content[cursor:ref.Start])
		label, href := referenceLabelAndHref(ref)
		if label == "" || href == "" {
			out.WriteString(ref.Raw)
		} else {
			out.WriteString("[")
			out.WriteString(label)
			out.WriteString("](")
			out.WriteString(href)
			out.WriteString(")")
		}
		cursor = ref.End
	}
	out.WriteString(content[cursor:])
	return out.String()
}

// nip27EventListHref is the list/detail URL for a decoded NIP-27 note or nevent.
func nip27EventListHref(ref nostrx.NIP27Reference) string {
	if ref.EventKind == nostrx.KindLongForm {
		return "/reads/" + ref.Event
	}
	return "/thread/" + ref.Event
}

func referenceLabelAndHref(ref nostrx.NIP27Reference) (label string, href string) {
	switch ref.Kind {
	case nostrx.NIP27KindNPub, nostrx.NIP27KindNProfile:
		if ref.PubKey == "" {
			return "", ""
		}
		return "@" + short(ref.PubKey), "/u/" + ref.PubKey
	case nostrx.NIP27KindNEvent, nostrx.NIP27KindNote:
		if ref.Event == "" {
			return "", ""
		}
		return "note:" + short(ref.Event), nip27EventListHref(ref)
	default:
		return "", ""
	}
}

func markdown() goldmark.Markdown {
	markdownOnce.Do(func() {
		markdownRenderer = goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,
				extension.Footnote,
				extension.Linkify,
				extension.Strikethrough,
				extension.Table,
				extension.TaskList,
			),
		)
	})
	return markdownRenderer
}

func avatarURL(profiles map[string]nostrx.Profile, pubkey string) string {
	if profile, ok := profiles[pubkey]; ok {
		return profile.Picture
	}
	return ""
}

func avatarSrc(profiles map[string]nostrx.Profile, pubkey string) string {
	return avatarSrcFor(pubkey, avatarURL(profiles, pubkey))
}
