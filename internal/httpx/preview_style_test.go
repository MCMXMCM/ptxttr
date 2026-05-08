package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetectPreviewStyleFromUserAgent(t *testing.T) {
	cases := map[string]previewStyle{
		"":                                                                  styleNormal,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)":                      styleNormal,
		"TelegramBot (like TwitterBot)":                                     styleTelegram,
		"Twitterbot/1.0":                                                    styleTwitter,
		"Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)":        styleSlack,
		"Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)": styleDiscord,
		"facebookexternalhit/1.1 (+https://www.facebook.com/externalhit)":   styleFacebook,
		"meta-externalagent/1.1":                                            styleFacebook,
	}
	for ua, want := range cases {
		t.Run(ua, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if ua != "" {
				r.Header.Set("User-Agent", ua)
			}
			if got := detectPreviewStyle(r); got != want {
				t.Fatalf("detectPreviewStyle(%q) = %v, want %v", ua, got, want)
			}
		})
	}
}

func TestDetectPreviewStyleQueryOverride(t *testing.T) {
	r := httptest.NewRequest("GET", "/?style=telegram", nil)
	if got := detectPreviewStyle(r); got != styleTelegram {
		t.Fatalf("got %v, want telegram", got)
	}
	r2 := httptest.NewRequest("GET", "/?style=normal", nil)
	r2.Header.Set("User-Agent", "TelegramBot")
	if got := detectPreviewStyle(r2); got != styleNormal {
		t.Fatalf("override should beat UA, got %v", got)
	}
}

func TestShouldRenderInstantViewKindLongForm(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Header.Set("User-Agent", "TelegramBot")
	if !shouldRenderInstantView(r, "short", 30023) {
		t.Fatal("long-form kind should always trigger IV under TelegramBot")
	}
}

func TestShouldRenderInstantViewLongContent(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Header.Set("User-Agent", "TelegramBot")
	long := strings.Repeat("a", tgivContentThreshold+10)
	if !shouldRenderInstantView(r, long, 1) {
		t.Fatal("long content under TelegramBot should trigger IV")
	}
}

func TestShouldRenderInstantViewSkipsShortContent(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Header.Set("User-Agent", "TelegramBot")
	if shouldRenderInstantView(r, "short note", 1) {
		t.Fatal("short content should NOT trigger IV even for TelegramBot")
	}
}

func TestShouldRenderInstantViewQueryOverride(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc?tgiv=true", nil)
	if !shouldRenderInstantView(r, "short", 1) {
		t.Fatal("?tgiv=true should force IV even for short non-bot")
	}
}

func TestShouldRenderInstantViewNonTelegramUA(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0")
	if shouldRenderInstantView(r, strings.Repeat("a", 1000), 30023) {
		t.Fatal("non-Telegram UA should not trigger IV")
	}
}
