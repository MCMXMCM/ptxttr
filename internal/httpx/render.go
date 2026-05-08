package httpx

import (
	"hash/fnv"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	"ptxt-nstr/internal/thread"
)

// Renderer guardrails. The wrapping helpers run inside template execution and
// allocate one output string per wrapped line, so a runaway width or huge note
// is catastrophic for memory/CPU. These limits cap the worst case to bounded
// allocation per call; callers should never see all of them tripped under
// normal traffic.
const (
	// minRenderWidth is the smallest width the wrap helpers will accept before
	// clamping to a sane fallback. Below this the output degenerates into
	// roughly one string per rune of input. asciiBoxLines internally subtracts
	// 4 from the box width, so this must stay below the smallest legitimate
	// inner width (mobile 42 - 4 = 38; tablet 64 - 4 = 60; desktop 120 - 4 = 116).
	minRenderWidth = 8
	// fallbackRenderWidth is the width substituted when callers pass a width
	// below minRenderWidth (e.g. a fragment handler forgot to set
	// BasePageData.AsciiWidth, leaving it at zero).
	fallbackRenderWidth = asciiWidthMobile
	// maxWrapInputBytes caps the input considered by the wrap helpers. Notes
	// longer than this are truncated with an ellipsis. Average kind-1 notes
	// are well under 2 KiB; the largest kind-1 in the cache is ~11 KiB, so
	// this leaves comfortable headroom.
	maxWrapInputBytes = 32 * 1024
	// maxWrapOutputLines caps the number of wrapped output lines per call.
	// At desktop width (120) this allows ~14 KiB of wrapped text per note,
	// which already exceeds anything we'd reasonably display in a feed cell.
	maxWrapOutputLines = 512
)

type BasePageData struct {
	Title       string
	Active      string
	PageClass   string
	AsciiWidth  int
	SearchQuery string
	OG          OpenGraphMeta
}

// OpenGraphMeta carries the Open Graph + Twitter Card fields rendered into
// the document head. Empty Title means "skip the OG block entirely" so pages
// that don't care about preview cards (debug, fragments) opt in by default.
type OpenGraphMeta struct {
	Type        string // article | profile | website
	Title       string
	Description string
	URL         string // absolute canonical URL of this page
	Image       string // absolute URL; empty means platform-default
	SiteName    string
	Author      string // optional, used for og:article:author
}

type FeedPageData struct {
	BasePageData
	UserPubKey                  string
	UserNPub                    string
	Relays                      []string
	WebOfTrustEnabled           bool
	LoggedOutWOTSeedDisplayName string
	WebOfTrustDepth             int
	FeedSort                    string
	Feed                        []nostrx.Event
	ReferencedEvents            map[string]nostrx.Event
	ReplyCounts                 map[string]int
	ReactionTotals              map[string]int
	ReactionViewers             map[string]string
	Profiles                    map[string]nostrx.Profile
	Cursor                      int64
	CursorID                    string
	HasMore                     bool
	DefaultFeed                 bool
	Trending                    []TrendingNote
	TrendingTimeframe           string
	// FeedSnapshotStarter is true when the feed body came from a canonical
	// starter snapshot (signed-in cold) so the client can refresh once.
	FeedSnapshotStarter bool
}

type ReadItem struct {
	Event       nostrx.Event
	Title       string
	PublishedAt int64
	Summary     string
	ImageURL    string
}

type ReadsPageData struct {
	BasePageData
	Items                       []ReadItem
	Trending                    []TrendingNote
	Profiles                    map[string]nostrx.Profile
	UserPubKey                  string
	ReadsSort                   string
	ReadsTrendingTimeframe      string
	Cursor                      int64
	CursorID                    string
	HasMore                     bool
	Relays                      []string
	WebOfTrustEnabled           bool
	WebOfTrustDepth             int
	WebOfTrustSeedPubkey        string
	LoggedOutWOTSeedDisplayName string
}

type ReadDetailPageData struct {
	BasePageData
	Read      ReadItem
	MoreReads []ReadItem
	Profiles  map[string]nostrx.Profile
}

type BookmarksPageData struct {
	BasePageData
	UserPubKey       string
	Items            []nostrx.Event
	ReferencedEvents map[string]nostrx.Event
	Profiles         map[string]nostrx.Profile
	ReplyCounts      map[string]int
	ReactionTotals   map[string]int
	ReactionViewers  map[string]string
}

