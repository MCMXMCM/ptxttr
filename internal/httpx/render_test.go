package httpx

import (
	"bytes"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
	templatesfs "ptxt-nstr/internal/templates"
	"ptxt-nstr/internal/thread"
)

func TestAsciiFillUsesRemainingWidth(t *testing.T) {
	got := asciiFill(20, "+- ", "alice", " -- ", "1m", " ", "[...]", " +")
	wantLen := 20 - stringDisplayWidth("+- alice -- 1m [...] +")
	if wantLen < 1 {
		wantLen = 1
	}
	if got != strings.Repeat("-", wantLen) {
		t.Fatalf("asciiFill() = %q, want %q", got, strings.Repeat("-", wantLen))
	}
}

func TestAsciiFillHasMinimumOneDash(t *testing.T) {
	got := asciiFill(8, "+- ", "very-long-author-label", " -- ", "20d", " ", "[...]", " +")
	if got != "-" {
		t.Fatalf("asciiFill() = %q, want one dash", got)
	}
}

func TestReactionBracketBlockCentersCount(t *testing.T) {
	got := reactionBracketBlock(2, "")
	if want := "[△] 2 [▽]"; got != want {
		t.Fatalf("reactionBracketBlock(2,\"\") = %q, want %q", got, want)
	}
}

func TestAsciiNoteFooterFill(t *testing.T) {
	const w = 60
	rb := reactionBracketBlock(0, "")
	replyLabel := "  2 rpls"
	fill := asciiNoteFooterFill(w, rb, replyLabel)
	left := "+-- " + rb + " "
	right := " " + replyLabel + " [reply] ---+"
	sum := stringDisplayWidth(left) + stringDisplayWidth(fill) + stringDisplayWidth(right)
	if sum != w {
		t.Fatalf("footer parts width = %d + %d + %d = %d want %d",
			stringDisplayWidth(left), stringDisplayWidth(fill), stringDisplayWidth(right), sum, w)
	}
}

func TestAsciiBoxLinePadsToFixedWidth(t *testing.T) {
	got := asciiBoxLine(20, "hello")
	want := "| hello            |"
	if got != want {
		t.Fatalf("asciiBoxLine() = %q, want %q", got, want)
	}
	if len([]rune(got)) != 20 {
		t.Fatalf("asciiBoxLine() length = %d, want 20", len([]rune(got)))
	}
}

func TestAsciiTextLinesWrapsContent(t *testing.T) {
	got := asciiTextLines("content of the reply here", 10)
	want := []string{"content of", "the reply", "here"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("asciiTextLines() = %#v, want %#v", got, want)
	}
}

// TestAsciiTextLinesClampsDegenerateWidth pins down the regression that
// dragged the production server to ~200% CPU and 35 GiB heap: when the
// caller passes width <= 0 (e.g. a fragment handler that forgot to
// populate BasePageData.AsciiWidth), the wrap helper used to emit one
// string per rune of the input. The clamp must substitute a sane width
// so the output stays bounded.
func TestAsciiTextLinesClampsDegenerateWidth(t *testing.T) {
	content := strings.Repeat("a", 8000)
	for _, w := range []int{-1, 0, 1, 4, 7} {
		got := asciiTextLines(content, w)
		// At fallback width the output must be roughly content/width lines,
		// not anywhere near len(content) lines.
		if len(got) > 200 {
			t.Fatalf("asciiTextLines(width=%d) produced %d lines, expected clamped output", w, len(got))
		}
	}
}

// TestAsciiTextLinesCapsLargeInput pins down the input-size guardrail so a
// rogue 1 MiB note can't produce hundreds of thousands of wrapped strings.
func TestAsciiTextLinesCapsLargeInput(t *testing.T) {
	content := strings.Repeat("word ", 200_000) // ~1 MiB
	got := asciiTextLines(content, asciiWidthDesktop)
	if len(got) > maxWrapOutputLines {
		t.Fatalf("asciiTextLines() returned %d lines, want <= %d", len(got), maxWrapOutputLines)
	}
}

