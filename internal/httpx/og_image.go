package httpx

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"log/slog"
	"net/http"
	"strconv"
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
	"ptxt-nstr/internal/thread"
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
	ogImagePadY         = 10
	ogImageAvatarSize   = 24
	ogImageAvatarCols   = 3
	ogImageMinCols      = 44
	ogImageMaxCols      = 47
	ogImageMaxRows      = 11
	ogImageRenderBudget = 15 * time.Second
)

// OG card colors. Dark background, soft pink/cream foreground that matches
// the site's accent palette without depending on the live CSS.
var (
	ogBackground       = color.RGBA{R: 0x17, G: 0x17, B: 0x17, A: 0xff}
	ogForeground       = color.RGBA{R: 0xff, G: 0xe6, B: 0xee, A: 0xff}
	ogAccent           = color.RGBA{R: 0xe3, G: 0x2a, B: 0x6d, A: 0xff}
	ogMuted            = color.RGBA{R: 0x9f, G: 0x9f, B: 0x9f, A: 0xff}
	ogAuthorAccent     = color.RGBA{R: 0x7d, G: 0xb7, B: 0xff, A: 0xff}
	ogBorderForeground = color.RGBA{R: 0x66, G: 0x66, B: 0x66, A: 0xff}
	ogPanelBackground  = color.RGBA{R: 0x1b, G: 0x1c, B: 0x1e, A: 0xff}
	ogChildBackground  = color.RGBA{R: 0x1d, G: 0x1d, B: 0x1d, A: 0xff}
)

type ogCardData struct {
	Event     nostrx.Event
	Profile   nostrx.Profile
	Profiles  map[string]nostrx.Profile
	Parent    *nostrx.Event
	Reference *nostrx.Event
	Avatar    image.Image
}

type ogNestedBlock struct {
	Title    string
	Body     string
	Inset    int
	MaxLines int
	Accent   color.Color
}

type ogTextSpan struct {
	Col   int
	Text  string
	Color color.Color
	Face  font.Face
}

type ogRenderedLine struct {
	Text  string
	Face  font.Face
	Color color.Color
	Spans []ogTextSpan
}

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
	data := s.ogCardData(ctx, *event)
	data.Avatar = s.ogAvatarImage(ctx, data.Profile)
	img, err := drawOGCard(data)
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

func (s *Server) ogCardData(ctx context.Context, event nostrx.Event) ogCardData {
	data := ogCardData{
		Event:    event,
		Profile:  s.profile(ctx, event.PubKey),
		Profiles: map[string]nostrx.Profile{},
	}

	if replyContextVisible(event) {
		parentID := ogParentEventID(event)
		if parentID != "" {
			data.Parent = s.eventFromStore(ctx, parentID)
		}
	}

	var referenced map[string]nostrx.Event
	referenced = s.referencedEventsForFromStore(ctx, []nostrx.Event{event})
	if refID := referencedEventID(event); refID != "" {
		if ref, ok := referenced[refID]; ok {
			refCopy := ref
			data.Reference = &refCopy
		}
	}

	events := []nostrx.Event{event}
	if data.Parent != nil {
		events = append(events, *data.Parent)
	}
	if data.Reference != nil {
		events = append(events, *data.Reference)
	}
	data.Profiles = s.profilesFor(ctx, events)
	if _, ok := data.Profiles[event.PubKey]; !ok && data.Profile.PubKey != "" {
		data.Profiles[event.PubKey] = data.Profile
	}
	return data
}

