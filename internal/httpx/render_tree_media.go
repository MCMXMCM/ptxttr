package httpx

// Tree-view media helpers.
//
// Mirrors the canonical media-detection rules in web/static/js/ascii.js
// (MEDIA_URL_PATTERN, IMAGE_EXT_PATTERN, VIDEO_EXT_PATTERN,
// stripMediaUrlsFromText, mediaSummaryLabel). Keep the two in sync.

import (
	"encoding/json"
	"html/template"
	"regexp"
	"strconv"
	"strings"
)

var treeMediaURLPattern = regexp.MustCompile("https?://[^\\s<>\"'`]+")
var treeBlossomURLPattern = regexp.MustCompile(`(?i)^https?://[^/]*\.blossom\.band/([^\s<>"'` + "`" + `?#]+)`)
var treeMediaDimPattern = regexp.MustCompile(`^([1-9][0-9]{0,5})x([1-9][0-9]{0,5})$`)

type treeMediaItem struct {
	URL    string `json:"url"`
	Type   string `json:"type"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

func treeMediaItemsJSON(items []treeMediaItem) string {
	if len(items) == 0 {
		return ""
	}
	b, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(b)
}

// treeMediaInfo bundles every tree-row field the templates need so
// extraction/strip work happens once per note.
type treeMediaInfo struct {
	Items         []treeMediaItem
	ItemsJSON     string
	Label         string
	DisplaySource string
}

func treeMediaFields(content string, tags [][]string) treeMediaInfo {
	itemsFromContent, mediaURLs := treeExtractMediaItems(content)
	itemsFromImeta := treeExtractImetaMediaItems(tags)
	merged := treeMergeMediaItems(itemsFromContent, itemsFromImeta)
	info := treeMediaInfo{
		Items: merged,
		Label: treeMediaLabelForItems(merged),
	}
	if len(merged) == 0 {
		return info
	}
	info.ItemsJSON = treeMediaItemsJSON(merged)
	stripped := treeStripMediaURLs(content, mediaURLs)
	if strings.TrimSpace(stripped) != "" {
		info.DisplaySource = stripped
	}
	return info
}

// imetaMediaItemsJSON returns JSON of image/video items parsed from NIP-94-style
// `imeta` tags (for feed `data-ascii-imeta-media`). Safe inside HTML attributes via html/template.
func imetaMediaItemsJSON(tags [][]string) string {
	return treeMediaItemsJSON(treeExtractImetaMediaItems(tags))
}

func treeMergeMediaItems(a, b []treeMediaItem) []treeMediaItem {
	seen := make(map[string]int)
	out := make([]treeMediaItem, 0, len(a)+len(b))
	for _, list := range [][]treeMediaItem{a, b} {
		for _, it := range list {
			if it.URL == "" {
				continue
			}
			key := treeMediaDedupKey(it.URL)
			if idx, ok := seen[key]; ok {
				// Later `imeta` entries replace display-only Blossom body URLs
				// that point at the same underlying content hash/path.
				out[idx] = it
				continue
			}
			seen[key] = len(out)
			out = append(out, it)
		}
	}
	return out
}

func treeMediaDedupKey(raw string) string {
	if suffix := treeBlossomPathSuffix(raw); suffix != "" {
		return "blossom:" + suffix
	}
	return raw
}

func treeBlossomPathSuffix(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "),.!?;:")
	if u == "" {
		return ""
	}
	m := treeBlossomURLPattern.FindStringSubmatch(u)
	if len(m) != 2 {
		return ""
	}
	return strings.ToLower(m[1])
}

func isImetaHTTPURL(u string) bool {
	lu := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(lu, "https://") || strings.HasPrefix(lu, "http://")
}

func parseImetaTag(tag []string) (url, mime string, width, height int) {
	if len(tag) < 2 || tag[0] != "imeta" {
		return "", "", 0, 0
	}
	for _, field := range tag[1:] {
		if strings.HasPrefix(field, "url ") {
			url = strings.TrimSpace(strings.TrimPrefix(field, "url "))
		} else if strings.HasPrefix(field, "m ") {
			mime = strings.TrimSpace(strings.TrimPrefix(field, "m "))
		} else if strings.HasPrefix(field, "dim ") {
			width, height = parseMediaDimensions(strings.TrimSpace(strings.TrimPrefix(field, "dim ")))
		}
	}
	return url, mime, width, height
}

func parseMediaDimensions(raw string) (int, int) {
	match := treeMediaDimPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if len(match) != 3 {
		return 0, 0
	}
	width, widthErr := strconv.Atoi(match[1])
	height, heightErr := strconv.Atoi(match[2])
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return 0, 0
	}
	return width, height
}

func treeExtractImetaMediaItems(tags [][]string) []treeMediaItem {
	if len(tags) == 0 {
		return nil
	}
	var out []treeMediaItem
	seen := make(map[string]struct{})
	for _, tag := range tags {
		u, mime, width, height := parseImetaTag(tag)
		if u == "" || !isImetaHTTPURL(u) {
			continue
		}
		kind := treeImetaMediaType(u, mime)
		if kind == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, treeMediaItem{URL: u, Type: kind, Width: width, Height: height})
	}
	return out
}

func mediaGridClass(items []treeMediaItem) string {
	count := len(items)
	if count < 1 {
		count = 1
	}
	if count > 6 {
		count = 6
	}
	return "note-media-grid note-media-grid-" + strconv.Itoa(count)
}

func mediaGridSignature(items []treeMediaItem) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		kind := "image"
		if item.Type == "video" {
			kind = "video"
		}
		parts = append(parts, kind+":"+item.URL)
	}
	return strings.Join(parts, "|")
}

func mediaGridVisibleItems(items []treeMediaItem) []treeMediaItem {
	limit := 6
	if len(items) > 6 {
		limit = 5
	}
	if len(items) < limit {
		limit = len(items)
	}
	return items[:limit]
}

func mediaGridAspectRatio(items []treeMediaItem) string {
	if len(items) != 1 {
		return ""
	}
	item := items[0]
	if item.Width <= 0 || item.Height <= 0 {
		return ""
	}
	return strconv.Itoa(item.Width) + " / " + strconv.Itoa(item.Height)
}

func mediaGridAspectStyle(items []treeMediaItem) template.CSS {
	ratio := mediaGridAspectRatio(items)
	if ratio == "" {
		return ""
	}
	return template.CSS("--note-media-image-aspect-ratio: " + ratio)
}

func treeImetaMediaType(url, mime string) string {
	mLower := strings.ToLower(strings.TrimSpace(mime))
	switch {
	case strings.HasPrefix(mLower, "image/"):
		return "image"
	case strings.HasPrefix(mLower, "video/"):
		return "video"
	case mLower == "jpg" || mLower == "jpeg" || mLower == "png" || mLower == "gif" || mLower == "webp" || mLower == "avif" || mLower == "svg" || mLower == "svg+xml" || mLower == "heic" || mLower == "heif":
		return "image"
	case mLower == "mp4" || mLower == "webm" || mLower == "m4v" || mLower == "mov" || mLower == "quicktime" || mLower == "ogv" || mLower == "ogg":
		return "video"
	}
	return treeMediaType(url)
}

func treeExtractMediaItems(content string) ([]treeMediaItem, map[string]struct{}) {
	matches := treeMediaURLPattern.FindAllString(content, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(matches))
	items := make([]treeMediaItem, 0, len(matches))
	for _, raw := range matches {
		url := strings.TrimRight(raw, "),.!?;:")
		if url == "" {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		kind := treeMediaType(url)
		if kind == "" {
			continue
		}
		seen[url] = struct{}{}
		items = append(items, treeMediaItem{URL: url, Type: kind})
	}
	return items, seen
}

func treeMediaType(url string) string {
	lower := strings.ToLower(url)
	if idx := strings.IndexAny(lower, "?#"); idx >= 0 {
		lower = lower[:idx]
	}
	switch {
	case strings.HasSuffix(lower, ".png"),
		strings.HasSuffix(lower, ".jpg"),
		strings.HasSuffix(lower, ".jpeg"),
		strings.HasSuffix(lower, ".gif"),
		strings.HasSuffix(lower, ".webp"),
		strings.HasSuffix(lower, ".avif"),
		strings.HasSuffix(lower, ".svg"):
		return "image"
	case strings.HasSuffix(lower, ".mp4"),
		strings.HasSuffix(lower, ".webm"),
		strings.HasSuffix(lower, ".m4v"),
		strings.HasSuffix(lower, ".mov"),
		strings.HasSuffix(lower, ".ogv"),
		strings.HasSuffix(lower, ".ogg"):
		return "video"
	default:
		return ""
	}
}

func treeMediaLabelForItems(items []treeMediaItem) string {
	if len(items) == 0 {
		return ""
	}
	images := 0
	videos := 0
	for _, item := range items {
		switch item.Type {
		case "image":
			images++
		case "video":
			videos++
		}
	}
	if images > 0 && videos == 0 {
		if images == 1 {
			return asciiDecimalPad(1, 2) + " image "
		}
		return asciiDecimalPad(images, 2) + " images"
	}
	if videos > 0 && images == 0 {
		if videos == 1 {
			return asciiDecimalPad(1, 2) + " video "
		}
		return asciiDecimalPad(videos, 2) + " videos"
	}
	n := len(items)
	if n == 1 {
		return asciiDecimalPad(1, 2) + " media "
	}
	return asciiDecimalPad(n, 2) + " media "
}

func treeStripMediaURLs(content string, mediaURLs map[string]struct{}) string {
	if content == "" {
		return content
	}
	stripped := treeMediaURLPattern.ReplaceAllStringFunc(content, func(raw string) string {
		url := strings.TrimRight(raw, "),.!?;:")
		if _, ok := mediaURLs[url]; !ok {
			return raw
		}
		return ""
	})
	lines := strings.Split(stripped, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		compact := strings.TrimSpace(strings.Join(strings.Fields(line), " "))
		if compact == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, compact)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
