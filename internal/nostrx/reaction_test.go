package nostrx

import (
	"strings"
	"testing"
)

func TestReactionLastETagID_LastWins(t *testing.T) {
	ev := Event{
		Tags: [][]string{
			{"e", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			{"p", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			{"e", "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		},
	}
	got := ReactionLastETagID(ev)
	want := strings.Repeat("c", 64)
	if got != want {
		t.Fatalf("ReactionLastETagID = %q, want %q", got, want)
	}
}

func TestReactionPolarity(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"+", 1},
		{"", 1},
		{"  +  ", 1},
		{"-", -1},
		{"  - ", -1},
		{"👍", 0},
		{"like", 0},
	}
	for _, tc := range tests {
		if got := ReactionPolarity(tc.in); got != tc.want {
			t.Fatalf("ReactionPolarity(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestValidateReactionHTTPAPIShape(t *testing.T) {
	noteID := strings.Repeat("a", 64)
	author := strings.Repeat("b", 64)
	good := Event{Kind: KindReaction, Content: "+", Tags: [][]string{{"e", noteID}, {"p", author}}}
	if err := ValidateReactionHTTPAPIShape(good); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateReactionHTTPAPIShape(Event{Kind: KindReaction, Content: "", Tags: [][]string{{"e", noteID}}}); err != nil {
		t.Fatalf("empty upvote: %v", err)
	}
	if err := ValidateReactionHTTPAPIShape(Event{Kind: KindReaction, Content: "-", Tags: [][]string{{"e", noteID}}}); err != nil {
		t.Fatalf("downvote: %v", err)
	}
	if err := ValidateReactionHTTPAPIShape(Event{Kind: KindTextNote, Content: "+", Tags: [][]string{{"e", noteID}}}); err == nil {
		t.Fatal("expected error for non-reaction kind")
	}
	if err := ValidateReactionHTTPAPIShape(Event{Kind: KindReaction, Content: "👍", Tags: [][]string{{"e", noteID}}}); err == nil {
		t.Fatal("expected error for emoji reaction")
	}
	if err := ValidateReactionHTTPAPIShape(Event{Kind: KindReaction, Content: "+", Tags: nil}); err == nil {
		t.Fatal("expected error when e tag missing")
	}
}