type NotificationEntry struct {
	Type      string
	Event     nostrx.Event
	Rollup    store.ReactionRollupRow
	CreatedAt int64
	CursorID  string
}

// NotificationsPageData is the paginated #p mentions list (kinds 1 and 6).
type NotificationsPageData struct {
	BasePageData
	UserPubKey                  string
	Entries                     []NotificationEntry
	Items                       []nostrx.Event
	ReferencedEvents            map[string]nostrx.Event
	Profiles                    map[string]nostrx.Profile
	ReplyCounts                 map[string]int
	ReactionTotals              map[string]int
	ReactionViewers             map[string]string
	ReactionRollups             []store.ReactionRollupRow
	Cursor                      int64
	CursorID                    string
	HasMore                     bool
	WebOfTrustEnabled           bool
	WebOfTrustDepth             int
	WebOfTrustSeedPubkey        string
	LoggedOutWOTSeedDisplayName string
}

type LoginPageData struct {
	BasePageData
}

type RelaysPageData struct {
	BasePageData
	Relays          []string
	RelayStatuses   map[string]store.RelayStatus
	SuggestedRelays []string
}

type FollowListView struct {
	Items         []string
	Query         string
	Page          int
	PageSize      int
	FilteredTotal int
	CachedTotal   int
	CachedExact   bool
	HasPrev       bool
	HasNext       bool
	PrevPage      int
	NextPage      int
}

type UserPageData struct {
	BasePageData
	Profile          nostrx.Profile
	FollowingList    FollowListView
	FollowersList    FollowListView
	UserRelays       []string
	Feed             []nostrx.Event
	Replies          []nostrx.Event
	Media            []nostrx.Event
	ReferencedEvents map[string]nostrx.Event
	ReplyCounts      map[string]int
	ReactionTotals   map[string]int
	ReactionViewers  map[string]string
	Profiles         map[string]nostrx.Profile
	ContactProfiles  map[string]nostrx.Profile
	Relays           []string
	Cursor           int64
	CursorID         string
	HasMore          bool
}

type ThreadPageData struct {
	BasePageData
	Thread           thread.View
	Tree             ThreadTreeData
	ReferencedEvents map[string]nostrx.Event
	ReplyCounts      map[string]int
	ReactionTotals   map[string]int
	ReactionViewers  map[string]string
	Profiles         map[string]nostrx.Profile
	Participants     []ThreadParticipant
	SelectedID       string
	TreeSelectedID   string
	SelectedDepth    int
	TraversalPath    []nostrx.Event
	RootID           string
	ParentID         string
	BackThreadID     string
	BackNoteID       string
	BackReadID       string
	FocusedView      bool
	HiddenReplies    int
	ReplyCursor      int64
	ReplyCursorID    string
	HasMore          bool
}

type ThreadParticipant struct {
	PubKey  string
	Profile nostrx.Profile
	Posts   int
}

// ErrorPanelCopy is the field set consumed by the {{template "error_panel"}}
// partial and the {{template "error_shell"}} layout. Embed it in any page-data
// struct that should render those templates.
type ErrorPanelCopy struct {
	Heading string
	Message string
	Detail  string
	// Shell layout knobs (only read by error_shell):
	AppShellClass    string // defaults to "app-shell"
	MainSectionClass string // defaults to "feed-column route-error-column"
	ShowReadsBack    bool
	ThreadRail       bool   // use thread_right_rail instead of right_rail_static
	ExtraScript      string // optional extra <script type="module" src=...> path
}

type ErrorPageData struct {
	BasePageData
	ErrorPanelCopy
}

// ThreadErrorShellData renders a thread-layout not-found page; the embedded
// ErrorPanelCopy supplies fields for {{template "error_panel" .}}.
type ThreadErrorShellData struct {
	ThreadPageData
	ErrorPanelCopy
}

type StubPageData struct {
	BasePageData
	Heading string
	Message string
}

type SearchPageData struct {
	BasePageData
	Query            string
	Scope            string
	ScopeLabel       string
	ScopeAllURL      string
	ScopeNetworkURL  string
	ShowScopeToggle  bool
	Feed             []nostrx.Event
	ReferencedEvents map[string]nostrx.Event
	ReplyCounts      map[string]int
	ReactionTotals   map[string]int
	ReactionViewers  map[string]string
	Profiles         map[string]nostrx.Profile
	Cursor           int64
	CursorID         string
	HasMore          bool
	OldestCachedAt   int64
	LatestCachedAt   int64
}

