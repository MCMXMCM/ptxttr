package store

import (
	"context"
	"testing"
	"time"
)

func TestResolvedAuthorsDurablePrefixDelete(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	key1 := "abcccc|1|2"
	key2 := "abdddd|1|2"
	now := time.Now().Unix()
	if err := st.SetResolvedAuthorsDurable(ctx, key1, []string{"p1"}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.SetResolvedAuthorsDurable(ctx, key2, []string{"p2"}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteResolvedAuthorsDurablePrefix(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	_, _, ok1, _ := st.GetResolvedAuthorsDurable(ctx, key1)
	_, _, ok2, _ := st.GetResolvedAuthorsDurable(ctx, key2)
	if ok1 {
		t.Fatal("expected key1 deleted by prefix abc")
	}
	if !ok2 {
		t.Fatal("expected key2 to remain")
	}
}
