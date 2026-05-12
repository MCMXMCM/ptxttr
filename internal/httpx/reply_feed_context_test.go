package httpx

import (
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func hex64(repeated2 string) string {
	return strings.Repeat(repeated2, 32)
}

func TestIsFeedThreadReply(t *testing.T) {
	root := hex64("aa")
	parent := hex64("bb")
	replyID := hex64("cc")
	author := hex64("01")

	replyTags := [][]string{
		{"e", root, "", "root"},
		{"e", parent, "", "reply"},
		{"p", hex64("02"), ""},
	}
	reply := nostrx.Event{
		ID:        replyID,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		Tags:      replyTags,
		Content:   "hi",
	}

	if !isFeedThreadReply(reply) {
		t.Fatal("expected reply with root+reply e-tags to be a thread reply")
	}

	rootNote := nostrx.Event{
		ID:        root,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		Tags:      nil,
		Content:   "root",
	}
	if isFeedThreadReply(rootNote) {
		t.Fatal("root note without e-tags should not be a thread reply")
	}

	quote := reply
	quote.Tags = append(append([][]string(nil), replyTags...), []string{"q", parent})
	if !isFeedThreadReply(quote) {
		t.Fatal("quote with e-tags is still a thread reply structurally")
	}
	if replyContextVisible(quote) {
		t.Fatal("quote post should not show reply feed context row")
	}
}

func TestReplyContextVisible(t *testing.T) {
	root := hex64("aa")
	parent := hex64("bb")
	replyID := hex64("cc")
	ev := nostrx.Event{
		ID:     replyID,
		PubKey: hex64("01"),
		Kind:   nostrx.KindTextNote,
		Tags: [][]string{
			{"e", root, "", "root"},
			{"e", parent, "", "reply"},
		},
	}
	if !replyContextVisible(ev) {
		t.Fatal("expected plain reply to show context")
	}
}

func TestReplyContextTargetsDedupesAndSkipsAuthor(t *testing.T) {
	author := hex64("01")
	other := hex64("02")
	ev := nostrx.Event{
		PubKey: author,
		Tags: [][]string{
			{"p", other, ""},
			{"p", author, ""},
			{"p", other, ""},
		},
	}
	got := replyContextTargets(ev)
	if len(got) != 1 || got[0] != other {
		t.Fatalf("got %#v, want single pubkey %q", got, other)
	}
}

func TestReplyContextHTML(t *testing.T) {
	root := hex64("aa")
	parent := hex64("bb")
	replyID := hex64("cc")
	author := hex64("01")
	bob := hex64("02")
	carol := hex64("03")
	dave := hex64("04")
	eve := hex64("05")

	profiles := map[string]nostrx.Profile{
		bob:   {PubKey: bob, Display: "Bob"},
		carol: {PubKey: carol, Display: "Carol"},
		dave:  {PubKey: dave, Display: "Dave"},
		eve:   {PubKey: eve, Display: "Eve"},
	}

	t.Run("two mentions no tail", func(t *testing.T) {
		ev := nostrx.Event{
			ID:     replyID,
			PubKey: author,
			Kind:   nostrx.KindTextNote,
			Tags: [][]string{
				{"e", root, "", "root"},
				{"e", parent, "", "reply"},
				{"p", bob, ""},
				{"p", carol, ""},
			},
		}
		html := string(replyContextHTML(profiles, ev))
		if !strings.Contains(html, "Replying to ") {
			t.Fatalf("missing lead: %q", html)
		}
		if !strings.Contains(html, `href="/u/`+bob+`"`) || !strings.Contains(html, `@Bob`) {
			t.Fatalf("want bob link: %q", html)
		}
		if !strings.Contains(html, `href="/u/`+carol+`"`) || !strings.Contains(html, `@Carol`) {
			t.Fatalf("want carol link: %q", html)
		}
		if strings.Contains(html, "other") {
			t.Fatalf("unexpected others tail: %q", html)
		}
	})

	t.Run("and N others", func(t *testing.T) {
		ev := nostrx.Event{
			ID:     replyID,
			PubKey: author,
			Kind:   nostrx.KindTextNote,
			Tags: [][]string{
				{"e", root, "", "root"},
				{"e", parent, "", "reply"},
				{"p", bob, ""},
				{"p", carol, ""},
				{"p", dave, ""},
				{"p", eve, ""},
			},
		}
		html := string(replyContextHTML(profiles, ev))
		if !strings.Contains(html, "and 2 others") {
			t.Fatalf("want and 2 others: %q", html)
		}
	})

	t.Run("fallback thread link when no p tags", func(t *testing.T) {
		ev := nostrx.Event{
			ID:     replyID,
			PubKey: author,
			Kind:   nostrx.KindTextNote,
			Tags: [][]string{
				{"e", root, "", "root"},
				{"e", parent, "", "reply"},
			},
		}
		html := string(replyContextHTML(nil, ev))
		if !strings.Contains(html, `href="/thread/`+parent+`"`) || !strings.Contains(html, ">thread</a>") {
			t.Fatalf("want parent thread link: %q", html)
		}
	})

	t.Run("quote post empty", func(t *testing.T) {
		ev := nostrx.Event{
			ID:     replyID,
			PubKey: author,
			Kind:   nostrx.KindTextNote,
			Tags: [][]string{
				{"e", root, "", "root"},
				{"e", parent, "", "reply"},
				{"q", parent},
				{"p", bob, ""},
			},
		}
		if got := string(replyContextHTML(profiles, ev)); got != "" {
			t.Fatalf("want empty for quote: %q", got)
		}
	})
}

func TestRepostContextHTML(t *testing.T) {
	alice := hex64("0a")
	ev := nostrx.Event{
		ID:        hex64("f0"),
		PubKey:    alice,
		Kind:      nostrx.KindRepost,
		Tags:      [][]string{{"e", hex64("aa"), ""}},
		Content:   "",
	}
	profiles := map[string]nostrx.Profile{
		alice: {PubKey: alice, Display: "Alice"},
	}
	html := string(repostContextHTML(profiles, ev))
	if !strings.Contains(html, "reposted") {
		t.Fatalf("missing reposted: %q", html)
	}
	if !strings.Contains(html, "Alice") {
		t.Fatalf("missing display name: %q", html)
	}
	if !strings.Contains(html, `note-feed-context-repost-inner`) {
		t.Fatalf("missing inner span: %q", html)
	}

	non := nostrx.Event{Kind: nostrx.KindTextNote, PubKey: alice}
	if got := string(repostContextHTML(profiles, non)); got != "" {
		t.Fatalf("kind 1 should not emit repost banner: %q", got)
	}
}
