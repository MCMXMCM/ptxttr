package httpx

import (
	"fmt"
	"testing"
)

func TestAsciiDecimalPad(t *testing.T) {
	tests := []struct {
		n, w int
		want string
	}{
		{1, 2, " 1"},
		{11, 2, "11"},
		{1, 3, "  1"},
		{22, 3, " 22"},
		{999, 3, "999"},
	}
	for _, tt := range tests {
		if got := asciiDecimalPad(tt.n, tt.w); got != tt.want {
			t.Errorf("asciiDecimalPad(%d, %d) = %q, want %q", tt.n, tt.w, got, tt.want)
		}
	}
}

func TestReplyBadgeText(t *testing.T) {
	tests := []struct {
		count int
		want  string
	}{
		{0, ""},
		{1, "  1 rply"},
		{2, "  2 rpls"},
		{22, " 22 rpls"},
		{999, "999 rpls"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("count=%d", tt.count), func(t *testing.T) {
			if got := replyBadgeText(tt.count); got != tt.want {
				t.Fatalf("replyBadgeText(%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}

func TestTreeMediaLabelForItems(t *testing.T) {
	elevenVideos := make([]treeMediaItem, 11)
	for i := range elevenVideos {
		elevenVideos[i] = treeMediaItem{URL: fmt.Sprintf("https://x/%d.mp4", i+1), Type: "video"}
	}
	tests := []struct {
		name  string
		items []treeMediaItem
		want  string
	}{
		{"empty", nil, ""},
		{"one image", []treeMediaItem{{URL: "https://x/a.png", Type: "image"}}, " 1 image "},
		{"two images", []treeMediaItem{
			{URL: "https://x/a.png", Type: "image"},
			{URL: "https://x/b.png", Type: "image"},
		}, " 2 images"},
		{"one video", []treeMediaItem{{URL: "https://x/a.mp4", Type: "video"}}, " 1 video "},
		{"eleven videos", elevenVideos, "11 videos"},
		{"mixed two", []treeMediaItem{
			{URL: "https://x/a.png", Type: "image"},
			{URL: "https://x/b.mp4", Type: "video"},
		}, " 2 media "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := treeMediaLabelForItems(tt.items); got != tt.want {
				t.Fatalf("treeMediaLabelForItems() = %q, want %q", got, tt.want)
			}
		})
	}
}
