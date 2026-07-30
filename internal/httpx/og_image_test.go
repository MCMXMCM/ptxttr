package httpx

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"

	"golang.org/x/image/font/inconsolata"
)

func TestResolveOGEventIDFromHex(t *testing.T) {
	got := resolveOGEventID(testHexEventID)
	if got != testHexEventID {
		t.Fatalf("resolveOGEventID(hex) = %q, want %q", got, testHexEventID)
	}
}

func TestResolveOGEventIDFromNEvent(t *testing.T) {
	nevent := nostrx.EncodeNEvent(testHexEventID, testHexPubkey)
	got := resolveOGEventID(nevent)
	if got != testHexEventID {
		t.Fatalf("resolveOGEventID(nevent) = %q, want %q", got, testHexEventID)
	}
}

func TestResolveOGEventIDRejectsProfile(t *testing.T) {
	npub := nostrx.EncodeNPub(testHexPubkey)
	got := resolveOGEventID(npub)
	if got != "" {
		t.Fatalf("resolveOGEventID(npub) = %q, want empty (only events allowed)", got)
	}
}

func TestResolveOGEventIDRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "abc", "favicon.ico", "1234"} {
		if got := resolveOGEventID(in); got != "" {
			t.Fatalf("resolveOGEventID(%q) = %q, want empty", in, got)
		}
	}
}

func TestSanitizeASCIIStripsControlsAndNonASCII(t *testing.T) {
	// Tab, newline and 0x00 all map to space (control replacement); the
	// \u00e9 (non-ASCII) maps to a single ?. Adjacent control chars produce
	// adjacent spaces; we don't try to coalesce them here because the
	// caller may want layout-stable output for fixed-width rendering.
	got := sanitizeASCII("Hello\tWorld\n\x00emoji\u00e9")
	want := "Hello World  emoji?"
	if got != want {
		t.Fatalf("sanitizeASCII = %q, want %q", got, want)
	}
}

func TestNormalizeBodyForOGCollapsesWhitespace(t *testing.T) {
	got := normalizeBodyForOG("Hello\n\n\nWorld\t \t with   spaces\r\nand carriage")
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "with spaces") {
		t.Fatalf("normalizeBodyForOG = %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("expected single-space runs, got %q", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Fatalf("expected single-newline runs, got %q", got)
	}
}

func TestWrapBodyLinesBreaksOnWordBoundaries(t *testing.T) {
	lines := wrapBodyLines("hello world from a Nostr note", 12, 4)
	if len(lines) == 0 {
		t.Fatal("wrapBodyLines returned no lines")
	}
	for _, line := range lines {
		if len(line) > 12 {
			t.Fatalf("line %q exceeds 12 cols", line)
		}
	}
}

func TestWrapBodyLinesAddsEllipsisOnOverflow(t *testing.T) {
	long := strings.Repeat("word ", 200)
	lines := wrapBodyLines(long, 20, 3)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if !strings.HasSuffix(lines[2], "…") {
		t.Fatalf("expected last line to end with …, got %q", lines[2])
	}
}

func TestWrapBodyLinesPreservesParagraphBreaks(t *testing.T) {
	lines := wrapBodyLines("first\nsecond", 30, 5)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %v", lines)
	}
	if lines[0] != "first" || lines[1] != "second" {
		t.Fatalf("paragraph break not preserved: %v", lines)
	}
}

func TestDrawOGCardProducesPNG(t *testing.T) {
	event := nostrx.Event{
		ID:        testHexEventID,
		PubKey:    testHexPubkey,
		Kind:      nostrx.KindTextNote,
		CreatedAt: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC).Unix(),
		Content:   "Hello world from a test Nostr note. This should render onto an OG card.",
	}
	profile := nostrx.Profile{PubKey: testHexPubkey, Display: "Alice"}
	img, err := drawOGCard(ogCardData{
		Event:    event,
		Profile:  profile,
		Profiles: map[string]nostrx.Profile{testHexPubkey: profile},
	})
	if err != nil {
		t.Fatalf("drawOGCard err: %v", err)
	}
	if img.Bounds().Dx() != ogImageWidth || img.Bounds().Dy() != ogImageHeight {
		t.Fatalf("size = %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), ogImageWidth, ogImageHeight)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode err: %v", err)
	}
	if buf.Len() < 1024 {
		t.Fatalf("png too small (%d bytes)", buf.Len())
	}
	// PNG signature must be present.
	if !bytes.HasPrefix(buf.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("output is not a PNG")
	}
}

func TestDrawOGCardHandlesEmptyContent(t *testing.T) {
	event := nostrx.Event{
		ID:        testHexEventID,
		PubKey:    testHexPubkey,
		CreatedAt: time.Now().Unix(),
		Content:   "",
	}
	if _, err := drawOGCard(ogCardData{Event: event, Profile: nostrx.Profile{}, Profiles: map[string]nostrx.Profile{}}); err != nil {
		t.Fatalf("drawOGCard with empty content err: %v", err)
	}
}

