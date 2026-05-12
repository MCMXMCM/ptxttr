package nostrx

import (
	"strings"
	"testing"

	fnostr "fiatjaf.com/nostr"
)

func TestNegentropySupportedFilter(t *testing.T) {
	pk, err := fnostr.PubKeyFromHex("a" + strings.Repeat("b", 63))
	if err != nil {
		t.Fatal(err)
	}
	if NegentropySupportedFilter(fnostr.Filter{Tags: fnostr.TagMap{"e": {"x"}}}) {
		t.Fatal("tag map should be unsupported")
	}
	if NegentropySupportedFilter(fnostr.Filter{Search: "x"}) {
		t.Fatal("search should be unsupported")
	}
	if NegentropySupportedFilter(fnostr.Filter{}) {
		t.Fatal("empty filter should be unsupported")
	}
	if !NegentropySupportedFilter(fnostr.Filter{Kinds: []fnostr.Kind{1}}) {
		t.Fatal("kind-only should be supported")
	}
	if !NegentropySupportedFilter(fnostr.Filter{Authors: []fnostr.PubKey{pk}, Kinds: []fnostr.Kind{1}}) {
		t.Fatal("author+kind should be supported")
	}
}

func TestNegentropyEnvEnabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"off", false},
		{"no", false},
		{"1", true},
		{"true", true},
		{"on", true},
		{"yes", true},
		{"bogus", false},
	}
	for _, tc := range cases {
		t.Run(tc.val, func(t *testing.T) {
			t.Setenv("PTXT_NEGENTROPY", tc.val)
			if got := negentropyEnvEnabled(); got != tc.want {
				t.Fatalf("PTXT_NEGENTROPY=%q: got %v want %v", tc.val, got, tc.want)
			}
		})
	}
}
