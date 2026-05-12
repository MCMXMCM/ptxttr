package httpx

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleTag(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.tag", time.Now())
	tag, err := parseTagFromRequestPath(r.URL.Path)
	if err != nil {
		s.renderNotFound(w, "error_shell", ErrorPageData{
			BasePageData: s.basePageData(r, "Not found", "feed", "feed-shell"),
			ErrorPanelCopy: ErrorPanelCopy{
				Heading: "Page not found",
				Message: "That hashtag URL is not valid.",
				Detail:  r.URL.Path,
			},
		})
		return
	}
	req := s.tagRequestFromHTTP(r, tag)
	rateKeys := []string{"ip:" + searchRemoteIP(r)}
	if viewer := normalizeViewerKey(req.Pubkey); viewer != "" {
		rateKeys = append(rateKeys, "viewer:"+viewer)
	}
	status := 0
	if !s.searchLimiter.allow(time.Now(), rateKeys...) {
		s.metrics.Add("search.ratelimit.deny", 1)
		w.Header().Set("Retry-After", "1")
		status = http.StatusTooManyRequests
	}
	var data TagPageData
	if status == 0 {
		data = s.tagData(r.Context(), s.newTagPlan(r.Context(), req))
		data.BasePageData = s.basePageData(r, fmt.Sprintf("#%s", tag), "tag", "feed-shell")
		data.ScopeAllURL = s.tagScopeURL(r, req, searchScopeAll)
		data.ScopeNetworkURL = s.tagScopeURL(r, req, searchScopeNetwork)
	} else {
		data = s.newTagPageData(r, req, tag)
	}
	s.renderTagPage(w, r, status, data)
}

func (s *Server) newTagPageData(r *http.Request, req tagRequest, tag string) TagPageData {
	data := TagPageData{
		Tag:             tag,
		TagPath:         url.PathEscape(tag),
		Scope:           searchScopeAll,
		ScopeLabel:      searchScopeLabel(searchScopeAll),
		ScopeAllURL:     s.tagScopeURL(r, req, searchScopeAll),
		ScopeNetworkURL: s.tagScopeURL(r, req, searchScopeNetwork),
	}
	data.BasePageData = s.basePageData(r, fmt.Sprintf("#%s", tag), "tag", "feed-shell")
	return data
}

func (s *Server) renderTagPage(w http.ResponseWriter, r *http.Request, status int, data TagPageData) {
	name := "tag"
	switch r.URL.Query().Get("fragment") {
	case "1":
		name = "tag_items"
		setPaginationHeaders(w, data.Cursor, data.CursorID, data.HasMore)
	case "main":
		name = "tag_main"
	}
	if status == 0 {
		s.render(w, name, data)
		return
	}
	s.renderStatus(w, status, name, data)
}

func (s *Server) tagRequestFromHTTP(r *http.Request, tag string) tagRequest {
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	return tagRequest{
		Pubkey:     viewerFromRequest(r),
		SeedPubkey: seedPubkeyFromRequest(r),
		Tag:        tag,
		Scope:      strings.TrimSpace(r.URL.Query().Get("scope")),
		Cursor:     cursor,
		CursorID:   r.URL.Query().Get("cursor_id"),
		Limit:      30,
		Relays:     s.requestRelays(r),
		WoT:        webOfTrustOptionsFromRequest(r),
	}
}

// tagScopeURL builds /tag/<tag>?... links. Viewer pubkey, WoT seed, WoT
// settings and relays are intentionally omitted: the client sends those as
// request headers so the URL stays cache-key-shared across all viewers.
func (s *Server) tagScopeURL(_ *http.Request, req tagRequest, scope string) string {
	values := url.Values{}
	if scope == searchScopeAll {
		values.Set("scope", searchScopeAll)
	} else {
		values.Set("scope", searchScopeNetwork)
	}
	return "/tag/" + url.PathEscape(req.Tag) + "?" + values.Encode()
}
