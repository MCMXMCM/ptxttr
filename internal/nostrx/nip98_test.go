package nostrx

import (
	"strings"
	"testing"
	"time"

	fnostr "fiatjaf.com/nostr"
)

func TestValidateNIP98HTTPAuth_AcceptsValidToken(t *testing.T) {
	secret := fnostr.Generate()
	url := "https://example.com/api/mute-list"
	ev := signedNIP98Event(t, secret, url, "GET", time.Unix(1700000000, 0))
	now := time.Unix(1700000000, 0)
	if err := ValidateNIP98HTTPAuth(ev, "GET", url, now, DefaultNIP98Skew); err != nil {
		t.Fatalf("ValidateNIP98HTTPAuth: %v", err)
	}
}

func TestValidateNIP98HTTPAuth_WrongKind(t *testing.T) {
	secret := fnostr.Generate()
	url := "https://example.com/api/mute-list"
	at := time.Now().Truncate(time.Second)
	ev2 := signedMutationLike(t, secret, KindTextNote, "", [][]string{{"u", url}, {"method", "GET"}}, at)
	if err := ValidateNIP98HTTPAuth(ev2, "GET", url, at, DefaultNIP98Skew); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("want kind error, got %v", err)
	}
}

func TestValidateNIP98HTTPAuth_WrongURL(t *testing.T) {
	secret := fnostr.Generate()
	ev := signedNIP98Event(t, secret, "https://example.com/api/mute-list", "GET", time.Unix(1700000000, 0))
	if err := ValidateNIP98HTTPAuth(ev, "GET", "https://evil.com/api/mute-list", time.Unix(1700000000, 0), DefaultNIP98Skew); err == nil {
		t.Fatal("expected u tag mismatch")
	}
}

func TestValidateNIP98HTTPAuth_WrongMethod(t *testing.T) {
	secret := fnostr.Generate()
	url := "https://example.com/api/mute-list"
	ev := signedNIP98Event(t, secret, url, "GET", time.Unix(1700000000, 0))
	if err := ValidateNIP98HTTPAuth(ev, "POST", url, time.Unix(1700000000, 0), DefaultNIP98Skew); err == nil {
		t.Fatal("expected method mismatch")
	}
}

func TestValidateNIP98HTTPAuth_ExpiredTimestamp(t *testing.T) {
	secret := fnostr.Generate()
	url := "https://example.com/api/mute-list"
	ev := signedNIP98Event(t, secret, url, "GET", time.Unix(1700000000, 0))
	now := time.Unix(1700000100, 0) // 100s later
	if err := ValidateNIP98HTTPAuth(ev, "GET", url, now, DefaultNIP98Skew); err == nil {
		t.Fatal("expected skew error")
	}
}

func TestValidateNIP98HTTPAuth_PayloadTagRejected(t *testing.T) {
	secret := fnostr.Generate()
	url := "https://example.com/api/mute-list"
	ev := signedMutationLike(t, secret, KindHTTPAuth, "", [][]string{{"u", url}, {"method", "GET"}, {"payload", "abc"}})
	if err := ValidateNIP98HTTPAuth(ev, "GET", url, time.Unix(ev.CreatedAt, 0), DefaultNIP98Skew); err == nil {
		t.Fatal("expected payload tag error")
	}
}

func TestValidateNIP98HTTPAuth_InvalidSignature(t *testing.T) {
	secret := fnostr.Generate()
	url := "https://example.com/api/mute-list"
	ev := signedNIP98Event(t, secret, url, "GET", time.Unix(1700000000, 0))
	ev.Sig = strings.Repeat("a", 128)
	if err := ValidateNIP98HTTPAuth(ev, "GET", url, time.Unix(1700000000, 0), DefaultNIP98Skew); err == nil {
		t.Fatal("expected signature error")
	}
}

func signedNIP98Event(t *testing.T, secret fnostr.SecretKey, u, method string, at time.Time) Event {
	t.Helper()
	return signedMutationLike(t, secret, KindHTTPAuth, "", [][]string{{"u", u}, {"method", method}}, at)
}

func signedMutationLike(t *testing.T, secret fnostr.SecretKey, kind int, content string, tags [][]string, at ...time.Time) Event {
	t.Helper()
	ts := fnostr.Now()
	if len(at) > 0 {
		ts = fnostr.Timestamp(at[0].Unix())
	}
	external := fnostr.Event{
		CreatedAt: ts,
		Kind:      fnostr.Kind(kind),
		Content:   content,
	}
	external.Tags = make(fnostr.Tags, 0, len(tags))
	for _, tag := range tags {
		external.Tags = append(external.Tags, fnostr.Tag(tag))
	}
	if err := external.Sign(secret); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return fromExternalEvent(external)
}