// TagPageData renders /tag/{tag} hashtag timelines (NIP-12 "t" tag index).
type TagPageData struct {
	BasePageData
	Tag              string
	TagPath          string // url.PathEscape(Tag)
	Scope            string
	ScopeLabel       string
	ScopeAllURL      string
	ScopeNetworkURL  string
	ShowScopeToggle  bool
	Feed             []nostrx.Event
	ReferencedEvents map[string]nostrx.Event
	ReplyCounts      map[string]int
	ReactionTotals   map[string]int
	ReactionViewers  map[string]string
	Profiles         map[string]nostrx.Profile
	Cursor           int64
	CursorID         string
	HasMore          bool
	OldestCachedAt   int64
	LatestCachedAt   int64
}

type AboutPageData struct {
	BasePageData
}

type SettingsPageData struct {
	BasePageData
	UserPubKey         string
	WebOfTrustMaxDepth int
}

type TrendingNote struct {
	Event      nostrx.Event
	ReplyCount int
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	s.renderStatus(w, 0, name, data)
}

// renderStatus writes an HTML template with an optional explicit status code
// (status == 0 leaves the default 200). Use a non-zero status for 404/500
// pages so responses aren't silently 200 OK.
func (s *Server) renderStatus(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != 0 {
		w.WriteHeader(status)
	}
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template render failed", "template", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) renderNotFound(w http.ResponseWriter, name string, data any) {
	setNegativeCache(w)
	s.renderStatus(w, http.StatusNotFound, name, data)
}

func asciiAuthor(width int, profiles map[string]nostrx.Profile, pubkey string) string {
	label := authorLabel(profiles, pubkey)
	maxAuthor := width - stringDisplayWidth("+-  --  [...] +")
	if maxAuthor < 8 {
		maxAuthor = 8
	}
	return truncateRunes(label, maxAuthor)
}

func contentLines(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return []string{""}
	}
	return strings.Split(content, "\n")
}

func dict(values ...any) map[string]any {
	out := make(map[string]any)
	for i := 0; i+1 < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			continue
		}
		out[key] = values[i+1]
	}
	return out
}

func asciiBorder() string {
	return "+------------------------------------------------------------------------------+"
}

func asciiFill(width int, parts ...string) string {
	used := 0
	for _, part := range parts {
		used += stringDisplayWidth(part)
	}
	remaining := width - used
	if remaining < 1 {
		remaining = 1
	}
	return strings.Repeat("-", remaining)
}

// reactionBracketBlock returns the ASCII reaction widget text "[△] n [▽]"
// with a single space on each side of the score (matches web/static/js/ascii.js).
func reactionBracketBlock(total int, viewer string) string {
	up := "△"
	if viewer == "+" {
		up = "▲"
	}
	down := "▽"
	if viewer == "-" {
		down = "▼"
	}
	num := formatThousandsSpaced(total, 1)
	return "[" + up + "] " + num + " [" + down + "]"
}

// asciiNoteFooterFill returns the middle dash rule for the note card footer
// line: "+-- [reactions] <dashes> [optional reply label] [reply] ---+".
func asciiNoteFooterFill(width int, reactionBlock, replyLabel string) string {
	left := "+-- " + reactionBlock + " "
	var right string
	if replyLabel != "" {
		right = " " + replyLabel + " [reply] ---+"
	} else {
		right = " [reply] ---+"
	}
	used := stringDisplayWidth(left) + stringDisplayWidth(right)
	remaining := width - used
	if remaining < 1 {
		remaining = 1
	}
	return strings.Repeat("-", remaining)
}

func asciiBoxLine(width int, content string) string {
	contentWidth := width - 4
	if contentWidth < 1 {
		contentWidth = 1
	}
	content, displayWidth := truncateRunesWithWidth(content, contentWidth)
	pad := contentWidth - displayWidth
	if pad < 0 {
		pad = 0
	}
	var b strings.Builder
	b.Grow(2 + len(content) + pad + 2)
	b.WriteString("| ")
	b.WriteString(content)
	for i := 0; i < pad; i++ {
		b.WriteByte(' ')
	}
	b.WriteString(" |")
	return b.String()
}