func TestOGBodyTextForRepostUsesReferencedNote(t *testing.T) {
	refID := strings.Repeat("1", 64)
	refPubKey := strings.Repeat("2", 64)
	repost := nostrx.Event{
		ID:      testHexEventID,
		PubKey:  testHexPubkey,
		Kind:    nostrx.KindRepost,
		Content: `{"id":"` + refID + `","pubkey":"` + refPubKey + `","kind":1,"content":"embedded content should not be rendered raw"}`,
		Tags:    [][]string{{"e", refID}},
	}
	ref := nostrx.Event{
		ID:      refID,
		PubKey:  refPubKey,
		Kind:    nostrx.KindTextNote,
		Content: "referenced repost body",
	}
	body := ogBodyText(ogCardData{
		Event:     repost,
		Profiles:  map[string]nostrx.Profile{refPubKey: {PubKey: refPubKey, Display: "Bob"}},
		Reference: &ref,
	})
	if !strings.Contains(body, "Bob") {
		t.Fatalf("body = %q, want repost lead", body)
	}
	if !strings.Contains(body, "referenced repost body") {
		t.Fatalf("body = %q, want referenced content", body)
	}
	if strings.Contains(body, `"kind":1`) {
		t.Fatalf("body leaked raw embedded repost json: %q", body)
	}
}

func TestOGBodyTextForReplyIncludesParentSnippet(t *testing.T) {
	parentID := strings.Repeat("3", 64)
	parentPubKey := strings.Repeat("4", 64)
	reply := nostrx.Event{
		ID:      testHexEventID,
		PubKey:  testHexPubkey,
		Kind:    nostrx.KindTextNote,
		Content: "child reply body",
		Tags: [][]string{
			{"e", parentID, "", "root"},
			{"e", parentID, "", "reply"},
			{"p", parentPubKey},
		},
	}
	parent := nostrx.Event{
		ID:      parentID,
		PubKey:  parentPubKey,
		Kind:    nostrx.KindTextNote,
		Content: "parent note body",
	}
	body := ogBodyText(ogCardData{
		Event:    reply,
		Profiles: map[string]nostrx.Profile{parentPubKey: {PubKey: parentPubKey, Display: "Carol"}},
		Parent:   &parent,
	})
	if !strings.Contains(body, "Replying to @Carol") {
		t.Fatalf("body = %q, want reply lead", body)
	}
	if !strings.Contains(body, "parent note body") || !strings.Contains(body, "child reply body") {
		t.Fatalf("body = %q, want parent + child content", body)
	}
}

func TestOGBodyTextForQuoteStripsRawNeventFromMainText(t *testing.T) {
	refID := strings.Repeat("5", 64)
	refPubKey := strings.Repeat("6", 64)
	quoteCode := nostrx.EncodeNEvent(refID, refPubKey)
	quote := nostrx.Event{
		ID:      testHexEventID,
		PubKey:  testHexPubkey,
		Kind:    nostrx.KindTextNote,
		Content: "got this one wrong stay humble and stack sats nostr:" + quoteCode,
		Tags:    [][]string{{"q", refID}},
	}
	ref := nostrx.Event{
		ID:      refID,
		PubKey:  refPubKey,
		Kind:    nostrx.KindTextNote,
		Content: "quoted note body",
	}
	body := ogBodyText(ogCardData{
		Event:     quote,
		Profiles:  map[string]nostrx.Profile{refPubKey: {PubKey: refPubKey, Display: "Odell"}},
		Reference: &ref,
	})
	if strings.Contains(body, quoteCode) {
		t.Fatalf("body leaked raw nevent: %q", body)
	}
	if !strings.Contains(body, "got this one wrong stay humble and stack sats") {
		t.Fatalf("body = %q, want main text", body)
	}
	if !strings.Contains(body, "Odell") || !strings.Contains(body, "quoted note body") {
		t.Fatalf("body = %q, want quoted section", body)
	}
}

