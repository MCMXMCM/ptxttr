package httpx

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	fnostr "fiatjaf.com/nostr"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

// guestSliceLookupTestServer points every relay tier at one test relay that
// serves byID and answers other queries with nothing.
func guestSliceLookupTestServer(t *testing.T, byID map[string]fnostr.Event) (*Server, *store.Store) {
	t.Helper()
	srv, st := newTestServer(t, testServerOptions{relayTimeout: 2 * time.Second})
	relay := newTestRelayREQEventsByIDs(context.Background(), byID)
	t.Cleanup(relay.Close)
	srv.cfg.DefaultRelays = []string{wsURL(relay.URL)}
	srv.cfg.MetadataRelays = []string{wsURL(relay.URL)}
	srv.cfg.IndexerRelays = nil
	return srv, st
}

func publishTestGuestCandidate(st *store.Store, member string, feed ...nostrx.Event) func() (store.GuestSliceReadiness, error) {
	return func() (store.GuestSliceReadiness, error) {
		return st.PublishGuestSlice(context.Background(), store.GuestSliceState{
			Generation: 1, SeedPubKey: member, Cohort: []string{member}, Trust: []string{member},
		}, []store.GuestSliceMember{{PubKey: member, Role: store.GuestSliceRoleCohort, MetadataCheckedAt: 100, MetadataFound: true}},
			&store.DefaultSeedGuestFeedSnapshot{Feed: feed}, time.Hour)
	}
}

// saveTestGuestNoteByStranger stores a root note by a new author with no
// profile, whose reply page is already hydrated.
func saveTestGuestNoteByStranger(t *testing.T, st *store.Store) (nostrx.Event, string) {
	t.Helper()
	ctx := context.Background()
	// Relay queries only accept real public keys.
	stranger := fnostr.Generate().Public().Hex()
	note := nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    stranger,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 100,
		Sig:       strings.Repeat("1", 128),
	}
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkHydrationAttempt(ctx, "noteReplies", note.ID, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	return note, stranger
}

func TestGuestSliceLookupsUnblockParticipantsWithoutProfiles(t *testing.T) {
	ctx := context.Background()
	srv, st := guestSliceLookupTestServer(t, nil)
	member := strings.Repeat("5", 64)
	note, stranger := saveTestGuestNoteByStranger(t, st)
	publish := publishTestGuestCandidate(st, member, note)

	if readiness, err := publish(); !errors.Is(err, store.ErrGuestSliceNotReady) || !slices.Contains(readiness.MissingParticipants, stranger) {
		t.Fatalf("before lookups: readiness=%#v err=%v, want the participant missing", readiness, err)
	}
	if readiness, err := srv.publishGuestSliceWithLookups(ctx, member, publish); err != nil || !readiness.Ready {
		t.Fatalf("after a lookup that found no profile: readiness=%#v err=%v, want ready", readiness, err)
	}
}

func TestGuestSliceLookupsWaitForTheNextRunWhenTimeIsShort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), guestSliceLookupMinBudget/2)
	defer cancel()
	srv, st := guestSliceLookupTestServer(t, nil)
	member := strings.Repeat("5", 64)
	note, stranger := saveTestGuestNoteByStranger(t, st)

	readiness, err := srv.publishGuestSliceWithLookups(ctx, member, publishTestGuestCandidate(st, member, note))
	if !errors.Is(err, store.ErrGuestSliceNotReady) || !slices.Contains(readiness.MissingParticipants, stranger) {
		t.Fatalf("readiness=%#v err=%v, want the candidate left for the next run", readiness, err)
	}
}

func TestGuestSliceLookupsFetchMissingRootInTheSameRun(t *testing.T) {
	ctx := context.Background()
	rootSecret := fnostr.Generate()
	rootExternal := fnostr.Event{
		CreatedAt: fnostr.Timestamp(1700000000),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "root only on the relay",
	}
	if err := rootExternal.Sign(rootSecret); err != nil {
		t.Fatal(err)
	}
	root := fnostrToNostrxEvent(rootExternal)
	srv, st := guestSliceLookupTestServer(t, map[string]fnostr.Event{root.ID: rootExternal})
	member := strings.Repeat("5", 64)
	reply := nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    fnostr.Generate().Public().Hex(),
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000001,
		Sig:       strings.Repeat("1", 128),
		Tags:      [][]string{{"e", root.ID, "", "root"}},
	}
	if err := st.SaveEvent(ctx, reply); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkHydrationAttempt(ctx, "noteReplies", root.ID, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	publish := publishTestGuestCandidate(st, member, reply)

	if readiness, err := publish(); !errors.Is(err, store.ErrGuestSliceNotReady) || !slices.Contains(readiness.MissingRoots, root.ID) {
		t.Fatalf("before lookups: readiness=%#v err=%v, want the root missing", readiness, err)
	}
	readiness, err := srv.publishGuestSliceWithLookups(ctx, member, publish)
	if err != nil || !readiness.Ready {
		t.Fatalf("after lookups: readiness=%#v err=%v, want ready", readiness, err)
	}
	if event, _ := st.GetEvent(ctx, root.ID); event == nil {
		t.Fatal("the missing root was not stored")
	}
}
