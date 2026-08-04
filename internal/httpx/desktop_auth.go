package httpx

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const desktopSessionCookie = "ptxt_desktop_token"

// desktopLoopbackAuth makes the loopback server private to the Electron
// process. Loopback is not a trust boundary: another local process can send
// requests to 127.0.0.1, so the per-launch token protects every application
// route rather than only the activity endpoint.
func (s *Server) desktopLoopbackAuth(next http.Handler) http.Handler {
	if s == nil || !s.runtimeCapabilities().DesktopShell || s.cfg.DesktopSessionToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if desktopUnauthenticatedPath(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !desktopRequestAuthorized(r, s.cfg.DesktopSessionToken) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func desktopUnauthenticatedPath(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	return r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/static/")
}

func desktopRequestAuthorized(r *http.Request, token string) bool {
	if r == nil || token == "" {
		return false
	}
	candidate := strings.TrimSpace(r.Header.Get("X-Ptxt-Desktop-Token"))
	if candidate == "" {
		if cookie, err := r.Cookie(desktopSessionCookie); err == nil {
			candidate = cookie.Value
		}
	}
	return len(candidate) == len(token) && subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1
}
