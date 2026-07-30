package nostrx

import (
	"encoding/json"
	"net/url"
	"strings"
)

type NIP05Identifier struct {
	Raw       string
	LocalPart string
	Domain    string
}

type NIP05VerificationResult string

const (
	NIP05Verified          NIP05VerificationResult = "verified"
	NIP05PubkeyMismatch    NIP05VerificationResult = "pubkeyMismatch"
	NIP05NameNotFound      NIP05VerificationResult = "nameNotFound"
	NIP05RedirectRejected  NIP05VerificationResult = "redirectRejected"
	NIP05Unreachable       NIP05VerificationResult = "unreachable"
	NIP05InvalidIdentifier NIP05VerificationResult = "invalidIdentifier"
)

type NIP05WellKnownDocument struct {
	Names map[string]string `json:"names"`
}

func ParseNIP05Identifier(value string) (*NIP05Identifier, bool) {
	trimmed := strings.TrimSpace(value)
	at := strings.LastIndex(trimmed, "@")
	if at <= 0 || at >= len(trimmed)-1 {
		return nil, false
	}
	localPart := strings.ToLower(strings.TrimSpace(trimmed[:at]))
	domain := strings.ToLower(strings.TrimSpace(trimmed[at+1:]))
	if localPart == "" || domain == "" {
		return nil, false
	}
	for _, ch := range localPart {
		if !isNIP05LocalPartRune(ch) {
			return nil, false
		}
	}
	if strings.ContainsAny(domain, "/ @") {
		return nil, false
	}
	return &NIP05Identifier{
		Raw:       trimmed,
		LocalPart: localPart,
		Domain:    domain,
	}, true
}

func isNIP05LocalPartRune(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '-' || ch == '_' || ch == '.'
}

func (id NIP05Identifier) WellKnownURL() (*url.URL, bool) {
	if id.Domain == "" {
		return nil, false
	}
	u, err := url.Parse("https://" + id.Domain + "/.well-known/nostr.json")
	if err != nil {
		return nil, false
	}
	q := u.Query()
	q.Set("name", id.LocalPart)
	u.RawQuery = q.Encode()
	return u, true
}

func VerifyNIP05Document(doc NIP05WellKnownDocument, localPart, expectedPubkey string) NIP05VerificationResult {
	expected := CanonicalHex64(expectedPubkey)
	if expected == "" || len(expected) != 64 {
		return NIP05InvalidIdentifier
	}
	found, ok := nip05PubkeyForName(doc, localPart)
	if !ok {
		return NIP05NameNotFound
	}
	found = CanonicalHex64(found)
	if found == "" || len(found) != 64 {
		return NIP05NameNotFound
	}
	if found != expected {
		return NIP05PubkeyMismatch
	}
	return NIP05Verified
}

func nip05PubkeyForName(doc NIP05WellKnownDocument, localPart string) (string, bool) {
	localPart = strings.ToLower(strings.TrimSpace(localPart))
	if localPart == "" {
		return "", false
	}
	if doc.Names == nil {
		return "", false
	}
	if pubkey, ok := doc.Names[localPart]; ok && strings.TrimSpace(pubkey) != "" {
		return pubkey, true
	}
	for name, pubkey := range doc.Names {
		if strings.EqualFold(name, localPart) && strings.TrimSpace(pubkey) != "" {
			return pubkey, true
		}
	}
	return "", false
}

func DecodeNIP05WellKnownDocument(data []byte) (NIP05WellKnownDocument, error) {
	var doc NIP05WellKnownDocument
	err := json.Unmarshal(data, &doc)
	return doc, err
}

func NIP05DisplayText(raw string) string {
	id, ok := ParseNIP05Identifier(raw)
	if !ok {
		return strings.TrimSpace(raw)
	}
	if id.LocalPart == "_" {
		return id.Domain
	}
	return id.Raw
}
