package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnonymousHTMLCacheKeyPartitionsASCIIWidths(t *testing.T) {
	desktop := httptest.NewRequest(http.MethodGet, "/thread/abc?selected=def", nil)
	desktop.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)")
	if got := anonymousHTMLCacheKey(desktop); got != "/thread/abc?selected=def|ascii_w=52" {
		t.Fatalf("desktop cache key = %q", got)
	}

	measured := httptest.NewRequest(http.MethodGet, "/thread/abc?selected=def", nil)
	measured.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)")
	measured.AddCookie(&http.Cookie{Name: asciiWidthDesktopCookieName, Value: "58"})
	if got := anonymousHTMLCacheKey(measured); got != "/thread/abc?selected=def|ascii_w=58" {
		t.Fatalf("measured cache key = %q", got)
	}
}

func TestAnonymousHTMLCacheKeyKeepsPreviewBotsApart(t *testing.T) {
	browser := httptest.NewRequest(http.MethodGet, "/thread/abc", nil)
	browser.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)")
	telegram := httptest.NewRequest(http.MethodGet, "/thread/abc", nil)
	telegram.Header.Set("User-Agent", "TelegramBot (like TwitterBot)")
	slack := httptest.NewRequest(http.MethodGet, "/thread/abc", nil)
	slack.Header.Set("User-Agent", "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)")

	keys := map[string]bool{
		anonymousHTMLCacheKey(browser):  true,
		anonymousHTMLCacheKey(telegram): true,
		anonymousHTMLCacheKey(slack):    true,
	}
	if len(keys) != 3 {
		t.Fatalf("preview bots share cache keys with browsers: %v", keys)
	}
}