// TestAsciiTextLinesCachesByContentAndWidth makes sure repeated calls with
// the same (content, width) reuse the wrap result instead of re-doing the
// work. Returning the same slice header is a strong signal of cache reuse.
func TestAsciiTextLinesCachesByContentAndWidth(t *testing.T) {
	content := "hello world this is a cached note"
	first := asciiTextLines(content, 32)
	second := asciiTextLines(content, 32)
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("expected non-empty wrap output")
	}
	if &first[0] != &second[0] {
		t.Fatalf("expected wrap cache to return the same slice for identical input")
	}
}

func TestReplyTextWidthUsesAvailableCardWidth(t *testing.T) {
	if got := replyTextWidth(120); got != 112 {
		t.Fatalf("replyTextWidth(120) = %d, want 112", got)
	}
	if got := replyTextWidth(24); got != 20 {
		t.Fatalf("replyTextWidth(24) = %d, want minimum 20", got)
	}
}

func TestAsciiReplyPadLineMatchesWrappedColumnWidth(t *testing.T) {
	w := 120
	want := clampRenderWidth(replyTextWidth(w))
	got := len(asciiReplyPadLine(w))
	if got != want {
		t.Fatalf("len(asciiReplyPadLine(%d)) = %d, want %d", w, got, want)
	}
}

func TestIsLastIndex(t *testing.T) {
	if !isLastIndex(2, 3) {
		t.Fatal("isLastIndex(2, 3) = false, want true")
	}
	if isLastIndex(1, 3) {
		t.Fatal("isLastIndex(1, 3) = true, want false")
	}
}

func TestRelTimeUsesCompactMonthAndYearUnits(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		age  time.Duration
		want string
	}{
		{name: "minutes", age: 5 * time.Minute, want: "5m"},
		{name: "hours", age: 6 * time.Hour, want: "6h"},
		{name: "days", age: 29 * 24 * time.Hour, want: "29d"},
		{name: "months", age: 45 * 24 * time.Hour, want: "1mo"},
		{name: "years", age: 400 * 24 * time.Hour, want: "1y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := now.Add(-tt.age).Unix()
			if got := relTime(ts); got != tt.want {
				t.Fatalf("relTime(%v) = %q, want %q", tt.age, got, tt.want)
			}
		})
	}
}

func TestAsciiAuthorTruncatesToFitWidth(t *testing.T) {
	pubkey := strings.Repeat("a", 64)
	profiles := map[string]nostrx.Profile{
		pubkey: {PubKey: pubkey, Display: "very-long-display-name"},
	}
	got := asciiAuthor(30, profiles, pubkey)
	if stringDisplayWidth(got) > 30-stringDisplayWidth("+-  --  [...] +") {
		t.Fatalf("asciiAuthor() = %q is too long", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("asciiAuthor() = %q, want truncated label", got)
	}
}

func TestAsciiBoxLinePadsToFixedWidthWithWideRunes(t *testing.T) {
	got := asciiBoxLine(20, "公開しました")
	if stringDisplayWidth(got) != 20 {
		t.Fatalf("asciiBoxLine() display width = %d, want 20", stringDisplayWidth(got))
	}
	if !strings.HasPrefix(got, "| ") || !strings.HasSuffix(got, " |") {
		t.Fatalf("asciiBoxLine() = %q, want boxed line", got)
	}
}

func TestAsciiTextLinesWrapsWideRunes(t *testing.T) {
	got := asciiTextLines("日本語 日本語 日本語", 8)
	want := []string{"日本語", "日本語", "日本語"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("asciiTextLines() = %#v, want %#v", got, want)
	}
}

func TestAuthorLabelUsesDisplayNameOnlyWhenAvailable(t *testing.T) {
	pubkey := strings.Repeat("a", 64)
	profiles := map[string]nostrx.Profile{
		pubkey: {PubKey: pubkey, Display: "alice"},
	}
	label := authorLabel(profiles, pubkey)
	if label != "alice" {
		t.Fatalf("authorLabel() = %q, want display name only", label)
	}
}

func TestAuthorLabelFallsBackToTruncatedID(t *testing.T) {
	pubkey := strings.Repeat("a", 64)
	profiles := map[string]nostrx.Profile{
		pubkey: {PubKey: pubkey},
	}
	label := authorLabel(profiles, pubkey)
	if label != "aaaaaaaaaaaa" {
		t.Fatalf("authorLabel() = %q, want outline-style truncated id", label)
	}
}

func TestAsciiWidthForRequestWithQuery(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/thread/abc?ascii_w=42&fragment=focus", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)")
	if got := s.asciiWidthForRequestWithQuery(req); got != asciiWidthMobile {
		t.Fatalf("asciiWidthForRequestWithQuery() = %d, want %d (ascii_w overrides desktop UA)", got, asciiWidthMobile)
	}
	userReq := httptest.NewRequest("GET", "/u/npub1?ascii_w=120&fragment=posts", nil)
	userReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)")
	if got := s.asciiWidthForUserRequestWithQuery(userReq); got != asciiWidthUserDesktop {
		t.Fatalf("asciiWidthForUserRequestWithQuery(ascii_w=120) = %d, want %d", got, asciiWidthUserDesktop)
	}
	bad := httptest.NewRequest("GET", "/thread/x?ascii_w=7", nil)
	bad.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)")
	if got := s.asciiWidthForRequestWithQuery(bad); got != asciiWidthDesktop {
		t.Fatalf("asciiWidthForRequestWithQuery invalid ascii_w = %d, want %d", got, asciiWidthDesktop)
	}
}

