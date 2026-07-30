package httpx

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"ptxt-nstr/internal/nostrx"
)

type appShellRouteContext struct {
	Path      string `json:"path"`
	Search    string `json:"search"`
	Hash      string `json:"hash"`
	Route     string `json:"route"`
	IsCrawler bool   `json:"isCrawler"`
}

type appShellBootstrap struct {
	Version        int                       `json:"version"`
	AssetBasePath  string                    `json:"assetBasePath"`
	Route          appShellRouteContext      `json:"route"`
	Viewer         appShellBootstrapViewer   `json:"viewer"`
	Features       map[string]bool           `json:"features"`
	Guest          appShellBootstrapGuest    `json:"guest"`
	InitialProfile *appShellBootstrapProfile `json:"initialProfile,omitempty"`
}

type appShellBootstrapViewer struct {
	PubKey     string   `json:"pubkey"`
	SeedPubKey string   `json:"seedPubkey"`
	Relays     []string `json:"relays"`
}

type appShellBootstrapGuest struct {
	DefaultWOTSeedNPub string `json:"defaultWOTSeedNpub"`
	DefaultWOTDepth    int    `json:"defaultWOTDepth"`
}

type appShellBootstrapProfile struct {
	PubKey      string   `json:"pubkey"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	About       string   `json:"about"`
	Picture     string   `json:"picture"`
	Website     string   `json:"website"`
	NIP05       string   `json:"nip05"`
	Lud16       string   `json:"lud16"`
	Lud06       string   `json:"lud06"`
	EventID     string   `json:"event_id"`
	CreatedAt   int64    `json:"created_at"`
	RelayHints  []string `json:"relay_hints,omitempty"`
}

func routeKindForPath(path string) string {
	switch {
	case path == "/" || path == "/feed":
		return "feed"
	case path == "/reads":
		return "reads"
	case path == "/bookmarks":
		return "bookmarks"
	case path == "/search":
		return "search"
	case path == "/relays":
		return "relays"
	case path == "/notifications":
		return "notifications"
	case path == "/settings",
		path == "/login",
		path == "/about",
		path == "/profile/edit",
		path == "/support",
		path == "/ios-plain-text-nostr",
		path == "/terms",
		path == "/privacy":
		return "stub"
	case len(path) > len("/tag/") && path[:len("/tag/")] == "/tag/":
		return "tag"
	case len(path) > len("/u/") && path[:len("/u/")] == "/u/":
		return "profile"
	case len(path) > len("/thread/") && path[:len("/thread/")] == "/thread/":
		return "thread"
	case len(path) > len("/reads/") && path[:len("/reads/")] == "/reads/":
		return "read"
	default:
		return ""
	}
}

func isCrawlerPreviewRequest(r *http.Request) bool {
	return detectPreviewStyle(r) != styleNormal
}

func appShellRouteContextJSON(r *http.Request) template.JS {
	payload := appShellRouteContextPayload(r)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return template.JS(`{"path":"/","search":"","hash":"","route":"","isCrawler":false}`)
	}
	return template.JS(encoded)
}

func appShellRouteContextPayload(r *http.Request) appShellRouteContext {
	if r == nil || r.URL == nil {
		return appShellRouteContext{Path: "/", Search: "", Hash: "", Route: "", IsCrawler: false}
	}
	return appShellRouteContext{
		Path:      r.URL.Path,
		Search:    r.URL.RawQuery,
		Hash:      r.URL.Fragment,
		Route:     routeKindForPath(r.URL.Path),
		IsCrawler: isCrawlerPreviewRequest(r),
	}
}

func (s *Server) appShellBootstrapJSON(r *http.Request, base BasePageData) template.JS {
	payload := appShellBootstrap{
		Version:       1,
		AssetBasePath: base.AssetBasePath,
		Route:         appShellRouteContextPayload(r),
		Viewer: appShellBootstrapViewer{
			PubKey:     normalizedViewerPubkey(viewerFromRequest(r)),
			SeedPubKey: normalizedViewerPubkey(seedPubkeyFromRequest(r)),
			Relays:     s.requestRelays(r),
		},
		Features: map[string]bool{
			"documentNavigation": true,
			"indexedDb":          true,
			"browserWrites":      true,
			// A desktop package has no production relay cache behind its
			// loopback origin. Route hydration must therefore fall back to the
			// browser's selected relays when the local store is empty or partial.
			"directRelayReads":         s.cfg.DesktopMode,
			"relayNativeRoutesPrimary": s.cfg.DesktopMode,
			"sharePreviewWarm":         true,
			"crawlerPreviewSSR":        false,
			"aboutSSR":                 true,
		},
		Guest: appShellBootstrapGuest{
			DefaultWOTSeedNPub: defaultLoggedOutWOTSeedNPub,
			DefaultWOTDepth:    defaultLoggedOutWOTDepth,
		},
	}
	if payload.Route.Route == "profile" {
		payload.InitialProfile = s.appShellInitialProfile(r)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return template.JS(`{"version":1,"assetBasePath":"/static","route":{"path":"/","search":"","hash":"","route":"","isCrawler":false},"viewer":{"pubkey":"","seedPubkey":"","relays":[]},"features":{"documentNavigation":true,"indexedDb":true,"browserWrites":true,"directRelayReads":false,"relayNativeRoutesPrimary":false,"sharePreviewWarm":true,"crawlerPreviewSSR":false,"aboutSSR":true},"guest":{"defaultWOTSeedNpub":"","defaultWOTDepth":0}}`)
	}
	return template.JS(encoded)
}

func (s *Server) appShellInitialProfile(r *http.Request) *appShellBootstrapProfile {
	if r == nil || r.URL == nil {
		return nil
	}
	identifier := r.URL.Path
	if len(identifier) <= len("/u/") || identifier[:len("/u/")] != "/u/" {
		return nil
	}
	pubkey, err := nostrx.DecodeIdentifier(identifier[len("/u/"):])
	if err != nil || pubkey == "" {
		return nil
	}
	profile := s.profile(r.Context(), pubkey)
	if profile.PubKey == "" {
		return nil
	}
	out := &appShellBootstrapProfile{
		PubKey:      profile.PubKey,
		Name:        profile.Name,
		DisplayName: profile.Display,
		About:       profile.About,
		Picture:     profile.Picture,
		Website:     profile.Website,
		NIP05:       profile.NIP05,
		Lud16:       profile.Lud16,
		Lud06:       profile.Lud06,
		RelayHints:  s.userRelays(r.Context(), pubkey),
	}
	if event, err := s.store.LatestReplaceable(r.Context(), pubkey, nostrx.KindProfileMetadata); err == nil && event != nil {
		out.EventID = event.ID
		out.CreatedAt = event.CreatedAt
	} else if profile.Event != nil {
		out.EventID = profile.Event.ID
		out.CreatedAt = profile.Event.CreatedAt
	}
	return out
}

func (s *Server) renderAppShell(w http.ResponseWriter, r *http.Request, title, active string, hideTrending bool) {
	base := s.basePageData(r, title, active, "feed-shell")
	base.HideTrendingRail = hideTrending
	s.renderAppShellWithBase(w, r, base)
}

func (s *Server) renderAppShellWithBase(w http.ResponseWriter, r *http.Request, base BasePageData) {
	setAppShellCache(w, r)
	s.render(w, "app_shell", AppShellPageData{
		BasePageData:     base,
		RouteContextJSON: appShellRouteContextJSON(r),
		AppBootstrapJSON: s.appShellBootstrapJSON(r, base),
		InitialRouteHTML: s.appShellInitialRouteHTML(r),
	})
}

func threadAppShellDocumentRequest(r *http.Request) bool {
	if r == nil || r.URL == nil || r.Method != http.MethodGet {
		return false
	}
	if r.URL.Query().Get("fragment") != "" || strings.TrimSpace(r.URL.Query().Get("tgiv")) != "" {
		return false
	}
	if detectPreviewStyle(r) != styleNormal {
		return false
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	if accept != "" && !strings.Contains(accept, "text/html") {
		return false
	}
	dest := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest")))
	if dest == "document" {
		return true
	}
	mode := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Mode")))
	if mode == "navigate" {
		return true
	}
	return strings.TrimSpace(r.Header.Get("Upgrade-Insecure-Requests")) == "1" && !isBotLikeRequest(r)
}

// appShellInitialRouteHTML inlines cached guest feed HTML for canonical home
// loads so mobile Safari first paint does not depend on relay WebSockets or
// IndexedDB. Personalized requests keep an empty outlet for client hydration.
func (s *Server) appShellInitialRouteHTML(r *http.Request) template.HTML {
	if s == nil || r == nil || r.URL == nil {
		return ""
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/thread/") {
		return s.appShellInitialThreadRouteHTML(r)
	}
	if path != "/" && path != "/feed" {
		return ""
	}
	if appShellRequestIsPersonalized(r) {
		return ""
	}
	req := s.feedRequestFromHTTP(r)
	data := s.homeFeedShellPageData(r.Context(), req)
	if len(data.Feed) == 0 {
		s.scheduleGuestFeedFragmentWarm(req)
		return ""
	}
	data.BasePageData = s.basePageData(r, "Nostr Feed", "feed", "feed-shell")
	return s.renderTemplateHTML("feed_app_route", data)
}

func (s *Server) appShellInitialThreadRouteHTML(r *http.Request) template.HTML {
	if r == nil || r.URL == nil {
		return ""
	}
	selected := r.URL.Path
	if r.URL.RawQuery != "" {
		selected += "?" + r.URL.RawQuery
	}
	selected = template.HTMLEscapeString(selected)
	statuses := "reading cached thread...||hydrating parent notes...||checking relay replies...||assembling thread view..."
	return template.HTML(`<section class="feed-column" data-route-outlet="main" data-shell-main data-thread-route-pending="` + selected + `">
  <section id="thread-summary" data-thread-fragment="summary">
    <section class="feed-loader retro-loader retro-loader--compact thread-telemetry-loader" data-feed-loader data-retro-loader data-retro-loader-type="thread" data-retro-loader-title="" data-retro-loader-statuses="` + statuses + `" data-retro-loader-complete="thread ready." data-retro-loader-progress-width="30" data-retro-loader-status-window="4" data-retro-loader-quiet-after-ms="0" aria-busy="true">
      <div class="retro-loader-block">
        <p class="muted retro-loader-summary" data-retro-loader-summary>hydrating the thread from cache and relays.</p>
        <div class="retro-loader-progress-block">
          <pre class="retro-loader-progress" data-retro-loader-progress aria-live="polite">------------------------------ 0%</pre>
        </div>
        <div class="retro-loader-activity-block">
          <pre class="retro-loader-activity" data-retro-loader-activity aria-live="polite">opening live thread status stream</pre>
        </div>
      </div>
    </section>
  </section>
  <section id="thread-tree-view" data-thread-fragment="tree" hidden></section>
  <section id="thread-ancestors" data-thread-fragment="ancestors"></section>
  <section id="thread-focus" data-thread-fragment="focus"></section>
  <section class="thread-replies">
    <div id="thread-replies" data-thread-fragment="replies"></div>
    <button class="load-more" type="button" data-thread-load-more data-load-label="Load more thread replies" data-cursor="" data-cursor-id="" hidden>Load more thread replies</button>
  </section>
</section>
<aside class="right-rail" data-route-outlet="right-rail" data-thread-fragment="participants">
  <section class="thread-people-panel">
    <h2>People in this thread</h2>
    <ul class="thread-people" aria-hidden="true"></ul>
  </section>
</aside>`)
}

func (s *Server) renderTemplateHTML(name string, data any) template.HTML {
	if s == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("template render failed", "template", name, "err", err)
		return ""
	}
	return template.HTML(buf.String())
}

func setAppShellCache(w http.ResponseWriter, r *http.Request) {
	if w == nil {
		return
	}
	if appShellRequestIsPersonalized(r) {
		w.Header().Set("Cache-Control", "private, no-store")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60, s-maxage=600, stale-while-revalidate=3600")
}

func appShellRequestIsPersonalized(r *http.Request) bool {
	if r == nil {
		return true
	}
	for _, header := range []string{
		headerViewerPubkey,
		headerWotSeed,
		headerRelays,
		headerFeedSort,
		headerFeedTrendingTf,
		headerReadsTrendingTf,
		headerWotEnabled,
		headerWotDepth,
	} {
		if strings.TrimSpace(r.Header.Get(header)) != "" {
			return true
		}
	}
	q := r.URL.Query()
	for _, key := range []string{
		"pubkey",
		"seed_pubkey",
		"relays",
		"relay",
		"sort",
		"tf",
		"reads_tf",
		"wot",
		"wot_depth",
	} {
		if q.Has(key) {
			return true
		}
	}
	return false
}
