package httpx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

const (
	nip05VerificationCacheTTL    = time.Hour
	nip05VerificationCacheMaxLen = 1024
	nip05VerificationTimeout     = 5 * time.Second
	nip05MaxBodyBytes            = 256 << 10
	nip05UserAgent               = "ptxt-nstr/1 (+https://plaintextnostr.com) nip05-verifier"
)

func newNIP05VerificationCache() *ttlCache[nostrx.NIP05VerificationResult] {
	return newTTLCache[nostrx.NIP05VerificationResult](nip05VerificationCacheTTL, nip05VerificationCacheMaxLen, nil)
}

func nip05VerificationCacheKey(identifier, pubkey string) string {
	id := strings.ToLower(strings.TrimSpace(identifier))
	pk := nostrx.CanonicalHex64(pubkey)
	if id == "" || pk == "" {
		return ""
	}
	return id + "|" + pk
}

func (s *Server) verifyNIP05Cached(ctx context.Context, identifier, pubkey string) nostrx.NIP05VerificationResult {
	key := nip05VerificationCacheKey(identifier, pubkey)
	if key == "" {
		return nostrx.NIP05InvalidIdentifier
	}
	now := time.Now()
	if cached, ok := s.nip05Cache.get(key, now); ok {
		return cached
	}
	status := s.verifyNIP05(ctx, identifier, pubkey)
	s.nip05Cache.put(key, status, now)
	return status
}

// cachedNIP05Verification is the request-safe projection read used by guest
// SSR. It never performs DNS or HTTP work; unknown is intentionally stable.
func (s *Server) cachedNIP05Verification(ctx context.Context, identifier, pubkey string) nostrx.NIP05VerificationResult {
	if s == nil || s.store == nil || strings.TrimSpace(identifier) == "" || strings.TrimSpace(pubkey) == "" {
		return ""
	}
	record, ok, err := s.store.GetNIP05Verification(ctx, identifier, pubkey)
	if err != nil || !ok {
		return ""
	}
	return nostrx.NIP05VerificationResult(record.Status)
}

func (s *Server) verifyNIP05(ctx context.Context, identifier, pubkey string) nostrx.NIP05VerificationResult {
	id, ok := nostrx.ParseNIP05Identifier(identifier)
	if !ok {
		return nostrx.NIP05InvalidIdentifier
	}
	u, ok := id.WellKnownURL()
	if !ok {
		return nostrx.NIP05InvalidIdentifier
	}
	fetchCtx, cancel := context.WithTimeout(ctx, nip05VerificationTimeout)
	defer cancel()
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("nip05: too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("nip05: insecure redirect")
			}
			return nil
		},
	}
	doc, status := fetchNIP05Document(fetchCtx, client, u.String())
	if status != "" {
		if status != nostrx.NIP05Unreachable {
			return status
		}
		slog.Debug("nip05 verification unreachable", "identifier", id.Raw, "domain", id.Domain)
		return status
	}
	return nostrx.VerifyNIP05Document(doc, id.LocalPart, pubkey)
}

func fetchNIP05Document(ctx context.Context, client *http.Client, rawURL string) (nostrx.NIP05WellKnownDocument, nostrx.NIP05VerificationResult) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nostrx.NIP05WellKnownDocument{}, nostrx.NIP05InvalidIdentifier
	}
	req.Header.Set("Accept", "application/json, application/nostr+json;q=0.9")
	req.Header.Set("User-Agent", nip05UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "redirect") {
			return nostrx.NIP05WellKnownDocument{}, nostrx.NIP05RedirectRejected
		}
		return nostrx.NIP05WellKnownDocument{}, nostrx.NIP05Unreachable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nostrx.NIP05WellKnownDocument{}, nostrx.NIP05Unreachable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, nip05MaxBodyBytes+1))
	if err != nil || len(body) > nip05MaxBodyBytes {
		return nostrx.NIP05WellKnownDocument{}, nostrx.NIP05Unreachable
	}
	doc, err := nostrx.DecodeNIP05WellKnownDocument(body)
	if err != nil {
		return nostrx.NIP05WellKnownDocument{}, nostrx.NIP05Unreachable
	}
	return doc, ""
}

func nip05StatusLabel(status nostrx.NIP05VerificationResult) string {
	switch status {
	case nostrx.NIP05Verified:
		return "NIP-5 verified for this profile."
	case nostrx.NIP05PubkeyMismatch:
		return "NIP-5 points to a different pubkey."
	case nostrx.NIP05NameNotFound:
		return "This name was not found in the site's NIP-5 record."
	case nostrx.NIP05RedirectRejected:
		return "The NIP-5 lookup redirect was rejected."
	case nostrx.NIP05Unreachable:
		return "The NIP-5 record could not be reached."
	case nostrx.NIP05InvalidIdentifier:
		return "This NIP-5 identifier is invalid."
	default:
		return "Unknown NIP-5 verification state."
	}
}
