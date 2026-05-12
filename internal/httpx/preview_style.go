package httpx

import (
	"net/http"
	"strings"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
)

// previewStyle classifies the requesting user-agent so the rendering layer
// can tailor preview cards / Telegram Instant View output. The default is
// styleNormal (regular browser).
type previewStyle int

const (
	styleNormal previewStyle = iota
	styleTwitter
	styleTelegram
	styleSlack
	styleDiscord
	styleFacebook
)

// Long-form / long-content threshold used to gate Telegram Instant View.
// Aligns with njump's render_event.go heuristic.
const tgivContentThreshold = 650

// detectPreviewStyle inspects the User-Agent header and returns the matching
// previewStyle. UA strings are matched case-insensitively. An explicit
// `?style=` query parameter overrides automatic detection so operators can
// debug and CDN crawlers can be coerced into a specific style.
func detectPreviewStyle(r *http.Request) previewStyle {
	if r == nil {
		return styleNormal
	}
	if override := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("style"))); override != "" {
		switch override {
		case "twitter":
			return styleTwitter
		case "telegram":
			return styleTelegram
		case "slack":
			return styleSlack
		case "discord":
			return styleDiscord
		case "facebook":
			return styleFacebook
		case "normal":
			return styleNormal
		}
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	if ua == "" {
		return styleNormal
	}
	switch {
	case strings.Contains(ua, "telegrambot"):
		return styleTelegram
	case strings.Contains(ua, "twitterbot"):
		return styleTwitter
	case strings.Contains(ua, "slackbot-linkexpanding"), strings.Contains(ua, "slackbot"):
		return styleSlack
	case strings.Contains(ua, "discordbot"):
		return styleDiscord
	case strings.Contains(ua, "facebookexternalhit"), strings.Contains(ua, "meta-externalagent"):
		return styleFacebook
	}
	return styleNormal
}

// shouldRenderInstantView reports whether handleThread should render the
// Telegram Instant View template for the current request and the given
// selected event. Triggers when the UA is a Telegram bot AND (the event is
// long-form OR its content exceeds tgivContentThreshold), or when the
// request opts in via `?tgiv=true`.
func shouldRenderInstantView(r *http.Request, content string, kind int) bool {
	if r == nil {
		return false
	}
	if v, ok := config.ParseBool(r.URL.Query().Get("tgiv")); ok && v {
		return true
	}
	if detectPreviewStyle(r) != styleTelegram {
		return false
	}
	if kind == nostrx.KindLongForm {
		return true
	}
	return len(content) > tgivContentThreshold
}
