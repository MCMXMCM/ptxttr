package httpx

import "testing"

func TestSearchSingleFlightClearsEntryAfterPanic(t *testing.T) {
	group := newSearchSingleFlight()

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic from first call")
			}
		}()
		group.do("key", func() SearchPageData {
			panic("boom")
		})
	}()

	if len(group.calls) != 0 {
		t.Fatalf("in-flight calls = %d, want 0", len(group.calls))
	}

	got := group.do("key", func() SearchPageData {
		return SearchPageData{Query: "ok"}
	})
	if got.Query != "ok" {
		t.Fatalf("query = %q, want ok", got.Query)
	}
}
