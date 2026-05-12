package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	fnostr "fiatjaf.com/nostr"

	"ptxt-nstr/internal/nostrx"
)

func signTestNostrEvent(t *testing.T, sk fnostr.SecretKey, createdAt fnostr.Timestamp, kind int, content string, tags [][]string) nostrx.Event {
	t.Helper()
	external := fnostr.Event{
		CreatedAt: createdAt,
		Kind:      fnostr.Kind(kind),
		Content:   content,
	}
	external.Tags = make(fnostr.Tags, 0, len(tags))
	for _, tag := range tags {
		external.Tags = append(external.Tags, fnostr.Tag(tag))
	}
	if err := external.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return nostrx.Event{
		ID:        external.ID.Hex(),
		PubKey:    external.PubKey.Hex(),
		CreatedAt: int64(external.CreatedAt),
		Kind:      int(external.Kind),
		Tags:      tags,
		Content:   external.Content,
		Sig:       fmt.Sprintf("%x", external.Sig[:]),
	}
}

func TestReactionStatsByNoteIDs_LatestVotePerReactor(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	authorSK := fnostr.Generate()
	reactorSK := fnostr.Generate()
	otherSK := fnostr.Generate()

	note := signTestNostrEvent(t, authorSK, 1000, nostrx.KindTextNote, "hello", nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}

	reactorPK := signTestNostrEvent(t, reactorSK, 1, nostrx.KindTextNote, "warm", nil).PubKey

	up := signTestNostrEvent(t, reactorSK, 2000, nostrx.KindReaction, "+", [][]string{
		{"e", note.ID},
		{"p", note.PubKey},
	})
	if err := st.SaveEvent(ctx, up); err != nil {
		t.Fatal(err)
	}
	down := signTestNostrEvent(t, reactorSK, 3000, nostrx.KindReaction, "-", [][]string{
		{"e", note.ID},
		{"p", note.PubKey},
	})
	if err := st.SaveEvent(ctx, down); err != nil {
		t.Fatal(err)
	}

	otherUp := signTestNostrEvent(t, otherSK, 2500, nostrx.KindReaction, "+", [][]string{
		{"e", note.ID},
		{"p", note.PubKey},
	})
	if err := st.SaveEvent(ctx, otherUp); err != nil {
		t.Fatal(err)
	}

	stats, viewers, err := st.ReactionStatsByNoteIDs(ctx, []string{note.ID}, reactorPK)
	if err != nil {
		t.Fatal(err)
	}
	s := stats[note.ID]
	if s.Up != 1 || s.Down != 1 || s.Total != 2 {
		t.Fatalf("stats = %+v, want Up=1 Down=1 Total=2", s)
	}
	if viewers[note.ID] != "-" {
		t.Fatalf("viewer polarity = %q, want -", viewers[note.ID])
	}
}

