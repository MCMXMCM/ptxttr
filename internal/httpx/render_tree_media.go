package httpx

// Tree-view media helpers.
//
// Mirrors the canonical media-detection rules in web/static/js/ascii.js
// (MEDIA_URL_PATTERN, IMAGE_EXT_PATTERN, VIDEO_EXT_PATTERN,
// stripMediaUrlsFromText, mediaSummaryLabel). Keep the two in sync.

import (
	"encoding/json"
	"regexp"
	"strings"
)

var treeMediaURLPattern = regexp.MustCompile("https?://[^\\s<>\"'`]+")

type treeMediaItem struct {
	URL  string `json:"url"`
	Type string `json:"type"`
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
	ItemsJSON     string
	Label         string
	DisplaySource string
}

func treeMediaFields(content string, tags [][]string) treeMediaInfo {
	itemsFromContent, mediaURLs := treeExtractMediaItems(content)
	itemsFromImeta := treeExtractImetaMediaItems(tags)
	merged := treeMergeMediaItems(itemsFromContent, itemsFromImeta)
	info := treeMediaInfo{Label: treeMediaLabelForItems(merged)}
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
	seen := make(map[string]struct{})
	out := make([]treeMediaItem, 0, len(a)+len(b))
	for _, list := range [][]treeMediaItem{a, b} {
		for _, it := range list {
			if it.URL == "" {
				continue
			}
			if _, ok := seen[it.URL]; ok {
				continue
			}
			seen[it.URL] = struct{}{}
			out = append(out, it)
		}
	}
	return out
}

func isImetaHTTPURL(u string) bool {
	lu := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(lu, "https://") || strings.HasPrefix(lu, "http://")
}

func parseImetaTag(tag []string) (url, mime string) {
	if len(tag) < 2 || tag[0] != "imeta" {
		return "", ""
	}
	for _, field := range tag[1:] {
		if strings.HasPrefix(field, "url ") {
			url = strings.TrimSpace(strings.TrimPrefix(field, "url "))
		} else if strings.HasPrefix(field, "m ") {
			mime = strings.TrimSpace(strings.TrimPrefix(field, "m "))
		}
	}
	return url, mime
}

func treeExtractImetaMediaItems(tags [][]string) []treeMediaItem {
	if len(tags) == 0 {
		return nil
	}
	var out []treeMediaItem
	seen := make(map[string]struct{})
	for _, tag := range tags {
		u, mime := parseImetaTag(tag)
		if u == "" || !isImetaHTTPURL(u) {
			continue
		}
		mLower := strings.ToLower(mime)
		kind := ""
		switch {
		case strings.HasPrefix(mLower, "image/"):
			kind = "image"
		case strings.HasPrefix(mLower, "video/"):
			kind = "video"
		default:
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, treeMediaItem{URL: u, Type: kind})
	}
	return out
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
