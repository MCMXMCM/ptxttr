package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func guestTestEvent(idChar, pubkey string, kind int, createdAt int64, tags [][]string) nostrx.Event {
	return nostrx.Event{
		ID:        strings.Repeat(idChar, 64),
		PubKey:    pubkey,
		CreatedAt: createdAt,
		Kind:      kind,
		Tags:      tags,
		Content:   "{}",
		Sig:       strings.Repeat("1", 128),
	}
}

func TestActivityRankedDirectFollowsUsesActivityNotPubkeyOrder(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	owner := strings.Repeat("9", 64)
	lexicalFirst := strings.Repeat("1", 64)
	mostRecent := strings.Repeat("f", 64)
	mustSaveEvent(t, ctx, st, guestTestEvent("a", owner, nostrx.KindFollowList, 100, [][]string{
		{"p", lexicalFirst}, {"p", mostRecent},
	}))
	mustSaveEvent(t, ctx, st, guestTestEvent("b", lexicalFirst, nostrx.KindTextNote, 200, nil))
	mustSaveEvent(t, ctx, st, guestTestEvent("c", mostRecent, nostrx.KindTextNote, 300, nil))

	members, err := st.ActivityRankedDirectFollows(ctx, owner, 150, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].PubKey != mostRecent || members[1].PubKey != lexicalFirst {
		t.Fatalf("activity ranking = %#v", members)
	}
}

func TestPublishGuestSliceRejectsMissingRootAndParent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	author := strings.Repeat("2", 64)
	root := strings.Repeat("d", 64)
	parent := strings.Repeat("e", 64)
	reply := guestTestEvent("a", author, nostrx.KindTextNote, 300, [][]string{
		{"e", root, "", "root"}, {"e", parent, "", "reply"},
	})
	mustSaveEvent(t, ctx, st, reply)
	mustSaveEvent(t, ctx, st, guestTestEvent("b", author, nostrx.KindProfileMetadata, 301, nil))
	snap := &DefaultSeedGuestFeedSnapshot{Feed: []nostrx.Event{reply}}
	readiness, err := st.PublishGuestSlice(ctx, GuestSliceState{
		Generation: 1, SeedPubKey: author, Cohort: []string{author}, Trust: []string{author},
	}, []GuestSliceMember{{PubKey: author, Role: GuestSliceRoleCohort, MetadataCheckedAt: 300, MetadataFound: true}}, snap, time.Hour)
	if !errors.Is(err, ErrGuestSliceNotReady) {
		t.Fatalf("publish error = %v, want ErrGuestSliceNotReady", err)
	}
	if len(readiness.MissingRoots) == 0 || len(readiness.MissingParents) == 0 {
		t.Fatalf("readiness did not report missing chain: %#v", readiness)
	}
	if _, ok, getErr := st.GetGuestSliceState(ctx, GuestSliceDefaultKey); getErr != nil || ok {
		t.Fatalf("failed generation became visible: ok=%v err=%v", ok, getErr)
	}
}

