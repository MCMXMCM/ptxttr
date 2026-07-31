package httpx

import (
	"context"
	"strings"
	"testing"
)

func TestViewerCrawlerAuthorsHonorsGraphDepth(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{})
	viewer := strings.Repeat("1", 64)
	hop1 := strings.Repeat("2", 64)
	hop2 := strings.Repeat("3", 64)
	hop3 := strings.Repeat("4", 64)
	hop4 := strings.Repeat("5", 64)

	saveTestFollowList(t, st, viewer, []string{hop1}, 1700000001)
	saveTestFollowList(t, st, hop1, []string{hop2}, 1700000002)
	saveTestFollowList(t, st, hop2, []string{hop3}, 1700000003)
	saveTestFollowList(t, st, hop3, []string{hop4}, 1700000004)

	graphOwners, err := srv.viewerCrawlerAuthors(context.Background(), viewer, 2, false)
	if err != nil {
		t.Fatalf("graph owners: %v", err)
	}
	assertStringSet(t, graphOwners, []string{hop1, hop2})

	cohort, err := srv.viewerCrawlerAuthors(context.Background(), viewer, 3, true)
	if err != nil {
		t.Fatalf("three-hop cohort: %v", err)
	}
	assertStringSet(t, cohort, []string{viewer, hop1, hop2, hop3})
	for _, author := range cohort {
		if author == hop4 {
			t.Fatal("fourth-hop author leaked into viewer crawl cohort")
		}
	}
}

func assertStringSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	seen := make(map[string]bool, len(got))
	for _, value := range got {
		seen[value] = true
	}
	for _, value := range want {
		if !seen[value] {
			t.Fatalf("got %v, missing %s", got, value)
		}
	}
}
