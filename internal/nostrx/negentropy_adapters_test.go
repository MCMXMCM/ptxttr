package nostrx

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	fnostr "fiatjaf.com/nostr"
)

func TestNegentropyFilterFromQuery(t *testing.T) {
	pk, err := fnostr.PubKeyFromHex("a" + strings.Repeat("b", 63))
	if err != nil {
		t.Fatal(err)
	}
	idHex := "c" + strings.Repeat("1", 63)
	_, err = fnostr.IDFromHex(idHex)
	if err != nil {
		t.Fatal(err)
	}

	_, err = NegentropyFilterFromQuery(Query{Tags: map[string][]string{"e": {"x"}}})
	if !errors.Is(err, ErrNegentropyUnsupportedFilter) {
		t.Fatalf("tags: err = %v", err)
	}

	_, err = NegentropyFilterFromQuery(Query{})
	if !errors.Is(err, ErrNegentropyUnsupportedFilter) {
		t.Fatalf("empty: err = %v", err)
	}

	f, err := NegentropyFilterFromQuery(Query{
		Authors: []string{pk.Hex()},
		Kinds:   []int{1},
		Since:   10,
		Until:   100,
		Limit:   5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Authors) != 1 || f.Authors[0] != pk {
		t.Fatalf("authors %#v", f.Authors)
	}
	if len(f.Kinds) != 1 || f.Kinds[0] != 1 {
		t.Fatalf("kinds %#v", f.Kinds)
	}
	if f.Since != 10 || f.Until != 100 || f.Limit != 5 {
		t.Fatalf("since/until/limit = %v %v %v", f.Since, f.Until, f.Limit)
	}

	f2, err := NegentropyFilterFromQuery(Query{IDs: []string{idHex}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f2.IDs) != 1 || f2.IDs[0].Hex() != idHex {
		t.Fatalf("ids %#v", f2.IDs)
	}
}

func TestCheckNegentropyLocalBudget(t *testing.T) {
	ctx := context.Background()
	pk, _ := fnostr.PubKeyFromHex("a" + strings.Repeat("b", 63))
	f := fnostr.Filter{Authors: []fnostr.PubKey{pk}, Kinds: []fnostr.Kind{1}}

	t.Run("unsupported", func(t *testing.T) {
		m := &mockNegentropyCache{n: 1}
		_, err := CheckNegentropyLocalBudget(ctx, m, fnostr.Filter{Search: "x"})
		if !errors.Is(err, ErrNegentropyUnsupportedFilter) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("too_large", func(t *testing.T) {
		m := &mockNegentropyCache{n: MaxNegentropyLocalRows + 1}
		_, err := CheckNegentropyLocalBudget(ctx, m, f)
		if !errors.Is(err, ErrNegentropyLocalSetTooLarge) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("ok", func(t *testing.T) {
		m := &mockNegentropyCache{n: 3}
		n, err := CheckNegentropyLocalBudget(ctx, m, f)
		if err != nil || n != 3 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})
}

func TestNegentropyPublisherPersist(t *testing.T) {
	ctx := context.Background()
	valid := signedTestEvent(t, KindTextNote, "hello", nil)
	fnValid, err := toExternalEvent(valid)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid_saves", func(t *testing.T) {
		m := &mockNegentropyCache{}
		e, ok, err := NegentropyPublisherPersist(ctx, m, "wss://relay.example", fnValid)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if e.RelayURL != "wss://relay.example" || e.Content != "hello" {
			t.Fatalf("event %#v", e)
		}
		if m.saveCalls != 1 {
			t.Fatalf("saveCalls = %d", m.saveCalls)
		}
	})

	t.Run("invalid_skipped", func(t *testing.T) {
		m := &mockNegentropyCache{}
		bad := fnValid
		bad.Content = "tampered"
		_, ok, err := NegentropyPublisherPersist(ctx, m, "wss://relay.example", bad)
		if err != nil || ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if m.saveCalls != 0 {
			t.Fatalf("saveCalls = %d", m.saveCalls)
		}
	})
}

type mockNegentropyCache struct {
	n         int64
	countErr  error
	saveCalls int
}

func (m *mockNegentropyCache) SaveEvent(_ context.Context, _ Event) error {
	m.saveCalls++
	return nil
}

func (m *mockNegentropyCache) NegentropyLocalMatchCount(_ context.Context, _ fnostr.Filter) (int64, error) {
	return m.n, m.countErr
}

func (m *mockNegentropyCache) NegentropyQueryEvents(_ context.Context, _ fnostr.Filter) iter.Seq[fnostr.Event] {
	return func(yield func(fnostr.Event) bool) {}
}