func TestAsciiWidthForRequest(t *testing.T) {
	s := &Server{}
	tests := []struct {
		name   string
		ua     string
		mobile string
		want   int
	}{
		{
			name:   "client hint mobile",
			ua:     "Mozilla/5.0",
			mobile: "?1",
			want:   asciiWidthMobile,
		},
		{
			name: "tablet android",
			ua:   "Mozilla/5.0 (Linux; Android 14; SM-X816B) AppleWebKit/537.36",
			want: asciiWidthTablet,
		},
		{
			name: "iphone",
			ua:   "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)",
			want: asciiWidthMobile,
		},
		{
			name: "desktop",
			ua:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)",
			want: asciiWidthDesktop,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("User-Agent", tt.ua)
			if tt.mobile != "" {
				req.Header.Set("Sec-CH-UA-Mobile", tt.mobile)
			}
			if got := s.asciiWidthForRequest(req); got != tt.want {
				t.Fatalf("asciiWidthForRequest() = %d, want %d", got, tt.want)
			}
		})
	}
}

// Regression: home must always emit data-load-more so feed.js can bind even when
// SSR shows notes but HasMore is false (deferred shell / starter snapshot).
func TestHomeIncludesLoadMoreWhenHasMoreFalseWithNonEmptyFeed(t *testing.T) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(templatesfs.FS, "*.html")
	if err != nil {
		t.Fatal(err)
	}
	pk := strings.Repeat("b", 64)
	ev := nostrx.Event{
		ID:        "deadbeefcafe",
		PubKey:    pk,
		CreatedAt: 1234567890,
		Kind:      1,
		Content:   "short note",
	}
	data := FeedPageData{
		BasePageData: BasePageData{
			Title:      "Nostr Feed",
			Active:     "feed",
			PageClass:  "feed-shell",
			AsciiWidth: asciiWidthMobile,
		},
		Feed:              []nostrx.Event{ev},
		Profiles:          map[string]nostrx.Profile{pk: {PubKey: pk, Display: "bob"}},
		ReplyCounts:       map[string]int{ev.ID: 0},
		ReferencedEvents:  map[string]nostrx.Event{},
		ReactionTotals:    map[string]int{},
		ReactionViewers:   map[string]string{},
		HasMore:           false,
		Cursor:            0,
		CursorID:          "",
		DefaultFeed:       true,
		FeedSort:          "recent",
		TrendingTimeframe: "24h",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "home", data); err != nil {
		t.Fatalf("execute home: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `data-load-more`) {
		t.Fatalf("home template missing data-load-more when HasMore=false and len(Feed)>0")
	}
	if !strings.Contains(out, `id="note-deadbeefcafe"`) {
		t.Fatalf("expected rendered feed note id in output")
	}
}