func TestReactionStatsByNoteIDs_AcceptsMixedCaseNoteAndViewerIDs(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	authorSK := fnostr.Generate()
	reactorSK := fnostr.Generate()

	note := signTestNostrEvent(t, authorSK, 1000, nostrx.KindTextNote, "hello mixed", nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	reactorPK := signTestNostrEvent(t, reactorSK, 1, nostrx.KindTextNote, "warm", nil).PubKey
	up := signTestNostrEvent(t, reactorSK, 2000, nostrx.KindReaction, "+", [][]string{
		{"e", note.ID},
		{"p", note.PubKey},
	})
	if err := st.SaveEvent(ctx, up); err != nil {
		t.Fatal(err)
	}

	stats, viewers, err := st.ReactionStatsByNoteIDs(ctx, []string{strings.ToUpper(note.ID)}, strings.ToUpper(reactorPK))
	if err != nil {
		t.Fatal(err)
	}
	s := stats[note.ID]
	if s.Up != 1 || s.Total != 1 {
		t.Fatalf("stats = %+v, want one upvote", s)
	}
	if viewers[note.ID] != "+" {
		t.Fatalf("viewer polarity = %q, want +", viewers[note.ID])
	}
}

func TestReactionStatsByNoteIDs_BackfillsWhenTargetArrivesLater(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	authorSK := fnostr.Generate()
	reactorSK := fnostr.Generate()

	note := signTestNostrEvent(t, authorSK, 1000, nostrx.KindTextNote, "hello later", nil)
	reaction := signTestNostrEvent(t, reactorSK, 2000, nostrx.KindReaction, "+", [][]string{
		{"e", note.ID},
		{"p", note.PubKey},
	})

	if err := st.SaveEvent(ctx, reaction); err != nil {
		t.Fatal(err)
	}
	stats, _, err := st.ReactionStatsByNoteIDs(ctx, []string{note.ID}, reaction.PubKey)
	if err != nil {
		t.Fatal(err)
	}
	if stats[note.ID].Total != 0 {
		t.Fatalf("pre-note stats = %+v, want zero before target exists", stats[note.ID])
	}

	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	stats, viewers, err := st.ReactionStatsByNoteIDs(ctx, []string{note.ID}, reaction.PubKey)
	if err != nil {
		t.Fatal(err)
	}
	if stats[note.ID].Up != 1 || stats[note.ID].Total != 1 {
		t.Fatalf("post-note stats = %+v, want one recovered upvote", stats[note.ID])
	}
	if viewers[note.ID] != "+" {
		t.Fatalf("viewer polarity = %q, want +", viewers[note.ID])
	}
}

func TestReactionRollupsForNoteAuthor_ExcludesSelfReactions(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	authorSK := fnostr.Generate()
	otherSK := fnostr.Generate()

	note := signTestNostrEvent(t, authorSK, 1000, nostrx.KindTextNote, "self rollup", nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	selfReaction := signTestNostrEvent(t, authorSK, 2000, nostrx.KindReaction, "+", [][]string{
		{"e", note.ID},
		{"p", note.PubKey},
	})
	if err := st.SaveEvent(ctx, selfReaction); err != nil {
		t.Fatal(err)
	}
	otherReaction := signTestNostrEvent(t, otherSK, 2100, nostrx.KindReaction, "+", [][]string{
		{"e", note.ID},
		{"p", note.PubKey},
	})
	if err := st.SaveEvent(ctx, otherReaction); err != nil {
		t.Fatal(err)
	}

	rows, err := st.ReactionRollupsForNoteAuthor(ctx, note.PubKey, 5000, "~", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rollups len = %d, want 1", len(rows))
	}
	if rows[0].NoteID != note.ID || rows[0].ReactorCount != 1 {
		t.Fatalf("rollup = %+v, want one non-self reactor", rows[0])
	}
}

func TestReactionReactorsByNoteID_OrderAndTruncation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	authorSK := fnostr.Generate()
	rA := fnostr.Generate()
	rB := fnostr.Generate()
	rC := fnostr.Generate()

	note := signTestNostrEvent(t, authorSK, 1000, nostrx.KindTextNote, "reactors list", nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	pkA := signTestNostrEvent(t, rA, 1, nostrx.KindTextNote, "a", nil).PubKey
	_ = signTestNostrEvent(t, rB, 1, nostrx.KindTextNote, "b", nil).PubKey
	_ = signTestNostrEvent(t, rC, 1, nostrx.KindTextNote, "c", nil).PubKey

	// B down, A up, C down — list should be up first then downs by pubkey.
	downB := signTestNostrEvent(t, rB, 2000, nostrx.KindReaction, "-", [][]string{{"e", note.ID}, {"p", note.PubKey}})
	upA := signTestNostrEvent(t, rA, 2100, nostrx.KindReaction, "+", [][]string{{"e", note.ID}, {"p", note.PubKey}})
	downC := signTestNostrEvent(t, rC, 2200, nostrx.KindReaction, "-", [][]string{{"e", note.ID}, {"p", note.PubKey}})
	for _, ev := range []nostrx.Event{downB, upA, downC} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	full, truncated, err := st.ReactionReactorsByNoteID(ctx, note.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(full) != 3 {
		t.Fatalf("full: len=%d truncated=%v", len(full), truncated)
	}
	if full[0].Polarity != 1 || full[0].ReactorPubkey != pkA {
		t.Fatalf("first row = %+v, want A up", full[0])
	}
	// Downs sorted by pubkey ascending
	if full[1].Polarity != -1 || full[2].Polarity != -1 {
		t.Fatalf("want two downs: %+v", full)
	}
	if full[1].ReactorPubkey > full[2].ReactorPubkey {
		t.Fatalf("down rows not pubkey-sorted: %s before %s", full[1].ReactorPubkey, full[2].ReactorPubkey)
	}

	partial, trunc, err := st.ReactionReactorsByNoteID(ctx, note.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !trunc || len(partial) != 2 {
		t.Fatalf("partial: len=%d truncated=%v", len(partial), trunc)
	}
}
