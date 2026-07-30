package nostrx

import (
	"strings"
	"testing"
)

func TestDeletionEventIDs(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	ev := Event{
		Kind: KindEventDeletion,
		Tags: [][]string{
			{"e", a},
			{"e", b},
			{"e", a},
			{"p", strings.Repeat("c", 64)},
		},
	}
	got := DeletionEventIDs(&ev)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("DeletionEventIDs = %v, want two unique ids", got)
	}
}

func TestValidateDeletionHTTPAPIShape(t *testing.T) {
	valid := Event{
		Kind:    KindEventDeletion,
		Content: "",
		Tags:    [][]string{{"e", strings.Repeat("f", 64)}},
	}
	if err := ValidateDeletionHTTPAPIShape(valid); err != nil {
		t.Fatalf("valid deletion: %v", err)
	}
	invalid := Event{Kind: KindEventDeletion, Tags: nil}
	if err := ValidateDeletionHTTPAPIShape(invalid); err == nil {
		t.Fatal("expected error for missing e tags")
	}
}
