package httpx

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/inconsolata"
	"golang.org/x/image/math/fixed"

	"ptxt-nstr/internal/nostrx"
)

// OG card dimensions and rendering knobs. We render at 1/3 of the target
// dimensions using a small bitmap font, then nearest-neighbor scale to the
// final 1200x630. The crisp pixel look is intentional and matches
// ptxt-nstr's text-first / ASCII-art visual identity.
const (
	ogImageWidth        = 1200
	ogImageHeight       = 630
	ogImageScale        = 3
	ogImageInternalW    = ogImageWidth / ogImageScale  // 400
	ogImageInternalH    = ogImageHeight / ogImageScale // 210
	ogImagePadX         = 12
	ogImagePadY         = 12
	ogImageHeaderHeight = 24 // pixel rows reserved for the author bar
	ogImageFooterHeight = 18 // pixel rows reserved for the date bar
	ogImageMaxLines     = 9
	ogImageRenderBudget = 5 * time.Second
)

// OG card colors. Dark background, soft pink/cream foreground that matches
// the site's accent palette without depending on the live CSS.
var (
	ogBackground       = color.RGBA{R: 0x17, G: 0x17, B: 0x17, A: 0xff}
	ogForeground       = color.RGBA{R: 0xff, G: 0xe6, B: 0xee, A: 0xff}
	ogAccent           = color.RGBA{R: 0xe3, G: 0x2a, B: 0x6d, A: 0xff}
	ogMuted            = color.RGBA{R: 0x9f, G: 0x9f, B: 0x9f, A: 0xff}
	ogBarBackground    = color.RGBA{R: 0x0a, G: 0x0a, B: 0x0a, A: 0xff}
	ogBorderForeground = color.RGBA{R: 0x66, G: 0x66, B: 0x66, A: 0xff}
)

