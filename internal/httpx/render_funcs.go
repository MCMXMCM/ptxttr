package httpx

import (
	"bytes"
	"html/template"
	"net/url"
	"reflect"
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

func briefBio(about string, maxWords int) string {
	words := strings.Fields(strings.TrimSpace(about))
	if len(words) == 0 {
		return ""
	}
	if maxWords <= 0 || len(words) <= maxWords {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:maxWords], " ") + "..."
}

// keyCheckerboardGridHTML lays out a string as rows of four-character groups
// (four groups per row) with a checkerboard emphasis pattern.
func keyCheckerboardGridHTML(s string) template.HTML {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var chunks []string
	for i := 0; i < len(s); i += 4 {
		end := i + 4
		if end > len(s) {
			end = len(s)
		}
		chunks = append(chunks, s[i:end])
	}
	var buf bytes.Buffer
	buf.WriteString(`<div class="profile-npub-grid" translate="no">`)
	for rowStart := 0; rowStart < len(chunks); rowStart += 4 {
		rowIdx := rowStart / 4
		buf.WriteString(`<div class="profile-npub-grid-row">`)
		for col := 0; col < 4; col++ {
			idx := rowStart + col
			if idx >= len(chunks) {
				break
			}
			chunk := chunks[idx]
			class := "profile-npub-cell"
			if (rowIdx+col)%2 == 0 {
				class += " profile-npub-cell--emph"
			}
			buf.WriteString(`<span class="`)
			buf.WriteString(class)
			buf.WriteString(`">`)
			buf.WriteString(template.HTMLEscapeString(chunk))
			buf.WriteString(`</span>`)
		}
		buf.WriteString(`</div>`)
	}
	buf.WriteString(`</div>`)
	return template.HTML(buf.String())
}

// npubGridHTML renders the bech32 npub as a human-readable grid. Returns
// empty HTML when the pubkey cannot be encoded.
func npubGridHTML(pubkey string) template.HTML {
	return keyCheckerboardGridHTML(nostrx.EncodeNPub(pubkey))
}

func profileHref(pubkey string, relays ...string) string {
	if normalized, err := nostrx.NormalizePubKey(pubkey); err == nil {
		return "/u/" + normalized
	}
	encoded := nostrx.EncodeNProfileOrNPub(pubkey, relays)
	if encoded == "" {
		return "/u/"
	}
	return "/u/" + url.PathEscape(encoded)
}