func TestAsciiTemplatesExecute(t *testing.T) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(templatesfs.FS, "*.html")
	if err != nil {
		t.Fatal(err)
	}
	event := nostrx.Event{
		ID:        "event",
		PubKey:    strings.Repeat("a", 64),
		CreatedAt: timeNowForTest(),
		Content:   "hello from a note",
	}
	data := map[string]any{
		"Event":      event,
		"Profiles":   map[string]nostrx.Profile{event.PubKey: {PubKey: event.PubKey, Display: "alice"}},
		"Width":      asciiWidthMobile,
		"ReplyCount": 2,
	}
	var note bytes.Buffer
	if err := tmpl.ExecuteTemplate(&note, "note", data); err != nil {
		t.Fatalf("execute note template: %v", err)
	}
	if !strings.Contains(note.String(), "+--") || !strings.Contains(note.String(), "| hello from a note") {
		t.Fatalf("note template did not render ascii frame: %s", note.String())
	}
	if !strings.Contains(note.String(), `data-ascii-avatar="`) {
		t.Fatalf("note template did not render data-ascii-avatar: %s", note.String())
	}
	if !strings.Contains(note.String(), `class="note-feed-avatar"`) {
		t.Fatalf("note template did not render note-feed-avatar: %s", note.String())
	}

	node := thread.Node{Event: event, Depth: 1, ParentID: "parent"}
	var reply bytes.Buffer
	if err := tmpl.ExecuteTemplate(&reply, "comment", map[string]any{
		"Node":       node,
		"Profiles":   data["Profiles"],
		"SelectedID": "",
		"RootID":     "root",
		"Width":      asciiWidthMobile,
		"IsLast":     true,
	}); err != nil {
		t.Fatalf("execute comment template: %v", err)
	}
	out := reply.String()
	if !strings.Contains(out, "     hello from a note") {
		t.Fatalf("comment template did not render reply content: %s", out)
	}
	if !strings.Contains(out, `class="comment-avatar"`) {
		t.Fatalf("comment template did not render avatar element: %s", out)
	}
	if strings.Contains(out, `data-collapse="`) {
		t.Fatalf("comment template should not render collapse control: %s", out)
	}
	if strings.Contains(out, `/thread/parent?back=root&back_note=event`) {
		t.Fatalf("comment template should not render parent thread link: %s", out)
	}
	if strings.Contains(out, `[select]`) {
		t.Fatalf("comment template should not render [select]: %s", out)
	}

	var selectedReply bytes.Buffer
	if err := tmpl.ExecuteTemplate(&selectedReply, "comment", map[string]any{
		"Node":       node,
		"Profiles":   data["Profiles"],
		"SelectedID": "event",
		"RootID":     "root",
		"Width":      asciiWidthMobile,
		"IsLast":     true,
	}); err != nil {
		t.Fatalf("execute selected comment template: %v", err)
	}
	if strings.Contains(selectedReply.String(), `[select]`) {
		t.Fatalf("selected comment should not render [select]: %s", selectedReply.String())
	}
}

func TestRenderMarkdownSupportsCommonStyles(t *testing.T) {
	html := string(renderMarkdown("# Heading\n\nA *plain* list:\n\n- one\n- two\n\n`code`"))
	if !strings.Contains(html, "<h1>Heading</h1>") {
		t.Fatalf("markdown heading missing: %s", html)
	}
	if !strings.Contains(html, "<em>plain</em>") {
		t.Fatalf("markdown emphasis missing: %s", html)
	}
	if !strings.Contains(html, "<li>one</li>") || !strings.Contains(html, "<li>two</li>") {
		t.Fatalf("markdown list missing: %s", html)
	}
	if !strings.Contains(html, "<code>code</code>") {
		t.Fatalf("markdown inline code missing: %s", html)
	}
}