func (s *Server) ogAvatarImage(ctx context.Context, profile nostrx.Profile) image.Image {
	upstream := strings.TrimSpace(profile.Picture)
	if upstream == "" {
		return nil
	}
	entry, ok := s.avatarCache.get(upstream)
	if !ok {
		fetched, err := s.fetchAvatar(ctx, upstream)
		if err != nil {
			return nil
		}
		s.avatarCache.put(upstream, fetched)
		entry = fetched
	}
	img, _, err := image.Decode(bytes.NewReader(entry.body))
	if err != nil {
		return nil
	}
	return img
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
func drawOGCard(data ogCardData) (image.Image, error) {
	internal := image.NewRGBA(image.Rect(0, 0, ogImageInternalW, ogImageInternalH))
	draw.Draw(internal, internal.Bounds(), &image.Uniform{C: ogBackground}, image.Point{}, draw.Src)

	bold := inconsolata.Bold8x16
	regular := inconsolata.Regular8x16
	advance := faceAdvance(bold)
	maxCols := (ogImageInternalW - 2*ogImagePadX - 16) / advance
	if maxCols > ogImageMaxCols {
		maxCols = ogImageMaxCols
	}
	if maxCols < ogImageMinCols {
		maxCols = ogImageMinCols
	}
	lines := buildOGThreadLines(data, bold, regular, maxCols)
	lineHeight := faceLineHeight(regular)
	cardW := maxCols*advance + 16
	cardH := len(lines)*lineHeight + 20
	cardX := (ogImageInternalW - cardW) / 2
	cardY := ogImagePadY
	if cardY+cardH > ogImageInternalH-ogImagePadY {
		cardY = ogImageInternalH - ogImagePadY - cardH
	}
	if cardY < ogImagePadY {
		cardY = ogImagePadY
	}
	cardRect := image.Rect(cardX, cardY, cardX+cardW, cardY+cardH)
	drawFilledRect(internal, cardRect, ogPanelBackground)
	drawRectOutline(internal, cardRect, ogBorderForeground)
	drawOGCardBody(internal, data, lines, cardRect, regular)
	if ogCardUsesHeaderAvatar(data) {
		drawOGAvatar(internal, data, cardRect, regular)
	}

	// Nearest-neighbor scale to the final 1200x630 so the pixel font stays
	// sharp instead of getting bilinear-smoothed into a blurry mess.
	final := image.NewRGBA(image.Rect(0, 0, ogImageWidth, ogImageHeight))
	xdraw.NearestNeighbor.Scale(final, final.Bounds(), internal, internal.Bounds(), xdraw.Src, nil)
	return final, nil
}

func drawOGCardBody(dst *image.RGBA, data ogCardData, lines []ogRenderedLine, rect image.Rectangle, bodyFace font.Face) {
	if rect.Empty() {
		return
	}
	baseX := rect.Min.X + 8
	baseY := ogHeaderBaselineY(rect, bodyFace, ogCardUsesHeaderAvatar(data))
	lineHeight := faceLineHeight(bodyFace)
	for i, line := range lines {
		if strings.TrimSpace(line.Text) == "" {
			continue
		}
		face := line.Face
		if face == nil {
			face = bodyFace
		}
		clr := line.Color
		if clr == nil {
			clr = ogForeground
		}
		drawText(dst, face, clr, baseX, baseY+(i*lineHeight), sanitizeASCII(line.Text))
		for _, span := range line.Spans {
			spanFace := span.Face
			if spanFace == nil {
				spanFace = face
			}
			spanColor := span.Color
			if spanColor == nil {
				spanColor = clr
			}
			drawText(dst, spanFace, spanColor, baseX+span.Col*faceAdvance(spanFace), baseY+(i*lineHeight), sanitizeASCII(span.Text))
		}
	}
}

func ogCardUsesHeaderAvatar(data ogCardData) bool {
	return data.Avatar != nil && !(replyContextVisible(data.Event) && data.Parent != nil)
}

func ogReferenceBlock(data ogCardData) *ogNestedBlock {
	if lead, snippet := ogReferenceSection(data); lead != "" || snippet != "" {
		return &ogNestedBlock{
			Title:    lead,
			Body:     snippet,
			Inset:    3,
			MaxLines: 3,
			Accent:   ogAccent,
		}
	}
	return nil
}

func ogBodyText(data ogCardData) string {
	var sections []string

	if lead := ogReplyLead(data.Profiles, data.Event); lead != "" {
		sections = append(sections, lead)
		parentText := ogSnippetText(data.Parent, data.Profiles)
		if parentText != "" {
			sections = append(sections, parentText)
		}
	}

	if main := ogSnippetText(&data.Event, data.Profiles); main != "" {
		sections = append(sections, main)
	}

	if lead, snippet := ogReferenceSection(data); lead != "" || snippet != "" {
		if lead != "" {
			sections = append(sections, lead)
		}
		if snippet != "" {
			sections = append(sections, snippet)
		}
	}

	if len(sections) == 0 {
		if fallback := strings.TrimSpace(data.Event.Content); fallback != "" {
			sections = append(sections, asciiMentionContent(fallback, data.Profiles))
		}
	}

	return normalizeBodyForOG(strings.Join(sections, "\n\n"))
}

func ogReplyLead(profiles map[string]nostrx.Profile, event nostrx.Event) string {
	if !replyContextVisible(event) {
		return ""
	}
	targets := replyContextTargets(event)
	if len(targets) == 0 {
		if ogParentEventID(event) != "" {
			return "Replying to thread"
		}
		return ""
	}
	show := targets
	rest := 0
	if len(show) > 2 {
		rest = len(show) - 2
		show = show[:2]
	}
	names := make([]string, 0, len(show))
	for _, pk := range show {
		names = append(names, "@"+authorLabel(profiles, pk))
	}
	lead := "Replying to " + strings.Join(names, " ")
	if rest > 0 {
		lead += " and " + strconv.Itoa(rest) + " others"
	}
	return lead
}

func ogReferenceSection(data ogCardData) (lead, snippet string) {
	switch {
	case data.Event.Kind == nostrx.KindRepost:
		lead = ogReferenceHeader(data.Reference, data.Profiles)
	case isQuotePost(data.Event):
		lead = ogReferenceHeader(data.Reference, data.Profiles)
	default:
		return "", ""
	}
	if data.Reference != nil {
		snippet = ogSnippetText(data.Reference, data.Profiles)
	} else if referencedEventID(data.Event) != "" {
		snippet = "[referenced note unavailable on current relays]"
	}
	return strings.TrimSpace(lead), snippet
}

func ogReferenceHeader(event *nostrx.Event, profiles map[string]nostrx.Profile) string {
	if event == nil {
		return ""
	}
	author := sanitizeASCII(authorLabel(profiles, event.PubKey))
	if author == "" {
		author = sanitizeASCII(short(event.PubKey))
	}
	timeLabel := sanitizeASCII(relTime(event.CreatedAt))
	if timeLabel == "" {
		return author
	}
	return author + " -- " + timeLabel
}

func ogSnippetText(event *nostrx.Event, profiles map[string]nostrx.Profile) string {
	if event == nil {
		return ""
	}
	text := strings.TrimSpace(noteMainBodySourceText(*event))
	if text == "" && event.Kind != nostrx.KindRepost {
		text = strings.TrimSpace(event.Content)
	}
	if text == "" {
		return ""
	}
	return asciiMentionContent(text, profiles)
}

func ogParentEventID(event nostrx.Event) string {
	root := thread.RootID(event)
	parent := thread.ParentID(root, event)
	return thread.NormalizeHexEventID(strings.TrimSpace(parent))
}

func buildOGThreadLines(data ogCardData, bold, regular font.Face, totalCols int) []ogRenderedLine {
	if totalCols < ogImageMinCols {
		totalCols = ogImageMinCols
	}
	if replyContextVisible(data.Event) && data.Parent != nil {
		return buildOGReplyThreadLines(data, bold, regular, totalCols)
	}
	author := sanitizeASCII(authorLabel(data.Profiles, data.Event.PubKey))
	if author == "" {
		author = sanitizeASCII(short(data.Event.PubKey))
	}
	timeLabel := sanitizeASCII(relTime(data.Event.CreatedAt))
	npubLabel := sanitizeASCII(short(nostrx.EncodeNPub(data.Event.PubKey)))
	headerStart := 4
	if ogCardUsesHeaderAvatar(data) {
		headerStart += ogImageAvatarCols + 1
	}
	lines := []ogRenderedLine{buildOGHeaderLine(author, timeLabel, npubLabel, totalCols, headerStart, bold, regular)}
	lines = append(lines, ogBorderedBlankLine(totalCols))

	after := ogReferenceBlock(data)
	remainingRows := ogImageMaxRows - 4
	mainRows := 4
	if mainRows > remainingRows-2 {
		mainRows = remainingRows - 2
	}
	if mainRows < 2 {
		mainRows = 2
	}
	mainText := ogSnippetText(&data.Event, data.Profiles)
	if mainText == "" {
		mainText = strings.TrimSpace(data.Event.Content)
	}
	for _, line := range wrapBodyLines(normalizeBodyForOG(mainText), totalCols-4, mainRows) {
		lines = append(lines, ogBorderedContentLine(line, totalCols))
	}
	remainingRows = ogImageMaxRows - len(lines) - 1
	if after != nil && remainingRows > 0 {
		block := buildOGNestedBlockLines(*after, totalCols, regular, ogAccent)
		if len(block) > remainingRows {
			block = block[:remainingRows]
		}
		lines = append(lines, block...)
	}
	lines = append(lines, ogBorderedBlankLine(totalCols))
	lines = append(lines, ogBottomBorderLine(totalCols))
	return lines
}

// buildOGReplyThreadLines mirrors the focused reply treatment in the thread
// UI: the direct parent flows down a rail into the selected reply. Reply
// context intentionally does not use buildOGNestedBlockLines; that inset box
// is reserved for quote/repost references.
func buildOGReplyThreadLines(data ogCardData, bold, regular font.Face, totalCols int) []ogRenderedLine {
	parent := data.Parent
	parentAuthor := sanitizeASCII(authorLabel(data.Profiles, parent.PubKey))
	if parentAuthor == "" {
		parentAuthor = sanitizeASCII(short(parent.PubKey))
	}
	parentAge := sanitizeASCII(relTime(parent.CreatedAt))
	lines := []ogRenderedLine{
		buildOGHeaderLine(parentAuthor, parentAge, "", totalCols, 4, bold, regular),
		ogThreadParentBlankLine(totalCols),
	}

	parentText := ogSnippetText(parent, data.Profiles)
	for _, line := range wrapBodyLines(normalizeBodyForOG(parentText), totalCols-7, 2) {
		lines = append(lines, ogThreadParentContentLine(line, totalCols, regular))
	}
	lines = append(lines, ogThreadParentBlankLine(totalCols))

	author := sanitizeASCII(authorLabel(data.Profiles, data.Event.PubKey))
	if author == "" {
		author = sanitizeASCII(short(data.Event.PubKey))
	}
	age := sanitizeASCII(relTime(data.Event.CreatedAt))
	npub := sanitizeASCII(short(nostrx.EncodeNPub(data.Event.PubKey)))
	lines = append(lines, buildOGFocusedReplyHeaderLine(author, age, npub, totalCols, bold, regular))

	mainText := ogSnippetText(&data.Event, data.Profiles)
	if mainText == "" {
		mainText = strings.TrimSpace(data.Event.Content)
	}
	remainingRows := ogImageMaxRows - len(lines) - 2
	if remainingRows < 1 {
		remainingRows = 1
	}
	for _, line := range wrapBodyLines(normalizeBodyForOG(mainText), totalCols-7, remainingRows) {
		lines = append(lines, ogFocusedReplyContentLine(line, totalCols, regular))
	}
	lines = append(lines, ogFocusedReplyBottomLine(totalCols, regular))
	lines = append(lines, ogBottomBorderLine(totalCols))
	return lines
}

func ogThreadParentBlankLine(totalCols int) ogRenderedLine {
	runes := fillRunes(totalCols, ' ')
	runes[0] = '|'
	runes[2] = '|'
	runes[totalCols-1] = '|'
	return ogRenderedLine{Text: string(runes), Face: inconsolata.Regular8x16, Color: ogForeground}
}

func ogThreadParentContentLine(content string, totalCols int, regular font.Face) ogRenderedLine {
	runes := fillRunes(totalCols, ' ')
	runes[0] = '|'
	runes[2] = '|'
	runes[totalCols-1] = '|'
	writeRunes(runes, 4, clipOGText(sanitizeASCII(content), totalCols-7))
	return ogRenderedLine{Text: string(runes), Face: regular, Color: ogForeground}
}

func buildOGFocusedReplyHeaderLine(author, timeLabel, npub string, totalCols int, bold, regular font.Face) ogRenderedLine {
	runes := fillRunes(totalCols, ' ')
	runes[0] = '|'
	runes[totalCols-1] = '|'
	innerEnd := totalCols - 3
	runes[innerEnd] = '+'
	headerStart := 4
	header := author
	if timeLabel != "" {
		header += " -- " + timeLabel
	}
	header = clipOGText(header, max(8, innerEnd-headerStart-2))
	writeRunes(runes, headerStart, header)
	rightStart := -1
	if npub != "" {
		rightStart = innerEnd - len(npub) - 1
		if rightStart > headerStart+len(header)+2 {
			writeRunes(runes, rightStart, npub)
		} else {
			rightStart = -1
		}
	}
	fillStart := headerStart + len(header) + 1
	fillEnd := innerEnd
	if rightStart > 0 {
		fillEnd = rightStart - 1
	}
	for i := fillStart; i < fillEnd; i++ {
		if runes[i] == ' ' {
			runes[i] = '-'
		}
	}
	line := ogRenderedLine{
		Text:  string(runes),
		Face:  regular,
		Color: ogForeground,
		Spans: []ogTextSpan{{Col: headerStart, Text: author, Color: ogAuthorAccent, Face: bold}},
	}
	if timeLabel != "" {
		line.Spans = append(line.Spans, ogTextSpan{Col: headerStart + len(author) + 4, Text: timeLabel, Color: ogForeground, Face: bold})
	}
	if rightStart > 0 {
		line.Spans = append(line.Spans, ogTextSpan{Col: rightStart, Text: npub, Color: ogMuted, Face: regular})
	}
	return line
}

func ogFocusedReplyContentLine(content string, totalCols int, regular font.Face) ogRenderedLine {
	runes := fillRunes(totalCols, ' ')
	runes[0] = '|'
	runes[totalCols-3] = '|'
	runes[totalCols-1] = '|'
	writeRunes(runes, 4, clipOGText(sanitizeASCII(content), totalCols-9))
	return ogRenderedLine{Text: string(runes), Face: regular, Color: ogForeground}
}

func ogFocusedReplyBottomLine(totalCols int, regular font.Face) ogRenderedLine {
	runes := fillRunes(totalCols, ' ')
	runes[0] = '|'
	runes[totalCols-1] = '|'
	for i := 4; i < totalCols-3; i++ {
		runes[i] = '-'
	}
	runes[totalCols-3] = '+'
	return ogRenderedLine{Text: string(runes), Face: regular, Color: ogForeground}
}

func buildOGHeaderLine(author, timeLabel, npub string, totalCols, startCol int, bold, regular font.Face) ogRenderedLine {
	runes := fillRunes(totalCols, ' ')
	runes[0] = '+'
	runes[1] = '-'
	runes[2] = '-'
	runes[totalCols-1] = '+'
	header := author
	if timeLabel != "" {
		header += " -- " + timeLabel
	}
	writeRunes(runes, startCol, header)
	rightStart := -1
	if npub != "" {
		rightStart = totalCols - 1 - len(npub) - 1
		if rightStart > startCol+len(header)+2 {
			writeRunes(runes, rightStart, npub)
		} else {
			rightStart = -1
		}
	}
	fillStart := startCol + len(header) + 1
	fillEnd := totalCols - 2
	if rightStart > 0 {
		fillEnd = rightStart - 1
	}
	for i := fillStart; i < fillEnd; i++ {
		if runes[i] == ' ' {
			runes[i] = '-'
		}
	}
	line := ogRenderedLine{
		Text:  string(runes),
		Face:  regular,
		Color: ogForeground,
		Spans: []ogTextSpan{{Col: startCol, Text: author, Color: ogAuthorAccent, Face: bold}},
	}
	if timeLabel != "" {
		line.Spans = append(line.Spans, ogTextSpan{Col: startCol + len(author) + 4, Text: timeLabel, Color: ogForeground, Face: bold})
	}
	if rightStart > 0 {
		line.Spans = append(line.Spans, ogTextSpan{Col: rightStart, Text: npub, Color: ogMuted, Face: regular})
	}
	return line
}

func buildOGNestedBlockLines(block ogNestedBlock, totalCols int, regular font.Face, accent color.Color) []ogRenderedLine {
	inset := block.Inset
	if inset < 2 {
		inset = 2
	}
	innerStart := inset
	innerEnd := totalCols - 3
	headerText := clipOGText(sanitizeASCII(block.Title), max(8, innerEnd-innerStart-6))
	runes := fillRunes(totalCols, ' ')
	runes[0] = '|'
	runes[totalCols-1] = '|'
	runes[innerStart] = '+'
	runes[innerStart+1] = '-'
	runes[innerStart+2] = '-'
	runes[innerEnd] = '+'
	writeRunes(runes, innerStart+4, headerText)
	for i := innerStart + 4 + len(headerText) + 1; i < innerEnd; i++ {
		if runes[i] == ' ' {
			runes[i] = '-'
		}
	}
	lines := []ogRenderedLine{{
		Text:  string(runes),
		Face:  regular,
		Color: ogForeground,
		Spans: []ogTextSpan{{Col: innerStart + 4, Text: headerText, Color: accent, Face: regular}},
	}}
	bodyCols := innerEnd - innerStart - 4
	for _, line := range wrapBodyLines(normalizeBodyForOG(block.Body), bodyCols, block.MaxLines) {
		row := fillRunes(totalCols, ' ')
		row[0] = '|'
		row[totalCols-1] = '|'
		row[innerStart] = '|'
		row[innerEnd] = '|'
		writeRunes(row, innerStart+2, clipOGText(sanitizeASCII(line), bodyCols))
		lines = append(lines, ogRenderedLine{Text: string(row), Face: regular, Color: ogForeground})
	}
	footer := fillRunes(totalCols, ' ')
	footer[0] = '|'
	footer[totalCols-1] = '|'
	footer[innerStart] = '+'
	footer[innerStart+1] = '-'
	footer[innerStart+2] = '-'
	footer[innerEnd] = '+'
	for i := innerStart + 3; i < innerEnd; i++ {
		footer[i] = '-'
	}
	lines = append(lines, ogRenderedLine{Text: string(footer), Face: regular, Color: ogForeground})
	return lines
}

func ogBorderedBlankLine(totalCols int) ogRenderedLine {
	runes := fillRunes(totalCols, ' ')
	runes[0] = '|'
	runes[totalCols-1] = '|'
	return ogRenderedLine{Text: string(runes), Face: inconsolata.Regular8x16, Color: ogForeground}
}

func ogBorderedContentLine(content string, totalCols int) ogRenderedLine {
	runes := fillRunes(totalCols, ' ')
	runes[0] = '|'
	runes[totalCols-1] = '|'
	writeRunes(runes, 2, clipOGText(sanitizeASCII(content), totalCols-4))
	return ogRenderedLine{Text: string(runes), Face: inconsolata.Regular8x16, Color: ogForeground}
}

func ogBottomBorderLine(totalCols int) ogRenderedLine {
	runes := fillRunes(totalCols, '-')
	runes[0] = '+'
	runes[totalCols-1] = '+'
	return ogRenderedLine{Text: string(runes), Face: inconsolata.Regular8x16, Color: ogForeground}
}

func drawOGAvatar(dst *image.RGBA, data ogCardData, cardRect image.Rectangle, face font.Face) {
	if data.Avatar == nil {
		return
	}
	baseX := cardRect.Min.X + 8
	avatarX := baseX + faceAdvance(face)*2
	avatarY := ogHeaderCenterY(cardRect, face, true) - (ogImageAvatarSize / 2)
	avatarRect := image.Rect(avatarX, avatarY, avatarX+ogImageAvatarSize, avatarY+ogImageAvatarSize)
	xdraw.ApproxBiLinear.Scale(dst, avatarRect, data.Avatar, data.Avatar.Bounds(), draw.Over, nil)
	drawRectOutline(dst, avatarRect, ogForeground)
}

func ogHeaderBaselineY(rect image.Rectangle, face font.Face, hasAvatar bool) int {
	topPad := 2
	if hasAvatar {
		topPad = max(topPad, 4+max(0, (ogImageAvatarSize-faceLineHeight(face))/2))
	}
	return rect.Min.Y + topPad + faceAscent(face)
}

func ogHeaderCenterY(rect image.Rectangle, face font.Face, hasAvatar bool) int {
	baselineY := ogHeaderBaselineY(rect, face, hasAvatar)
	top := baselineY - faceAscent(face)
	bottom := baselineY + faceDescent(face)
	return top + ((bottom - top) / 2)
}

func faceAscent(face font.Face) int {
	if face == nil {
		return 0
	}
	return face.Metrics().Ascent.Ceil()
}

func faceDescent(face font.Face) int {
	if face == nil {
		return 0
	}
	return face.Metrics().Descent.Ceil()
}

func fillRunes(count int, fill rune) []rune {
	out := make([]rune, count)
	for i := range out {
		out[i] = fill
	}
	return out
}

func writeRunes(dst []rune, start int, text string) {
	if start < 0 || start >= len(dst) {
		return
	}
	runes := []rune(text)
	for i, r := range runes {
		if start+i >= len(dst) {
			return
		}
		dst[start+i] = r
	}
}

func clipOGText(s string, maxCols int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxCols {
		return string(runes)
	}
	if maxCols <= 1 {
		return string(runes[:maxCols])
	}
	return string(runes[:maxCols-1]) + "…"
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

func drawVerticalLine(dst *image.RGBA, x, top, bottom int, c color.Color) {
	bounds := dst.Bounds()
	if x < bounds.Min.X || x >= bounds.Max.X {
		return
	}
	if top < bounds.Min.Y {
		top = bounds.Min.Y
	}
	if bottom > bounds.Max.Y {
		bottom = bounds.Max.Y
	}
	for y := top; y < bottom; y++ {
		dst.Set(x, y, c)
	}
}

func drawFilledRect(dst draw.Image, r image.Rectangle, c color.Color) {
	if r.Empty() {
		return
	}
	draw.Draw(dst, r, &image.Uniform{C: c}, image.Point{}, draw.Src)
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