// handleOGImage serves a generated PNG OpenGraph card for a given Nostr
// event. The event must be present in the local cache; we never fan out to
// relays from the OG path (it would be a relay-DDoS surface for any social
// crawler). Misses 404 with a short negative cache.
func (s *Server) handleOGImage(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.og_image", time.Now())
	raw := strings.TrimPrefix(r.URL.Path, "/og/")
	raw = strings.TrimSuffix(raw, ".png")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	id := resolveOGEventID(raw)
	if id == "" {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	if matchesETag(r, id) {
		writeNotModifiedLong(w, id)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ogImageRenderBudget)
	defer cancel()
	event := s.eventFromStore(ctx, id)
	if event == nil {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	profile := s.profile(ctx, event.PubKey)
	img, err := drawOGCard(*event, profile)
	if err != nil {
		slog.Warn("og image render failed", "id", id, "err", err)
		setNegativeCache(w)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	setContentAddressedCacheLong(w, id)
	if err := png.Encode(w, img); err != nil {
		slog.Warn("og image encode failed", "id", id, "err", err)
	}
}

// resolveOGEventID accepts the path segment from /og/<segment>.png and
// returns the canonical hex event id when it can be parsed as one of:
// nevent / note (NIP-19), or a bare 64-char hex string. Returns "" when
// the segment is not a recognizable event reference.
func resolveOGEventID(segment string) string {
	if segment == "" {
		return ""
	}
	if ref, err := nostrx.DecodeNIP27Reference(segment); err == nil {
		switch ref.Kind {
		case nostrx.NIP27KindNote, nostrx.NIP27KindNEvent:
			if ref.Event != "" {
				return strings.ToLower(ref.Event)
			}
		}
	}
	if isBare64Hex(segment) {
		return strings.ToLower(segment)
	}
	return ""
}

// drawOGCard renders the OG card for an event into an in-memory RGBA image.
// We render at 1/3 the final dimensions and then nearest-neighbor scale so
// the bitmap font stays crisp instead of getting smoothed into mush.
func drawOGCard(event nostrx.Event, profile nostrx.Profile) (image.Image, error) {
	internal := image.NewRGBA(image.Rect(0, 0, ogImageInternalW, ogImageInternalH))
	draw.Draw(internal, internal.Bounds(), &image.Uniform{C: ogBackground}, image.Point{}, draw.Src)

	bold := inconsolata.Bold8x16
	regular := inconsolata.Regular8x16
	advance := faceAdvance(bold)

	// Top border + author bar background.
	authorBarRect := image.Rect(0, 0, ogImageInternalW, ogImageHeaderHeight)
	draw.Draw(internal, authorBarRect, &image.Uniform{C: ogBarBackground}, image.Point{}, draw.Src)
	drawHorizontalLine(internal, ogImageHeaderHeight-1, ogBorderForeground)

	authorName := nostrx.DisplayName(profile)
	if authorName == "" {
		authorName = short(event.PubKey)
	}
	drawText(internal, bold, ogForeground, ogImagePadX, 16, sanitizeASCII(authorName))

	npubLabel := short(nostrx.EncodeNPub(event.PubKey))
	if npubLabel != "" {
		labelX := ogImageInternalW - ogImagePadX - advance*utf8.RuneCountInString(npubLabel)
		if labelX < ogImagePadX {
			labelX = ogImagePadX
		}
		drawText(internal, regular, ogMuted, labelX, 16, sanitizeASCII(npubLabel))
	}

	// Body content.
	contentTop := ogImageHeaderHeight + ogImagePadY
	contentBottom := ogImageInternalH - ogImageFooterHeight - ogImagePadY
	maxCols := (ogImageInternalW - 2*ogImagePadX) / advance
	if maxCols < 8 {
		maxCols = 8
	}
	body := normalizeBodyForOG(event.Content)
	lines := wrapBodyLines(body, maxCols, ogImageMaxLines)
	lineHeight := faceLineHeight(bold)
	for i, line := range lines {
		y := contentTop + (i+1)*lineHeight
		if y > contentBottom {
			break
		}
		drawText(internal, regular, ogForeground, ogImagePadX, y, sanitizeASCII(line))
	}

	// Footer with date + site name.
	footerTop := ogImageInternalH - ogImageFooterHeight
	footerRect := image.Rect(0, footerTop, ogImageInternalW, ogImageInternalH)
	draw.Draw(internal, footerRect, &image.Uniform{C: ogBarBackground}, image.Point{}, draw.Src)
	drawHorizontalLine(internal, footerTop, ogBorderForeground)
	dateStr := time.Unix(event.CreatedAt, 0).UTC().Format("2006-01-02")
	drawText(internal, regular, ogMuted, ogImagePadX, footerTop+13, dateStr)
	siteLabel := ogSiteName
	siteX := ogImageInternalW - ogImagePadX - advance*utf8.RuneCountInString(siteLabel)
	if siteX < ogImagePadX {
		siteX = ogImagePadX
	}
	drawText(internal, bold, ogAccent, siteX, footerTop+13, siteLabel)

	// Outer border to give it the framed look.
	drawRectOutline(internal, internal.Bounds(), ogBorderForeground)

	// Nearest-neighbor scale to the final 1200x630 so the pixel font stays
	// sharp instead of getting bilinear-smoothed into a blurry mess.
	final := image.NewRGBA(image.Rect(0, 0, ogImageWidth, ogImageHeight))
	xdraw.NearestNeighbor.Scale(final, final.Bounds(), internal, internal.Bounds(), xdraw.Src, nil)
	return final, nil
}

// drawText writes s at (x, baselineY) using the supplied face and color.
// Skips silently when the inputs are degenerate so the caller stays simple.
func drawText(dst draw.Image, face font.Face, c color.Color, x, baselineY int, s string) {
	if s == "" {
		return
	}
	d := &font.Drawer{
		Dst:  dst,
		Src:  &image.Uniform{C: c},
		Face: face,
		Dot:  fixed.P(x, baselineY),
	}
	d.DrawString(s)
}

// drawHorizontalLine fills a single horizontal pixel row across the image.
func drawHorizontalLine(dst *image.RGBA, y int, c color.Color) {
	bounds := dst.Bounds()
	if y < bounds.Min.Y || y >= bounds.Max.Y {
		return
	}
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		dst.Set(x, y, c)
	}
}

// drawRectOutline draws a one-pixel stroke around r. The rectangle must
// already be inside dst's bounds.
func drawRectOutline(dst *image.RGBA, r image.Rectangle, c color.Color) {
	if r.Empty() {
		return
	}
	for x := r.Min.X; x < r.Max.X; x++ {
		dst.Set(x, r.Min.Y, c)
		dst.Set(x, r.Max.Y-1, c)
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		dst.Set(r.Min.X, y, c)
		dst.Set(r.Max.X-1, y, c)
	}
}

// sanitizeASCII strips control characters and non-ASCII runes that the
// inconsolata bitmap font can't render. Replaced runes become a single
// '?' so the layout doesn't shift.
func sanitizeASCII(input string) string {
	if input == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(input))
	for _, r := range input {
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20:
			b.WriteByte(' ')
		case r > 0x7e:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeBodyForOG collapses runs of whitespace and trims leading or
// trailing space. Newlines are preserved as paragraph separators so the
// wrapper can break paragraphs the way the original note structured them.
func normalizeBodyForOG(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	out := strings.Builder{}
	out.Grow(len(content))
	var prev rune = -1
	for _, r := range content {
		if r == '\n' {
			if prev == '\n' {
				continue
			}
			out.WriteRune(r)
			prev = r
			continue
		}
		if unicode.IsSpace(r) {
			if prev == ' ' || prev == '\n' || prev == -1 {
				continue
			}
			out.WriteByte(' ')
			prev = ' '
			continue
		}
		out.WriteRune(r)
		prev = r
	}
	return strings.TrimSpace(out.String())
}

// wrapBodyLines splits content into at most maxLines lines no wider than
// maxCols columns, breaking on word boundaries when possible. The last
// line is suffixed with an ellipsis when content remains.
func wrapBodyLines(content string, maxCols, maxLines int) []string {
	if maxLines <= 0 || maxCols <= 0 {
		return nil
	}
	out := make([]string, 0, maxLines)
	for _, paragraph := range strings.Split(content, "\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			if len(out) > 0 && len(out) < maxLines {
				out = append(out, "")
			}
			continue
		}
		words := strings.Fields(paragraph)
		current := ""
		flush := func() bool {
			out = append(out, current)
			current = ""
			return len(out) >= maxLines
		}
		for _, word := range words {
			if utf8.RuneCountInString(word) > maxCols {
				if current != "" {
					if flush() {
						return finalizeWrappedLines(out, maxLines, content)
					}
				}
				for utf8.RuneCountInString(word) > maxCols {
					current = string([]rune(word)[:maxCols])
					if flush() {
						return finalizeWrappedLines(out, maxLines, content)
					}
					word = string([]rune(word)[maxCols:])
				}
				current = word
				continue
			}
			candidate := word
			if current != "" {
				candidate = current + " " + word
			}
			if utf8.RuneCountInString(candidate) > maxCols {
				if flush() {
					return finalizeWrappedLines(out, maxLines, content)
				}
				current = word
				continue
			}
			current = candidate
		}
		if current != "" {
			if flush() {
				return finalizeWrappedLines(out, maxLines, content)
			}
		}
	}
	return out
}

// faceAdvance returns the per-glyph horizontal advance in pixels for face,
// falling back to the inconsolata 8x16 default when the face is not the
// expected basicfont.Face type or carries an obviously bogus advance.
func faceAdvance(face font.Face) int {
	if bf, ok := face.(*basicfont.Face); ok && bf.Advance > 0 {
		return bf.Advance
	}
	return 8
}

// faceLineHeight returns inter-line spacing in pixels.
func faceLineHeight(face font.Face) int {
	if bf, ok := face.(*basicfont.Face); ok && bf.Height > 0 {
		return bf.Height
	}
	return 16
}

// finalizeWrappedLines applies the trailing ellipsis when wrapping ran out
// of vertical room before it ran out of content.
func finalizeWrappedLines(lines []string, maxLines int, original string) []string {
	if len(lines) == 0 {
		return lines
	}
	if utf8.RuneCountInString(strings.Join(lines, " ")) >= utf8.RuneCountInString(original) {
		return lines
	}
	last := lines[len(lines)-1]
	if !strings.HasSuffix(last, "…") {
		lines[len(lines)-1] = strings.TrimRight(last, " ") + "…"
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
}
