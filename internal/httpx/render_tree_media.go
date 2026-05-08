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

// treeMediaInfo bundles every tree-row field the templates need so
// extraction/strip work happens once per note.
type treeMediaInfo struct {
	ItemsJSON     string
	Label         string
	DisplaySource string
}

func treeMediaFields(content string) treeMediaInfo {
	items, mediaURLs := treeExtractMediaItems(content)
	info := treeMediaInfo{Label: treeMediaLabelForItems(items)}
	if len(items) == 0 {
		return info
	}
	if encoded, err := json.Marshal(items); err == nil {
		info.ItemsJSON = string(encoded)
	}
	// When the note is media-only, leave DisplaySource empty so the tree
	// row shows just the clickable footer label rather than duplicating it
	// as both placeholder text and a button.
	stripped := treeStripMediaURLs(content, mediaURLs)
	if strings.TrimSpace(stripped) != "" {
		info.DisplaySource = stripped
	}
	return info
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
