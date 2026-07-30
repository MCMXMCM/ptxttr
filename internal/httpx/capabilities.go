package httpx

import (
	"strings"
)

func (s *Server) shareServerMode() bool {
	return s != nil && s.cfg.ServerMode == "share"
}

func (s *Server) allowLegacyRelayBackend() bool {
	return s != nil && !s.shareServerMode()
}

func (s *Server) allowLegacyWarmers() bool {
	return s != nil && !s.shareServerMode()
}

func (s *Server) allowShareSurfaceRelayFetch(viewerPub string, loggedOut bool) bool {
	if s == nil {
		return false
	}
	if !s.shareServerMode() {
		return allowSyncRelayWork(viewerPub, loggedOut)
	}
	return loggedOut && strings.TrimSpace(viewerPub) == ""
}

func (s *Server) allowThreadRelayFetch(viewerPub string, loggedOut bool, fragment string) bool {
	if s == nil {
		return false
	}
	if loggedOut || strings.TrimSpace(viewerPub) == "" {
		return false
	}
	if !s.shareServerMode() {
		return allowSyncRelayWork(viewerPub, loggedOut) || threadFragmentUsesRelayFetch(fragment)
	}
	return false
}