func TestPublishGuestSliceRejectsMissingIntermediateAncestor(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	author := strings.Repeat("2", 64)
	root := guestTestEvent("d", author, nostrx.KindTextNote, 100, nil)
	missingGrandparent := strings.Repeat("e", 64)
	middle := guestTestEvent("b", author, nostrx.KindTextNote, 200, [][]string{
		{"e", root.ID, "", "root"}, {"e", missingGrandparent, "", "reply"},
	})
	reply := guestTestEvent("a", author, nostrx.KindTextNote, 300, [][]string{
		{"e", root.ID, "", "root"}, {"e", middle.ID, "", "reply"},
	})
	for _, event := range []nostrx.Event{root, middle, reply, guestTestEvent("c", author, nostrx.KindProfileMetadata, 301, nil)} {
		mustSaveEvent(t, ctx, st, event)
	}
	if err := st.MarkHydrationAttempt(ctx, "noteReplies", root.ID, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	readiness, err := st.PublishGuestSlice(ctx, GuestSliceState{
		Generation: 1, SeedPubKey: author, Cohort: []string{author}, Trust: []string{author},
	}, []GuestSliceMember{{PubKey: author, Role: GuestSliceRoleCohort, MetadataCheckedAt: 300, MetadataFound: true}},
		&DefaultSeedGuestFeedSnapshot{Feed: []nostrx.Event{reply}}, time.Hour)
	if !errors.Is(err, ErrGuestSliceNotReady) {
		t.Fatalf("publish error = %v, want ErrGuestSliceNotReady", err)
	}
	if !slices.Contains(readiness.MissingParents, missingGrandparent) {
		t.Fatalf("missing intermediate ancestor was not reported: %#v", readiness)
	}
}

func TestPublishGuestSliceAcceptsRecentlyCheckedParticipants(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	member := strings.Repeat("5", 64)
	stranger := strings.Repeat("6", 64)
	note := guestTestEvent("a", stranger, nostrx.KindTextNote, 100, nil)
	mustSaveEvent(t, ctx, st, note)
	if err := st.MarkHydrationAttempt(ctx, "noteReplies", note.ID, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	publish := func() (GuestSliceReadiness, error) {
		return st.PublishGuestSlice(ctx, GuestSliceState{
			Generation: 1, SeedPubKey: member, Cohort: []string{member}, Trust: []string{member},
		}, []GuestSliceMember{{PubKey: member, Role: GuestSliceRoleCohort, MetadataCheckedAt: 100, MetadataFound: true}},
			&DefaultSeedGuestFeedSnapshot{Feed: []nostrx.Event{note}}, time.Hour)
	}
	lookedUp := func(ago time.Duration) {
		t.Helper()
		if err := st.MarkHydrationAttempt(ctx, "profile", stranger, true, 2*time.Minute); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-ago).Unix()
		if _, err := st.db.ExecContext(ctx, `UPDATE hydration_state SET last_attempt_at = ?, next_retry_at = ?
			WHERE entity_type = 'profile' AND entity_id = ?`, at, at+120, stranger); err != nil {
			t.Fatal(err)
		}
	}

	if readiness, err := publish(); !errors.Is(err, ErrGuestSliceNotReady) || !slices.Contains(readiness.MissingParticipants, stranger) {
		t.Fatalf("never looked up: readiness=%#v err=%v, want the participant missing", readiness, err)
	}
	lookedUp(2 * time.Hour)
	if readiness, err := publish(); !errors.Is(err, ErrGuestSliceNotReady) || !slices.Contains(readiness.MissingParticipants, stranger) {
		t.Fatalf("looked up two hours ago: readiness=%#v err=%v, want the participant missing", readiness, err)
	}
	// The scheduler runs every few minutes, so a lookup that found nothing
	// must outlive its two-minute retry time.
	lookedUp(10 * time.Minute)
	if readiness, err := publish(); err != nil || !readiness.Ready {
		t.Fatalf("looked up ten minutes ago: readiness=%#v err=%v, want ready", readiness, err)
	}
}

func TestPublishedGuestDependenciesRemainPinnedUntilExpiry(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	author := strings.Repeat("3", 64)
	root := guestTestEvent("a", author, nostrx.KindTextNote, 100, nil)
	mustSaveEvent(t, ctx, st, root)
	mustSaveEvent(t, ctx, st, guestTestEvent("b", author, nostrx.KindProfileMetadata, 101, nil))
	if err := st.MarkHydrationAttempt(ctx, "noteReplies", root.ID, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	snap := &DefaultSeedGuestFeedSnapshot{Feed: []nostrx.Event{root}}
	readiness, err := st.PublishGuestSlice(ctx, GuestSliceState{
		Generation: 1, SeedPubKey: author, Cohort: []string{author}, Trust: []string{author},
	}, []GuestSliceMember{{PubKey: author, Role: GuestSliceRoleCohort, MetadataCheckedAt: 100, MetadataFound: true}}, snap, time.Hour)
	if err != nil || !readiness.Ready {
		t.Fatalf("publish readiness=%#v err=%v", readiness, err)
	}
	mustSaveEvent(t, ctx, st, guestTestEvent("c", strings.Repeat("4", 64), nostrx.KindTextNote, 200, nil))
	if _, err := st.PruneEvents(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if event, err := st.GetEvent(ctx, root.ID); err != nil || event == nil {
		t.Fatalf("pinned root was pruned: event=%v err=%v", event, err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE event_pins SET expires_at = 0`); err != nil {
		t.Fatal(err)
	}
	// Add another row so max=1 has an excess after the first prune retained the pin.
	mustSaveEvent(t, ctx, st, guestTestEvent("d", strings.Repeat("5", 64), nostrx.KindTextNote, 300, nil))
	if _, err := st.PruneEvents(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if event, err := st.GetEvent(ctx, root.ID); err != nil || event != nil {
		t.Fatalf("expired root pin stayed unprunable: event=%v err=%v", event, err)
	}
}

func TestNIP05VerificationProjectionRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	record := NIP05VerificationRecord{Identifier: "Alice@Example.com", PubKey: strings.Repeat("6", 64), Status: "verified", CheckedAt: 10, NextRetryAt: 20}
	if err := st.PutNIP05Verification(ctx, record); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.GetNIP05Verification(ctx, "alice@example.com", record.PubKey)
	if err != nil || !ok || got.Status != "verified" || got.CheckedAt != 10 {
		t.Fatalf("nip05 projection got=%#v ok=%v err=%v", got, ok, err)
	}
}

func TestGuestSliceProgressAndNegativeMetadataChecksSurviveFailedPublish(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	pubkey := strings.Repeat("7", 64)
	if err := st.MarkGuestMetadataChecked(ctx, []string{pubkey}, 50, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.SetGuestSliceProgress(ctx, GuestSliceDefaultKey, map[string]int64{"tier_active7d_cursor": 80}); err != nil {
		t.Fatal(err)
	}
	progress, err := st.GuestSliceProgress(ctx, GuestSliceDefaultKey)
	if err != nil || progress["tier_active7d_cursor"] != 80 {
		t.Fatalf("progress=%v err=%v", progress, err)
	}
	owner := strings.Repeat("8", 64)
	mustSaveEvent(t, ctx, st, guestTestEvent("a", owner, nostrx.KindFollowList, 100, [][]string{{"p", pubkey}}))
	members, err := st.DirectFollowMembers(ctx, owner, 10)
	if err != nil || len(members) != 1 || members[0].MetadataCheckedAt != 50 || members[0].MetadataFound {
		t.Fatalf("negative metadata projection members=%#v err=%v", members, err)
	}
	if _, ok, err := st.GetGuestSliceState(ctx, GuestSliceDefaultKey); err != nil || ok {
		t.Fatalf("build progress unexpectedly published a generation: ok=%v err=%v", ok, err)
	}
}

func TestGuestSliceSortSnapshotsPublishAtomically(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	author := strings.Repeat("9", 64)
	note := guestTestEvent("a", author, nostrx.KindTextNote, 100, nil)
	mustSaveEvent(t, ctx, st, note)
	mustSaveEvent(t, ctx, st, guestTestEvent("b", author, nostrx.KindProfileMetadata, 101, nil))
	if err := st.MarkHydrationAttempt(ctx, "noteReplies", note.ID, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	state := GuestSliceState{Generation: 4, SeedPubKey: author, Cohort: []string{author}, Trust: []string{author}}
	members := []GuestSliceMember{{PubKey: author, Role: GuestSliceRoleCohort, MetadataCheckedAt: 100, MetadataFound: true}}
	canonical := &DefaultSeedGuestFeedSnapshot{Feed: []nostrx.Event{note}}
	missing := guestTestEvent("c", author, nostrx.KindTextNote, 102, nil)
	_, err := st.PublishGuestSliceSnapshots(ctx, state, members, canonical, map[string]*FeedSnapshotRecord{
		"gc:trend24h:test": {Feed: []nostrx.Event{missing}},
	}, time.Hour)
	if !errors.Is(err, ErrGuestSliceNotReady) && !errors.Is(err, ErrFeedSnapshotMissingCanonicalEvent) {
		t.Fatalf("publish missing sort snapshot error = %v", err)
	}
	if _, ok, getErr := st.GetGuestSliceState(ctx, GuestSliceDefaultKey); getErr != nil || ok {
		t.Fatalf("partial sort generation became visible: ok=%v err=%v", ok, getErr)
	}

	key := "gc:recent:test"
	readiness, err := st.PublishGuestSliceSnapshots(ctx, state, members, canonical, map[string]*FeedSnapshotRecord{
		key: {Feed: []nostrx.Event{note}},
	}, time.Hour)
	if err != nil || !readiness.Ready {
		t.Fatalf("publish complete sort snapshot readiness=%#v err=%v", readiness, err)
	}
	if rec, ok, err := st.GetFeedSnapshot(ctx, key); err != nil || !ok || len(rec.Feed) != 1 || rec.Feed[0].ID != note.ID {
		t.Fatalf("published sort snapshot rec=%#v ok=%v err=%v", rec, ok, err)
	}
}

func TestGetGuestSliceGenerationReadsPublishedMarker(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	if generation, status, ok, err := st.GetGuestSliceGeneration(ctx, GuestSliceDefaultKey); err != nil || ok || generation != 0 || status != "" {
		t.Fatalf("empty store generation=%d status=%q ok=%v err=%v", generation, status, ok, err)
	}
	author := strings.Repeat("6", 64)
	note := guestTestEvent("a", author, nostrx.KindTextNote, 100, nil)
	mustSaveEvent(t, ctx, st, note)
	mustSaveEvent(t, ctx, st, guestTestEvent("b", author, nostrx.KindProfileMetadata, 101, nil))
	if err := st.MarkHydrationAttempt(ctx, "noteReplies", note.ID, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PublishGuestSlice(ctx, GuestSliceState{
		Generation: 11, SeedPubKey: author, Cohort: []string{author}, Trust: []string{author},
	}, []GuestSliceMember{{PubKey: author, Role: GuestSliceRoleCohort, MetadataCheckedAt: 100, MetadataFound: true}},
		&DefaultSeedGuestFeedSnapshot{Feed: []nostrx.Event{note}}, time.Hour); err != nil {
		t.Fatal(err)
	}
	generation, status, ok, err := st.GetGuestSliceGeneration(ctx, GuestSliceDefaultKey)
	if err != nil || !ok || generation != 11 || status != GuestSliceStatusReady {
		t.Fatalf("published generation=%d status=%q ok=%v err=%v", generation, status, ok, err)
	}
}

func TestPublishGuestSliceWritesTrustOnlyWhenItChanges(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	author := strings.Repeat("4", 64)
	other := strings.Repeat("3", 64)
	note := guestTestEvent("a", author, nostrx.KindTextNote, 100, nil)
	mustSaveEvent(t, ctx, st, note)
	mustSaveEvent(t, ctx, st, guestTestEvent("b", author, nostrx.KindProfileMetadata, 101, nil))
	if err := st.MarkHydrationAttempt(ctx, "noteReplies", note.ID, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	publish := func(generation int64, trust []string) {
		t.Helper()
		if _, err := st.PublishGuestSlice(ctx, GuestSliceState{
			Generation: generation, SeedPubKey: author, Cohort: []string{author}, Trust: trust,
		}, []GuestSliceMember{{PubKey: author, Role: GuestSliceRoleCohort, MetadataCheckedAt: 100, MetadataFound: true}},
			&DefaultSeedGuestFeedSnapshot{Feed: []nostrx.Event{note}}, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	trustUpdatedAt := func() int64 {
		t.Helper()
		var updatedAt int64
		if err := st.db.QueryRowContext(ctx, `SELECT updated_at FROM guest_slice_trust WHERE slice_key = ?`, GuestSliceDefaultKey).Scan(&updatedAt); err != nil {
			t.Fatal(err)
		}
		return updatedAt
	}
	expectTrust := func(generation int64, want []string) {
		t.Helper()
		state, ok, err := st.GetGuestSliceState(ctx, GuestSliceDefaultKey)
		if err != nil || !ok || state.Generation != generation || !slices.Equal(state.Trust, want) ||
			state.TrustFingerprint != GuestSliceTrustFingerprint(want) {
			t.Fatalf("state = %#v ok=%v err=%v, want generation %d trust %v", state, ok, err, generation, want)
		}
		var stateTrust string
		if err := st.db.QueryRowContext(ctx, `SELECT trust_json FROM guest_slice_state WHERE slice_key = ?`, GuestSliceDefaultKey).Scan(&stateTrust); err != nil || stateTrust != "[]" {
			t.Fatalf("guest_slice_state.trust_json = %q err=%v, want []", stateTrust, err)
		}
	}

	publish(1, []string{author})
	expectTrust(1, []string{author})
	if _, err := st.db.ExecContext(ctx, `UPDATE guest_slice_trust SET updated_at = 1`); err != nil {
		t.Fatal(err)
	}
	publish(2, []string{author})
	expectTrust(2, []string{author})
	if got := trustUpdatedAt(); got != 1 {
		t.Fatalf("unchanged trust was rewritten (updated_at=%d)", got)
	}
	publish(3, []string{author, other})
	expectTrust(3, []string{author, other})
	if got := trustUpdatedAt(); got == 1 {
		t.Fatal("changed trust was not written")
	}
}

func TestGetGuestSliceStateReadsTrustFromLegacyRow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	legacy := strings.Repeat("2", 64)
	if _, err := st.db.ExecContext(ctx, `INSERT INTO guest_slice_state(slice_key, generation, status, trust_json)
		VALUES (?, 5, ?, ?)`, GuestSliceDefaultKey, GuestSliceStatusReady, `["`+legacy+`"]`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO guest_slice_trust(slice_key, fingerprint, trust_json)
		VALUES (?, 'older', '["stale"]')`, GuestSliceDefaultKey); err != nil {
		t.Fatal(err)
	}
	state, ok, err := st.GetGuestSliceState(ctx, GuestSliceDefaultKey)
	if err != nil || !ok || !slices.Equal(state.Trust, []string{legacy}) || state.TrustFingerprint != "" {
		t.Fatalf("legacy state = %#v ok=%v err=%v, want the row's own trust list", state, ok, err)
	}
}
