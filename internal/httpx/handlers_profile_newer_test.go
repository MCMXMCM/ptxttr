package httpx

import (
	"context"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestFetchAuthorsPageProfileHeadRefreshDoesNotPanic(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("d", 64)
	ev := nostrx.Event{
		ID:        strings.Repeat("e", 64),
		PubKey:    author,
		CreatedAt: 1,
		Kind:      nostrx.KindTextNote,
		Content:   "thin-cache",
		Sig:       "sig",
	}
	if err := st.SaveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	_, _ = srv.fetchAuthorsPage(ctx, "", []string{author}, 0, "", 30, nil, "profile", author, nil, false)
}
