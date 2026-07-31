package httpx

import (
	"strings"
)

type runtimeCapabilities struct {
	LocalFirst               bool
	DesktopShell             bool
	DirectRelayReads         bool
	RelayNativeRoutesPrimary bool
	StorageControls          bool
	BrowserExtensionSigner   bool
	HostedGuestAdmission     bool
}

func (s *Server) runtimeCapabilities() runtimeCapabilities {
	desktop := s != nil && s.cfg.DesktopMode
	return runtimeCapabilities{
		LocalFirst:               desktop,
		DesktopShell:             desktop,
		DirectRelayReads:         desktop,
		RelayNativeRoutesPrimary: desktop,
		StorageControls:          desktop,
		BrowserExtensionSigner:   !desktop,
		HostedGuestAdmission:     !desktop,
	}
}

func (s *Server) shareServerMode() bool {
	return s != nil && s.cfg.ServerMode == "share"
}

func (s *Server) allowLegacyRelayBackend() bool {
	return s != nil && (s.runtimeCapabilities().LocalFirst || !s.shareServerMode())
}

func (s *Server) allowLegacyWarmers() bool {
	return s != nil && (s.runtimeCapabilities().LocalFirst || !s.shareServerMode())
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
	if s.runtimeCapabilities().DirectRelayReads {
		return true
	}
	if loggedOut || strings.TrimSpace(viewerPub) == "" {
		return false
	}
	if !s.shareServerMode() {
		return allowSyncRelayWork(viewerPub, loggedOut) || threadFragmentUsesRelayFetch(fragment)
	}
	return false
}
