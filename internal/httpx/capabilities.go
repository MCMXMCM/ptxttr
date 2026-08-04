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
		LocalFirst:   desktop,
		DesktopShell: desktop,
		// The desktop sidecar is the one local authority for relay I/O and
		// durable data. Keeping renderer relay reads enabled creates a second
		// event store (IndexedDB) that can disagree with SQLite after a restart.
		DirectRelayReads:         false,
		RelayNativeRoutesPrimary: false,
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
	// Desktop requests are allowed to hydrate through the local sidecar. This
	// is intentionally separate from DirectRelayReads, which describes the
	// browser's relay-native fallback rather than the sidecar's relay client.
	if s.runtimeCapabilities().DesktopShell || s.runtimeCapabilities().DirectRelayReads {
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
