package store

import (
	"context"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestApplyEventDeletionTxRemovesOwnedNote(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	author := strings.Repeat("a", 64)
	noteID := strings.Repeat("b", 64)
	note := event(noteID, author, 10, nostrx.KindTextNote, nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	deletion := event(strings.Repeat("c", 64), author, 11, nostrx.KindEventDeletion, [][]string{{"e", noteID}})
	deletion.Content = ""
	if err := st.SaveEvent(ctx, deletion); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetEvent(ctx, noteID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("note should be deleted, still have id=%q", got.ID)
	}
}

func TestApplyEventDeletionTxIgnoresOtherAuthors(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	owner := strings.Repeat("a", 64)
	attacker := strings.Repeat("e", 64)
	noteID := strings.Repeat("b", 64)
	note := event(noteID, owner, 10, nostrx.KindTextNote, nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	deletion := event(strings.Repeat("c", 64), attacker, 11, nostrx.KindEventDeletion, [][]string{{"e", noteID}})
	deletion.Content = ""
	if err := st.SaveEvent(ctx, deletion); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetEvent(ctx, noteID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != noteID {
		t.Fatal("note should remain when deletion author does not own it")
	}
}