// hexGridHTML renders the normalized hex public key the same way. Returns
// empty HTML when the pubkey is not valid hex.
func hexGridHTML(pubkey string) template.HTML {
	pk, err := nostrx.NormalizePubKey(pubkey)
	if err != nil {
		return ""
	}
	return keyCheckerboardGridHTML(pk)
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"assetPath":                      assetPath,
		"relTime":                        relTime,
		"short":                          short,
		"displayName":                    displayName,
		"profileSecondary":               profileSecondary,
		"authorLabel":                    authorLabel,
		"avatarURL":                      avatarURL,
		"avatarSrc":                      avatarSrc,
		"avatarSrcURL":                   avatarSrcFor,
		"npub":                           nostrx.EncodeNPub,
		"nip05Display":                   nostrx.NIP05DisplayText,
		"profileHref":                    profileHref,
		"npubGridHTML":                   npubGridHTML,
		"hexGridHTML":                    hexGridHTML,
		"nevent":                         nostrx.EncodeNEvent,
		"contentLines":                   contentLines,
		"dict":                           dict,
		"asciiBorder":                    asciiBorder,
		"asciiFill":                      asciiFill,
		"asciiFeedAuthor":                asciiFeedAuthor,
		"asciiFeedHeaderFill":            asciiFeedHeaderFill,
		"asciiReplyHeaderAuthor":         asciiReplyHeaderAuthor,
		"asciiReplyHeaderFill":           asciiReplyHeaderFill,
		"asciiSelectedHeaderAuthor":      asciiSelectedHeaderAuthor,
		"asciiSelectedHeaderFill":        asciiSelectedHeaderFill,
		"asciiReplyFooterFill":           asciiReplyFooterFill,
		"asciiPaddedTextLine":            asciiPaddedTextLine,
		"asciiNoteFooterFill":            asciiNoteFooterFill,
		"reactionBracketBlock":           reactionBracketBlock,
		"asciiAuthor":                    asciiAuthor,
		"asciiBoxLine":                   asciiBoxLine,
		"asciiBoxLines":                  asciiBoxLines,
		"asciiFeedNotePreview":           asciiFeedNotePreviewFor,
		"asciiFeedViewMorePad":           asciiFeedViewMorePad,
		"asciiNoteCollapsedFooterFill":   asciiNoteCollapsedFooterFill,
		"asciiReferencePlaceholderLines": asciiReferencePlaceholderLines,
		"asciiTextLines":                 asciiTextLines,
		"asciiMentionContent":            asciiMentionContent,
		"asciiMentionsJSON":              asciiMentionsJSON,
		"asciiMentionsJSONFor":           asciiMentionsJSONFor,
		"inlineReferenceEvents":          inlineReferenceEvents,
		"replyTextWidth":                 replyTextWidth,
		"selectedReplyTextWidth":         selectedReplyTextWidth,
		"threadTreeTextWidth":            threadTreeTextWidth,
		"asciiReplyPadLine":              asciiReplyPadLine,
		"asciiSelectedReplyPadLine":      asciiSelectedReplyPadLine,
		"isLastIndex":                    isLastIndex,
		"renderMarkdown":                 renderMarkdown,
		"formatDate":                     formatDate,
		"formatProfileMetadataDate":      formatProfileMetadataDate,
		"formatProfileMetadataDateISO":   formatProfileMetadataDateISO,
		"replyCountText":                 replyCountText,
		"replyBadgeText":                 replyBadgeText,
		"referencedEventID":              referencedEventID,
		"referencedEventIDs":             referencedEventIDs,
		"referenceEvent":                 referenceEvent,
		"replyCountFor":                  replyCountFor,
		"reactionTotalFor":               reactionTotalFor,
		"reactionViewerFor":              reactionViewerFor,
		"isSimpleRepost":                 isSimpleRepost,
		"isQuotePost":                    isQuotePost,
		"noteMainBodySourceText":         noteMainBodySourceText,
		"threadTreeMainBodyText":         threadTreeMainBodyText,
		"treeMediaFields":                treeMediaFields,
		"imetaMediaItemsJSON":            imetaMediaItemsJSON,
		"mediaGridClass":                 mediaGridClass,
		"mediaGridSignature":             mediaGridSignature,
		"mediaGridVisibleItems":          mediaGridVisibleItems,
		"mediaGridAspectRatio":           mediaGridAspectRatio,
		"mediaGridAspectStyle":           mediaGridAspectStyle,
		"threadContinueThreadHref":       threadContinueThreadHref,
		"hnPathIndentPx":                 hnPathIndentPx,
		"add":                            func(a, b int) int { return a + b },
		"sub":                            func(a, b int) int { return a - b },
		"threadMaxDepth":                 func() int { return thread.MaxDepth },
		"readsLoadMoreURL":               readsLoadMoreURL,
		"replyContextVisible":            replyContextVisible,
		"replyContextHTML":               replyContextHTML,
		"threadRootID":                   probableThreadRootID,
		"threadSelectHref":               threadSelectHref,
		"threadSelectHrefForRoot":        threadSelectHrefForRoot,
		"repostContextHTML":              repostContextHTML,
		"bioLinkHTML":                    bioLinkHTML,
		"webOfTrustDepth":                webOfTrustDepthForTemplate,
		"webOfTrustEnabled":              webOfTrustEnabledForTemplate,
		"briefBio":                       briefBio,
		"profileWebsiteURL":              profileWebsiteURL,
		"profileWebsiteDisplay":          profileWebsiteDisplay,
		"profilePaymentRaw":              profilePaymentRaw,
		"profileHasMetadataLinks":        profileHasMetadataLinks,
		"profileSearchText":              profileSearchText,
		"filteredRepliesToggleLabel":     filteredRepliesToggleLabel,
		"notificationActionText":         notificationActionText,
		"joinCSV":                        func(items []string) string { return strings.Join(items, ",") },
	}
}

func profileSearchText(pubkey string, profile nostrx.Profile) string {
	parts := []string{
		displayName(map[string]nostrx.Profile{pubkey: profile}, pubkey),
		profile.Name,
		profile.Display,
		profile.NIP05,
		nostrx.EncodeNPub(pubkey),
		pubkey,
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func webOfTrustDepthForTemplate(data any) int {
	if data == nil {
		return defaultLoggedOutWOTDepth
	}
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return defaultLoggedOutWOTDepth
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return defaultLoggedOutWOTDepth
	}
	field := v.FieldByName("WebOfTrustDepth")
	if !field.IsValid() || field.Kind() != reflect.Int {
		return defaultLoggedOutWOTDepth
	}
	depth := int(field.Int())
	if depth < 1 || depth > 3 {
		return defaultLoggedOutWOTDepth
	}
	return depth
}

func webOfTrustEnabledForTemplate(data any) bool {
	if data == nil {
		return true
	}
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return true
	}
	field := v.FieldByName("WebOfTrustEnabled")
	if !field.IsValid() || field.Kind() != reflect.Bool {
		return true
	}
	return field.Bool()
}