func TestRenderMarkdownDoesNotRenderRawHTML(t *testing.T) {
	html := string(renderMarkdown("<script>alert('xss')</script>\n\nParagraph"))
	if strings.Contains(html, "<script>") {
		t.Fatalf("raw html rendered unexpectedly: %s", html)
	}
	if !strings.Contains(html, "Paragraph") {
		t.Fatalf("expected paragraph output, got: %s", html)
	}
}

func TestRenderMarkdownLinkifiesNIP27ProfileAndEventRefs(t *testing.T) {
	pubkey := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	eventID := strings.Repeat("c", 64)
	content := strings.Join([]string{
		"hello",
		"nostr:" + nostrx.EncodeNPub(pubkey),
		"and",
		"nostr:" + nostrx.EncodeNEvent(eventID, pubkey),
	}, " ")
	html := string(renderMarkdown(content))
	if !strings.Contains(html, `/u/`+pubkey) {
		t.Fatalf("profile ref was not linkified: %s", html)
	}
	if !strings.Contains(html, `/thread/`+eventID) {
		t.Fatalf("event ref was not linkified: %s", html)
	}
}

func TestLinkifyNostrReferencesLeavesInvalidReferenceUntouched(t *testing.T) {
	input := "bad nostr:npub1xyz stays as-is"
	got := linkifyNostrReferences(input)
	if got != input {
		t.Fatalf("linkifyNostrReferences() = %q, want %q", got, input)
	}
}

func TestBioLinkHTMLHashtagAnchors(t *testing.T) {
	html := string(bioLinkHTML("Coder #golang and #Nostr fan"))
	if !strings.Contains(html, `href="/tag/golang"`) || !strings.Contains(html, `href="/tag/Nostr"`) {
		t.Fatalf("expected tag links: %s", html)
	}
	if strings.Contains(html, "<script") {
		t.Fatalf("unexpected script-like content: %s", html)
	}
}

func TestNpubGridHTMLCheckerboardAndRows(t *testing.T) {
	const pk = "fa984bd7dbb282f07e16e7ae87b26a2a7b9b90b7246a44771f0cf5ae58018f52"
	got := string(npubGridHTML(pk))
	if nostrx.EncodeNPub(pk) == "" {
		t.Fatal("EncodeNPub returned empty")
	}
	if !strings.Contains(got, `class="profile-npub-grid"`) {
		t.Fatalf("missing grid: %s", got)
	}
	rowOpen := strings.Count(got, `class="profile-npub-grid-row"`)
	if rowOpen != 4 {
		t.Fatalf("want 4 rows for standard npub, got %d: %s", rowOpen, got)
	}
	if !strings.Contains(got, `profile-npub-cell--emph">npub`) {
		t.Fatalf("first quartet should be emphasized: %s", got)
	}
	if !strings.Contains(got, `</span><span class="profile-npub-cell">`) || !strings.Contains(got, `npub</span><span class="profile-npub-cell">`) {
		t.Fatalf("second quartet should follow npub with non-emphasized cell: %s", got)
	}
	if strings.Count(got, "profile-npub-cell--emph") != 8 {
		t.Fatalf("want 8 emphasized cells in 4x4 checkerboard, got %d: %s",
			strings.Count(got, "profile-npub-cell--emph"), got)
	}
}

func TestHexGridHTMLRowsAndCheckerboard(t *testing.T) {
	const pk = "fa984bd7dbb282f07e16e7ae87b26a2a7b9b90b7246a44771f0cf5ae58018f52"
	got := string(hexGridHTML(pk))
	if !strings.Contains(got, `class="profile-npub-grid"`) {
		t.Fatalf("missing grid: %s", got)
	}
	if strings.Count(got, `class="profile-npub-grid-row"`) != 4 {
		t.Fatalf("want 4 rows for 64-char hex: %s", got)
	}
	if !strings.Contains(got, `profile-npub-cell--emph">fa98`) {
		t.Fatalf("first quartet should be emphasized: %s", got)
	}
}

func timeNowForTest() int64 {
	return time.Now().Unix()
}
