package httpx

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
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
	img, err := drawOGCard(event, profile)
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
	if _, err := drawOGCard(event, nostrx.Profile{}); err != nil {
		t.Fatalf("drawOGCard with empty content err: %v", err)
	}
}
