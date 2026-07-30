package httpx

import "net/http"

func (s *Server) handleSupport(w http.ResponseWriter, r *http.Request) {
	s.renderInfoPage(w, r, "Support", "support")
}

func (s *Server) handleMarketing(w http.ResponseWriter, r *http.Request) {
	s.renderInfoPage(w, r, "Plain Text Nostr for iOS", "marketing")
}

func (s *Server) handleTerms(w http.ResponseWriter, r *http.Request) {
	s.renderInfoPage(w, r, "Terms of Service", "terms")
}

func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	s.renderInfoPage(w, r, "Privacy Policy", "privacy")
}

func (s *Server) renderInfoPage(w http.ResponseWriter, r *http.Request, title, active string) {
	data := AboutPageData{
		BasePageData: s.basePageData(r, title, active, ""),
	}
	data.HideTrendingRail = true
	s.render(w, active, data)
}
