package store

import (
	"context"
	"path/filepath"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func openTestStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	return openTestStoreAtPath(t, ctx, filepath.Join(t.TempDir(), "test.sqlite"))
}

func openTestStoreAtPath(t *testing.T, ctx context.Context, path string) *Store {
	t.Helper()
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustSaveEvent(t *testing.T, ctx context.Context, st *Store, ev nostrx.Event) {
	t.Helper()
	if err := st.SaveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
}