func asciiBoxLines(width int, content string) []string {
	width = clampRenderWidth(width)
	lines := asciiTextLines(content, width-4)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, asciiBoxLine(width, line))
	}
	return out
}

func asciiTextLines(content string, width int) []string {
	width = clampRenderWidth(width)
	content = clampRenderInput(content)
	if cached, ok := wrapCacheGet(content, width); ok {
		return cached
	}
	out := wrapAsciiTextLinesUncached(content, width)
	wrapCachePut(content, width, out)
	return out
}

func wrapAsciiTextLinesUncached(content string, width int) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return []string{""}
	}
	// Pre-size based on the smaller of newline count and the output cap to
	// avoid pathological pre-allocation when the input has many newlines.
	preallocCap := strings.Count(content, "\n") + 1
	if preallocCap > maxWrapOutputLines {
		preallocCap = maxWrapOutputLines
	}
	out := make([]string, 0, preallocCap)
	truncated := false
	for _, raw := range strings.Split(content, "\n") {
		if len(out) >= maxWrapOutputLines {
			truncated = true
			break
		}
		lines := wrapASCIITextLine(raw, width)
		if len(lines) == 0 {
			out = append(out, "")
			continue
		}
		remaining := maxWrapOutputLines - len(out)
		if len(lines) > remaining {
			out = append(out, lines[:remaining]...)
			truncated = true
			break
		}
		out = append(out, lines...)
	}
	if truncated && len(out) > 0 {
		out[len(out)-1] = appendEllipsisIfRoom(out[len(out)-1], width)
	}
	return out
}

// clampRenderWidth keeps width inside a sane range. The wrap helpers degenerate
// catastrophically (one string per rune) when width is too small, so we substitute
// a safe fallback rather than honoring the request.
func clampRenderWidth(width int) int {
	if width < minRenderWidth {
		return fallbackRenderWidth
	}
	return width
}

// clampRenderInput truncates content that exceeds maxWrapInputBytes. We slice
// on a UTF-8 boundary so we never split a multi-byte rune.
func clampRenderInput(content string) string {
	if len(content) <= maxWrapInputBytes {
		return content
	}
	cut := maxWrapInputBytes
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	return content[:cut] + "…"
}

func appendEllipsisIfRoom(line string, width int) string {
	if width <= 1 {
		return line
	}
	w := stringDisplayWidth(line)
	if w+1 <= width {
		return line + "…"
	}
	if w == 0 {
		return "…"
	}
	// Replace the last visible rune with the ellipsis to stay within width.
	return takeRunes(line, width-1) + "…"
}

func wrapASCIITextLine(raw string, width int) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 4)
	var line strings.Builder
	line.Grow(min(len(raw), width*2))
	lineWidth := 0
	wordStart := -1
	wordWidth := 0
	flushWord := func(end int) {
		if wordStart < 0 {
			return
		}
		appendWrappedWord(&out, &line, &lineWidth, raw[wordStart:end], wordWidth, width)
		wordStart = -1
		wordWidth = 0
	}
	for i, r := range raw {
		if isWrapSpace(r) {
			flushWord(i)
			continue
		}
		if wordStart < 0 {
			wordStart = i
		}
		wordWidth += runeDisplayWidth(r)
	}
	flushWord(len(raw))
	if lineWidth > 0 {
		out = append(out, line.String())
	}
	return out
}

func appendWrappedWord(out *[]string, line *strings.Builder, lineWidth *int, word string, wordWidth int, maxWidth int) {
	for word != "" {
		if *lineWidth == 0 {
			if wordWidth <= maxWidth {
				line.WriteString(word)
				*lineWidth = wordWidth
				return
			}
			chunk, rest, chunkWidth, restWidth := splitAtDisplayWidth(word, maxWidth, wordWidth)
			*out = append(*out, chunk)
			word = rest
			wordWidth = restWidth
			if chunkWidth == 0 {
				return
			}
			continue
		}
		if *lineWidth+1+wordWidth <= maxWidth {
			line.WriteByte(' ')
			line.WriteString(word)
			*lineWidth += 1 + wordWidth
			return
		}
		*out = append(*out, line.String())
		line.Reset()
		*lineWidth = 0
	}
}