func assetPath(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	ref = strings.TrimLeft(ref, "/")
	ref = strings.TrimPrefix(ref, "static/")
	if base == "" {
		return "/" + ref
	}
	return base + "/" + ref
}

// readsLoadMoreURL builds the "Load more reads" link with proper URL encoding,
// keeping the template free of nested {{if}} chains and avoiding
// query-string-injection footguns when values contain unexpected characters.
//
// Viewer prefs (`pubkey`, `seed_pubkey`, `sort`, `reads_tf`, `wot`,
// `wot_depth`, `relays`) are NOT emitted: the client sends those as
// X-Ptxt-* request headers so the resulting URL is cache-key-shared across
// all viewers.
func readsLoadMoreURL(data ReadsPageData) string {
	values := url.Values{}
	values.Set("cursor", strconv.FormatInt(data.Cursor, 10))
	if data.CursorID != "" {
		values.Set("cursor_id", data.CursorID)
	}
	return "/reads?" + values.Encode()
}

func isSimpleRepost(event nostrx.Event) bool {
	return event.Kind == nostrx.KindRepost
}

func threadSelectHref(event nostrx.Event) string {
	return threadSelectHrefForRoot(probableThreadRootID(event), event.ID)
}

func threadSelectHrefForRoot(rootID, eventID string) string {
	rootID = thread.NormalizeHexEventID(strings.TrimSpace(rootID))
	eventID = thread.NormalizeHexEventID(strings.TrimSpace(eventID))
	if eventID == "" {
		return "/thread/"
	}
	if rootID == "" || rootID == eventID {
		return "/thread/" + eventID
	}
	return "/thread/" + rootID + "?selected=" + eventID + "#note-" + eventID
}

func isQuotePost(event nostrx.Event) bool {
	return event.Kind == nostrx.KindTextNote && event.FirstTagValue("q") != ""
}

func noteMainBodySourceText(event nostrx.Event) string {
	if isSimpleRepost(event) {
		return ""
	}
	return stripNIP27EventReferences(event.Content, referencedEventIDs(event))
}

// threadTreeMainBodyText is the tree-view main text column: empty for simple
// reposts (quoted body is rendered separately like the feed note client).
func threadTreeMainBodyText(event nostrx.Event, profiles map[string]nostrx.Profile) string {
	if isSimpleRepost(event) {
		return ""
	}
	return asciiMentionContent(noteMainBodySourceText(event), profiles)
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

func formatProfileMetadataDate(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format("January 2006")
}

func formatProfileMetadataDateISO(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format("2006-01")
}

func short(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:8] + "…" + value[len(value)-4:]
}

func profileSecondary(profiles map[string]nostrx.Profile, pubkey string) string {
	if profile, ok := profiles[pubkey]; ok {
		if nip05 := strings.TrimSpace(profile.NIP05); nip05 != "" {
			return nostrx.NIP05DisplayText(nip05)
		}
	}
	if nostrx.IsValidPubKeyHex(pubkey) {
		return pubkey
	}
	return short(pubkey)
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
		return "@" + short(ref.PubKey), profileHref(ref.PubKey, ref.Relays...)
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

func profileWebsiteURL(website string) string {
	trimmed := strings.TrimSpace(website)
	if trimmed == "" {
		return ""
	}
	if u, err := url.Parse(trimmed); err == nil && u.Scheme != "" {
		return trimmed
	}
	return "https://" + trimmed
}

func profileWebsiteDisplay(website string) string {
	trimmed := strings.TrimSpace(website)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(profileWebsiteURL(trimmed))
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	display := parsed.Host
	if path := strings.TrimSpace(parsed.Path); path != "" && path != "/" {
		display += path
	}
	return display
}

func profilePaymentRaw(profile nostrx.Profile) string {
	if lud16 := strings.TrimSpace(profile.Lud16); lud16 != "" {
		return lud16
	}
	return strings.TrimSpace(profile.Lud06)
}

func profileHasMetadataLinks(profile nostrx.Profile) bool {
	return profilePaymentRaw(profile) != ""
}

func filteredRepliesToggleLabel(count int) string {
	if count == 1 {
		return "show 1 more"
	}
	if count <= 0 {
		return "show more"
	}
	return "show " + strconv.Itoa(count) + " more"
}

func notificationActionText(category string) string {
	switch strings.TrimSpace(category) {
	case "reply":
		return "replied to your note"
	case "like":
		return "liked your note"
	case "repost":
		return "reposted your note"
	case "mention":
		return "mentioned you"
	default:
		return "notified you"
	}
}
