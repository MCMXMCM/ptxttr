package httpx

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const tagMaxRunes = 64

var errInvalidTagPath = errors.New("invalid hashtag path")

// parseTagFromRequestPath extracts and validates a single /tag/{segment} value
// from r.URL.Path (must start with "/tag/", no extra path segments).
func parseTagFromRequestPath(path string) (string, error) {
	if !strings.HasPrefix(path, "/tag/") {
		return "", errInvalidTagPath
	}
	rest := strings.TrimPrefix(path, "/tag/")
	if rest == "" || strings.Contains(rest, "/") {
		return "", errInvalidTagPath
	}
	decoded, err := url.PathUnescape(rest)
	if err != nil {
		return "", errInvalidTagPath
	}
	decoded = strings.TrimSpace(strings.TrimPrefix(decoded, "#"))
	if decoded == "" {
		return "", errInvalidTagPath
	}
	if decoded == "." || decoded == ".." {
		return "", errInvalidTagPath
	}
	if utf8.RuneCountInString(decoded) > tagMaxRunes {
		return "", errInvalidTagPath
	}
	for _, r := range decoded {
		if r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) {
			continue
		}
		return "", errInvalidTagPath
	}
	return decoded, nil
}