func splitAtDisplayWidth(value string, maxWidth int, totalWidth int) (head string, tail string, headWidth int, tailWidth int) {
	if maxWidth <= 0 || value == "" {
		return "", value, 0, totalWidth
	}
	if totalWidth <= maxWidth {
		return value, "", totalWidth, 0
	}
	used := 0
	for i, r := range value {
		rw := runeDisplayWidth(r)
		next := i + utf8.RuneLen(r)
		if used+rw > maxWidth {
			if used == 0 {
				return value[:next], value[next:], rw, max(totalWidth-rw, 0)
			}
			return value[:i], value[i:], used, max(totalWidth-used, 0)
		}
		used += rw
	}
	return value, "", used, 0
}

func isWrapSpace(r rune) bool {
	if r <= 0x7f {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			return true
		default:
			return false
		}
	}
	return unicode.IsSpace(r)
}

func replyTextWidth(width int) int {
	replyWidth := width - 8
	if replyWidth < 20 {
		return 20
	}
	return replyWidth
}

// asciiReplyPadLine is a spaces-only row for vertical padding in templates;
// width matches asciiTextLines(..., replyTextWidth(w)) and ascii.js wrapText.
func asciiReplyPadLine(width int) string {
	tw := clampRenderWidth(replyTextWidth(width))
	return strings.Repeat(" ", tw)
}

func isLastIndex(index, total int) bool {
	return total > 0 && index == total-1
}

func truncateRunes(value string, max int) string {
	out, _ := truncateRunesWithWidth(value, max)
	return out
}

// truncateRunesWithWidth returns the truncated string and its final display
// width. Folding the truncation and the width measurement into one helper
// avoids a second full-string scan in hot rendering paths.
func truncateRunesWithWidth(value string, max int) (string, int) {
	if max < 1 {
		return "", 0
	}
	w := stringDisplayWidth(value)
	if w <= max {
		return value, w
	}
	if max == 1 {
		return "…", 1
	}
	head := (max - 1) / 2
	tail := max - 1 - head
	out := takeRunes(value, head) + "…" + lastRunes(value, tail)
	return out, max
}

func takeRunes(value string, count int) string {
	if count <= 0 {
		return ""
	}
	if stringDisplayWidth(value) <= count {
		return value
	}
	var out strings.Builder
	used := 0
	for _, r := range value {
		rw := runeDisplayWidth(r)
		if used+rw > count {
			break
		}
		out.WriteRune(r)
		used += rw
	}
	return out.String()
}

func lastRunes(value string, count int) string {
	if count <= 0 {
		return ""
	}
	if stringDisplayWidth(value) <= count {
		return value
	}
	runes := []rune(value)
	used := 0
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		rw := runeDisplayWidth(runes[i])
		if used+rw > count {
			break
		}
		used += rw
		start = i
	}
	return string(runes[start:])
}

func stringDisplayWidth(value string) int {
	total := 0
	for _, r := range value {
		total += runeDisplayWidth(r)
	}
	return total
}

