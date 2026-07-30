package httpx

import "strings"

const (
	searchScopeAll     = "all"
	searchScopeNetwork = "network"
	searchModeNotes    = "notes"
	searchModeUsers    = "users"
)

func normalizeSearchMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), searchModeUsers) {
		return searchModeUsers
	}
	return searchModeNotes
}

func normalizeSearchScope(scope string, loggedOut bool, wotEnabled bool) string {
	if loggedOut || !wotEnabled {
		return searchScopeAll
	}
	if strings.EqualFold(strings.TrimSpace(scope), searchScopeAll) {
		return searchScopeAll
	}
	return searchScopeNetwork
}

func searchScopeLabel(scope string) string {
	if scope == searchScopeNetwork {
		return "current network"
	}
	return "all cached notes"
}
