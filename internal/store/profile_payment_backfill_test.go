package store

import (
	"context"
	"encoding/json"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestProfilePaymentBackfillFromCachedEvent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	profileContent, err := json.Marshal(map[string]string{
		"name":  "laser",
		"lud16": "laser@primal.net",
		"nip05": "laser@primal.net",
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := event("laser-profile", "laser", 10, nostrx.KindProfileMetadata, nil)
	profile.Content = string(profileContent)
	if err := st.SaveEvent(ctx, profile); err != nil {
		t.Fatal(err)
	}

	// Simulate profiles cached before lud16/lud06/website columns existed.
	if _, err := st.db.ExecContext(ctx, `UPDATE profiles_cache SET lud16 = '', lud06 = '', website = '' WHERE pubkey = ?`, "laser"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM app_meta WHERE key = ?`, profilePaymentBackfillKey); err != nil {
		t.Fatal(err)
	}
	st.clearProfileSummariesBestEffort()

	if err := st.maybeBackfillProfileCachePaymentFields(ctx); err != nil {
		t.Fatal(err)
	}

	var lud16 string
	if err := st.db.QueryRowContext(ctx, `SELECT lud16 FROM profiles_cache WHERE pubkey = ?`, "laser").Scan(&lud16); err != nil {
		t.Fatal(err)
	}
	if lud16 != "laser@primal.net" {
		t.Fatalf("sqlite lud16 = %q, want laser@primal.net", lud16)
	}

	summaries, err := st.ProfileSummariesByPubkeys(ctx, []string{"laser"})
	if err != nil {
		t.Fatal(err)
	}
	if summaries["laser"].Lud16 != "laser@primal.net" {
		t.Fatalf("lud16 = %q, want laser@primal.net", summaries["laser"].Lud16)
	}

	if err := st.maybeBackfillProfileCachePaymentFields(ctx); err != nil {
		t.Fatal(err)
	}
}