func runeDisplayWidth(r rune) int {
	if r <= 0x7e {
		if r >= 32 {
			return 1
		}
		return 0
	}
	if r < 0xa0 {
		return 0
	}
	if isZeroWidthRune(r) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

// isZeroWidthRune fast-paths the common BMP ranges for combining marks
// (Mn/Me) and format characters (Cf). For runes outside these ranges we fall
// back to unicode.In, which is slower but correct for less common scripts.
//
// The ranges below are derived from Go's unicode tables and cover the vast
// majority of real-world non-ASCII content (Latin combining diacritics,
// Cyrillic/Greek/Hebrew/Arabic combining marks, Devanagari/Indic vowel signs
// and viramas, Thai/Lao tone marks, Hangul jamo, CJK variation selectors,
// zero-width joiner/non-joiner, and the BOM/RLM/LRM family).
func isZeroWidthRune(r rune) bool {
	switch {
	case r < 0x0300:
		return false
	case r >= 0x0300 && r <= 0x036f: // combining diacritical marks
		return true
	case r >= 0x0483 && r <= 0x0489: // Cyrillic combining
		return true
	case r >= 0x0591 && r <= 0x05bd: // Hebrew
		return true
	case r == 0x05bf || r == 0x05c1 || r == 0x05c2 || r == 0x05c4 || r == 0x05c5 || r == 0x05c7:
		return true
	case r >= 0x0600 && r <= 0x0605: // Arabic format
		return true
	case r >= 0x0610 && r <= 0x061a: // Arabic combining
		return true
	case r == 0x061c:
		return true
	case r >= 0x064b && r <= 0x065f: // Arabic diacritics
		return true
	case r == 0x0670:
		return true
	case r >= 0x06d6 && r <= 0x06dd: // Arabic small high marks
		return true
	case r >= 0x06df && r <= 0x06e4:
		return true
	case r >= 0x06e7 && r <= 0x06e8:
		return true
	case r >= 0x06ea && r <= 0x06ed:
		return true
	case r == 0x070f || r == 0x0711:
		return true
	case r >= 0x0730 && r <= 0x074a: // Syriac
		return true
	case r >= 0x07a6 && r <= 0x07b0: // Thaana
		return true
	case r >= 0x0900 && r <= 0x0903: // Devanagari signs/vowels
		return r == 0x0900 || r == 0x0901 || r == 0x0902
	case r >= 0x093a && r <= 0x094f: // Devanagari combining
		return r == 0x093a || r == 0x093c || (r >= 0x0941 && r <= 0x0948) || r == 0x094d || r == 0x0951 || r == 0x0952 || r == 0x0953 || r == 0x0954
	case r >= 0x0e31 && r <= 0x0e3a: // Thai
		return r == 0x0e31 || (r >= 0x0e34 && r <= 0x0e3a)
	case r >= 0x0e47 && r <= 0x0e4e:
		return true
	case r == 0x200b || r == 0x200c || r == 0x200d || r == 0x200e || r == 0x200f: // ZWSP/ZWNJ/ZWJ/LRM/RLM
		return true
	case r >= 0x202a && r <= 0x202e: // bidi controls
		return true
	case r >= 0x2060 && r <= 0x2064:
		return true
	case r >= 0x2066 && r <= 0x206f: // bidi isolates + format
		return true
	case r == 0xfeff: // BOM / ZWNBSP
		return true
	case r >= 0xfe00 && r <= 0xfe0f: // variation selectors
		return true
	case r >= 0xe0100 && r <= 0xe01ef: // variation selectors supplement
		return true
	}
	// Cold path for less common scripts. unicode.In merges the three ranges
	// into a single binary search instead of three separate ones.
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf)
}

// wrapCacheKey identifies a cached wrap result. Notes are immutable (NIP-01:
// the event ID is the SHA-256 of the canonical event), so for a given
// (content, width) the wrapped lines are stable forever. We hash content with
// FNV-1a and verify by storing the original content in the entry to guard
// against the (astronomically unlikely) hash collision.
type wrapCacheKey struct {
	hash  uint64
	width int
}

type wrapCacheEntry struct {
	content string
	lines   []string
}

// wrapCache is bounded by wrapCacheCapacity. Eviction is best-effort random;
// in steady state the working set is small (~30 visible notes × 2-3 widths)
// so we rarely evict at all.
const wrapCacheCapacity = 4096

var (
	wrapCacheMu      sync.RWMutex
	wrapCacheEntries = make(map[wrapCacheKey]wrapCacheEntry, wrapCacheCapacity)
)

func wrapCacheHash(content string, width int) wrapCacheKey {
	h := fnv.New64a()
	_, _ = h.Write([]byte(content))
	return wrapCacheKey{hash: h.Sum64(), width: width}
}

func wrapCacheGet(content string, width int) ([]string, bool) {
	if content == "" {
		return nil, false
	}
	key := wrapCacheHash(content, width)
	wrapCacheMu.RLock()
	entry, ok := wrapCacheEntries[key]
	wrapCacheMu.RUnlock()
	if !ok || entry.content != content {
		return nil, false
	}
	return entry.lines, true
}

func wrapCachePut(content string, width int, lines []string) {
	if content == "" {
		return
	}
	key := wrapCacheHash(content, width)
	wrapCacheMu.Lock()
	defer wrapCacheMu.Unlock()
	if len(wrapCacheEntries) >= wrapCacheCapacity {
		// Evict an arbitrary entry. Map iteration order is randomized by the
		// runtime, so this approximates a random eviction without needing
		// ordering metadata.
		for k := range wrapCacheEntries {
			delete(wrapCacheEntries, k)
			break
		}
	}
	wrapCacheEntries[key] = wrapCacheEntry{content: content, lines: lines}
}

func isWideRune(r rune) bool {
	if r < 0x1100 {
		return false
	}
	return (r >= 0x1100 && r <= 0x115f) ||
		r == 0x2329 ||
		r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6)
}
