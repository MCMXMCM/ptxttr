package httpx

import (
	"html"
	"html/template"
	"strconv"
	"strings"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

// isFeedThreadReply is true when the text note replies to another note in the
// thread (NIP-10), using the same RootID/ParentID rules as the thread builder.
func isFeedThreadReply(ev nostrx.Event) bool {
	if ev.Kind != nostrx.KindTextNote {
		return false
	}
	root := thread.RootID(ev)
	parent := thread.ParentID(root, ev)
	if parent == "" {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(parent), strings.TrimSpace(ev.ID))
}

// replyContextVisible is true when the feed should show the "Replying to …" row.
func replyContextVisible(ev nostrx.Event) bool {
	return isFeedThreadReply(ev) && !isQuotePost(ev)
}

// replyContextTargets returns ordered unique p-tag pubkeys, excluding the author.
func replyContextTargets(ev nostrx.Event) []string {
	author := strings.TrimSpace(ev.PubKey)
	seen := make(map[string]bool)
	var out []string
	for _, tag := range ev.Tags {
		if len(tag) < 2 || tag[0] != "p" {
			continue
		}
		pk, err := nostrx.NormalizePubKey(tag[1])
		if err != nil {
			continue
		}
		if seen[pk] {
			continue
		}
		if author != "" && strings.EqualFold(pk, author) {
			continue
		}
		seen[pk] = true
		out = append(out, pk)
	}
	return out
}

func replyMentionLink(profiles map[string]nostrx.Profile, pubkey string) string {
	pk := thread.NormalizeHexEventID(strings.TrimSpace(pubkey))
	if len(pk) != 64 {
		return ""
	}
	label := authorLabel(profiles, pk)
	if label == "" {
		label = short(pk)
	}
	escaped := html.EscapeString("@" + label)
	href := "/u/" + html.EscapeString(pk)
	return `<a href="` + href + `" data-relay-aware>` + escaped + `</a>`
}

// replyContextHTML builds safe HTML for the reply context row (no outer wrapper).
func replyContextHTML(profiles map[string]nostrx.Profile, ev nostrx.Event) template.HTML {
	if !replyContextVisible(ev) {
		return ""
	}
	root := thread.RootID(ev)
	parent := thread.ParentID(root, ev)
	parent = thread.NormalizeHexEventID(strings.TrimSpace(parent))

	targets := replyContextTargets(ev)
	var b strings.Builder
	b.WriteString(`<span class="note-feed-context-lead">Replying to </span>`)

	if len(targets) == 0 {
		if len(parent) != 64 {
			return template.HTML(b.String())
		}
		b.WriteString(`<a href="/thread/`)
		b.WriteString(html.EscapeString(parent))
		b.WriteString(`" data-relay-aware>thread</a>`)
		return template.HTML(b.String())
	}

	show := targets
	rest := 0
	if len(show) > 2 {
		rest = len(show) - 2
		show = show[:2]
	}
	for i, pk := range show {
		if i > 0 {
			b.WriteString(` `)
		}
		if link := replyMentionLink(profiles, pk); link != "" {
			b.WriteString(link)
		}
	}
	if rest > 0 {
		b.WriteString(` <span class="note-feed-context-tail">and `)
		b.WriteString(strconv.Itoa(rest))
		b.WriteString(` other`)
		if rest != 1 {
			b.WriteString(`s`)
		}
		b.WriteString(`</span>`)
	}
	return template.HTML(b.String())
}

// repostContextHTML builds safe HTML for the repost banner (no outer wrapper).
func repostContextHTML(profiles map[string]nostrx.Profile, ev nostrx.Event) template.HTML {
	if ev.Kind != nostrx.KindRepost {
		return ""
	}
	pk := thread.NormalizeHexEventID(strings.TrimSpace(ev.PubKey))
	name := html.EscapeString(displayName(profiles, pk))
	var b strings.Builder
	b.WriteString(`<span class="note-feed-context-repost-inner">`)
	b.WriteString(name)
	b.WriteString(` reposted</span>`)
	return template.HTML(b.String())
}