func TestBuildOGThreadLinesKeepsNestedRightBorder(t *testing.T) {
	refPubKey := strings.Repeat("6", 64)
	lines := buildOGThreadLines(ogCardData{
		Event: nostrx.Event{
			ID:        testHexEventID,
			PubKey:    testHexPubkey,
			Kind:      nostrx.KindTextNote,
			CreatedAt: time.Now().Add(-3 * time.Hour).Unix(),
			Content:   "got this one wrong stay humble and stack sats",
			Tags:      [][]string{{"q", strings.Repeat("5", 64)}},
		},
		Profiles: map[string]nostrx.Profile{
			testHexPubkey: {PubKey: testHexPubkey, Display: "Odell"},
			refPubKey:     {PubKey: refPubKey, Display: "Odell"},
		},
		Reference: &nostrx.Event{
			ID:      strings.Repeat("5", 64),
			PubKey:  refPubKey,
			Kind:    nostrx.KindTextNote,
			Content: "quoted note body",
		},
	}, inconsolata.Bold8x16, inconsolata.Regular8x16, ogImageMaxCols)
	found := false
	for _, line := range lines {
		if strings.Contains(line.Text, "|  +-- Odell --") {
			found = true
			if !strings.HasSuffix(line.Text, "|") {
				t.Fatalf("nested quote line lost outer right border: %q", line.Text)
			}
			if !strings.Contains(line.Text, "+") {
				t.Fatalf("nested quote line missing inner box corner: %q", line.Text)
			}
		}
	}
	if !found {
		t.Fatalf("expected quoted note header line in %v", lines)
	}
	if len(lines) >= ogImageMaxRows+1 {
		t.Fatalf("expected compact layout, got %d lines", len(lines))
	}
}

func TestBuildOGThreadLinesFormatsReplyAsFocusedThread(t *testing.T) {
	parentID := strings.Repeat("3", 64)
	parentPubKey := strings.Repeat("4", 64)
	reply := nostrx.Event{
		ID:        testHexEventID,
		PubKey:    testHexPubkey,
		Kind:      nostrx.KindTextNote,
		CreatedAt: time.Now().Add(-12 * time.Hour).Unix(),
		Content:   "John Wick but it's bees.",
		Tags: [][]string{
			{"e", parentID, "", "root"},
			{"e", parentID, "", "reply"},
			{"p", parentPubKey},
		},
	}
	parent := nostrx.Event{
		ID:        parentID,
		PubKey:    parentPubKey,
		Kind:      nostrx.KindTextNote,
		CreatedAt: time.Now().Add(-21 * time.Hour).Unix(),
		Content:   "What's it about",
	}
	lines := buildOGThreadLines(ogCardData{
		Event: reply,
		Profiles: map[string]nostrx.Profile{
			testHexPubkey: {PubKey: testHexPubkey, Display: "Jay"},
			parentPubKey:  {PubKey: parentPubKey, Display: "MAHDOOD"},
		},
		Parent: &parent,
	}, inconsolata.Bold8x16, inconsolata.Regular8x16, ogImageMaxCols)

	var parentHeader, parentBody, replyHeader, replyBody int = -1, -1, -1, -1
	for i, line := range lines {
		switch {
		case strings.Contains(line.Text, "MAHDOOD --"):
			parentHeader = i
		case strings.Contains(line.Text, "What's it about"):
			parentBody = i
		case strings.Contains(line.Text, "Jay --"):
			replyHeader = i
		case strings.Contains(line.Text, "John Wick but it's bees."):
			replyBody = i
		}
		if strings.Contains(line.Text, "Replying to") {
			t.Fatalf("reply preview used repost-style nested context: %q", line.Text)
		}
	}
	if !(parentHeader >= 0 && parentHeader < parentBody && parentBody < replyHeader && replyHeader < replyBody) {
		t.Fatalf("reply thread order = parent header %d, parent body %d, reply header %d, reply body %d; lines=%v", parentHeader, parentBody, replyHeader, replyBody, lines)
	}
	if !strings.Contains(lines[replyHeader].Text, "+") {
		t.Fatalf("focused reply header missing right-edge corner: %q", lines[replyHeader].Text)
	}
	if len(lines) > ogImageMaxRows {
		t.Fatalf("reply layout exceeds row budget: got %d lines", len(lines))
	}
}

func TestOGHeaderLayoutCentersAvatarAndAddsTopInset(t *testing.T) {
	rect := image.Rect(24, 10, 24+360, 10+196)
	face := inconsolata.Regular8x16

	noAvatarBaseline := ogHeaderBaselineY(rect, face, false)
	withAvatarBaseline := ogHeaderBaselineY(rect, face, true)
	if withAvatarBaseline <= noAvatarBaseline {
		t.Fatalf("avatar baseline = %d, want > no-avatar baseline %d", withAvatarBaseline, noAvatarBaseline)
	}

	headerCenter := ogHeaderCenterY(rect, face, true)
	avatarTop := headerCenter - (ogImageAvatarSize / 2)
	avatarCenter := avatarTop + (ogImageAvatarSize / 2)
	if avatarCenter != headerCenter {
		t.Fatalf("avatar center = %d, want header center %d", avatarCenter, headerCenter)
	}

	headerTop := withAvatarBaseline - faceAscent(face)
	headerBottom := withAvatarBaseline + faceDescent(face)
	headerMid := headerTop + ((headerBottom - headerTop) / 2)
	if headerMid != headerCenter {
		t.Fatalf("header mid = %d, want header center %d", headerMid, headerCenter)
	}

	if avatarTop <= rect.Min.Y {
		t.Fatalf("avatar top = %d, want padding above rect min %d", avatarTop, rect.Min.Y)
	}
}
