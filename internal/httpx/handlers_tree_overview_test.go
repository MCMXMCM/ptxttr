package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

func overviewEvent(id string, createdAt int64, tags [][]string) nostrx.Event {
	return nostrx.Event{
		ID:        id,
		PubKey:    strings.Repeat("1", 64),
		CreatedAt: createdAt,
		Kind:      nostrx.KindTextNote,
		Content:   id,
		Sig:       "sig",
		Tags:      tags,
	}
}

func TestBuildThreadTreeOverviewDeepChainThroughDepth4(t *testing.T) {
	root := overviewEvent("root", 1, nil)
	d1 := overviewEvent("d1", 2, [][]string{{"e", "root", "", "root"}})
	d2 := overviewEvent("d2", 3, [][]string{{"e", "root", "", "root"}, {"e", "d1", "", "reply"}})
	d3 := overviewEvent("d3", 4, [][]string{{"e", "root", "", "root"}, {"e", "d2", "", "reply"}})
	d4 := overviewEvent("d4", 5, [][]string{{"e", "root", "", "root"}, {"e", "d3", "", "reply"}})
	replies := []nostrx.Event{d1, d2, d3, d4}
	view := thread.BuildSelected(root, root, replies)
	ov := buildThreadTreeOverview(root, view)
	if ov == nil {
		t.Fatal("expected overview on OP page")
	}
	if len(ov.Branches) != 1 {
		t.Fatalf("branches = %d, want 1", len(ov.Branches))
	}
	br := ov.Branches[0]
	if br.Event.ID != "d1" {
		t.Fatalf("branch root = %q, want d1", br.Event.ID)
	}
	if len(br.Rows) != 3 {
		t.Fatalf("subrows = %d, want 3 (d2, d3, d4)", len(br.Rows))
	}
	if br.Rows[0].Depth != 2 || br.Rows[0].Event.ID != "d2" || br.Rows[0].IsContinue {
		t.Fatalf("row0 = %#v", br.Rows[0])
	}
	if br.Rows[1].Depth != 3 || br.Rows[1].Event.ID != "d3" || br.Rows[1].IsContinue {
		t.Fatalf("row1 = %#v", br.Rows[1])
	}
	last := br.Rows[2]
	if last.IsContinue || last.Event.ID != "d4" || last.Depth != 4 {
		t.Fatalf("row2 = %#v, want d4 depth 4", last)
	}
	if got := string(br.AsciiRail); got != "    `-- " {
		t.Fatalf("branch ascii wire = %q, want \"    `-- \"", got)
	}
	if got := string(br.Rows[0].AsciiRail); got != "    `-- " {
		t.Fatalf("row0 ascii wire = %q", got)
	}
	if got := string(br.Rows[1].AsciiRail); got != "        `-- " {
		t.Fatalf("row1 ascii wire = %q", got)
	}
	if got := string(last.AsciiRail); got != "            `-- " {
		t.Fatalf("row2 ascii wire = %q", got)
	}
	r0, r1 := br.Rows[0], br.Rows[1]
	if r0.SubtreeTrunkPrefix != 0 || !r0.SubtreeTrunk {
		t.Fatalf("row0 trunk prefix/trunk = %d/%v, want 0/true", r0.SubtreeTrunkPrefix, r0.SubtreeTrunk)
	}
	if r1.SubtreeTrunkPrefix != 1 || !r1.SubtreeTrunk {
		t.Fatalf("row1 trunk prefix/trunk = %d/%v, want 1/true", r1.SubtreeTrunkPrefix, r1.SubtreeTrunk)
	}
	if last.SubtreeTrunkPrefix != 2 || last.SubtreeTrunk {
		t.Fatalf("row2 trunk prefix/trunk = %d/%v, want 2/false", last.SubtreeTrunkPrefix, last.SubtreeTrunk)
	}
}

func TestBuildThreadTreeOverviewNilInFocusMode(t *testing.T) {
	root := overviewEvent("root", 1, nil)
	d1 := overviewEvent("d1", 2, [][]string{{"e", "root", "", "root"}})
	view := thread.BuildSelected(root, d1, []nostrx.Event{d1})
	if !view.FocusMode {
		t.Fatal("expected focus mode when selected != root")
	}
	if buildThreadTreeOverview(root, view) != nil {
		t.Fatal("expected nil overview in focus mode")
	}
}

func TestBuildThreadTreeOverviewTwoTopLevelBranches(t *testing.T) {
	root := overviewEvent("root", 1, nil)
	a := overviewEvent("a", 2, [][]string{{"e", "root", "", "root"}})
	b := overviewEvent("b", 3, [][]string{{"e", "root", "", "root"}})
	view := thread.BuildSelected(root, root, []nostrx.Event{a, b})
	ov := buildThreadTreeOverview(root, view)
	if ov == nil || len(ov.Branches) != 2 {
		t.Fatalf("branches = %v", ov)
	}
	if ov.Branches[0].Event.ID != "a" || ov.Branches[1].Event.ID != "b" {
		t.Fatalf("order %#v", ov.Branches)
	}
	if a, b := string(ov.Branches[0].AsciiRail), string(ov.Branches[1].AsciiRail); a != "|   |-- " || b != "    `-- " {
		t.Fatalf("ascii wire %#v, %#v", a, b)
	}
}

func TestThreadContinueThreadHref(t *testing.T) {
	h := threadContinueThreadHref("roothex", "branchhex")
	if !strings.Contains(h, "/thread/branchhex") {
		t.Fatalf("path: %q", h)
	}
	if !strings.Contains(h, "back=roothex") || !strings.Contains(h, "back_note=branchhex") {
		t.Fatalf("query: %q", h)
	}
}

func TestBuildTraversalPathUnchangedForOP(t *testing.T) {
	root := overviewEvent("root", 1, nil)
	view := thread.BuildSelected(root, root, nil)
	path := buildTraversalPath(root, root, view, nil)
	if len(path) != 1 || path[0].ID != "root" {
		t.Fatalf("path = %#v", path)
	}
}

func TestBuildTraversalPathLayoutsChain(t *testing.T) {
	root := overviewEvent("root", 1, nil)
	a := overviewEvent("a", 2, [][]string{{"e", "root", "", "root"}})
	b := overviewEvent("b", 3, [][]string{{"e", "root", "", "root"}, {"e", "a", "", "reply"}})
	path := []nostrx.Event{root, a, b}
	lay := buildTraversalPathLayouts(path)
	if len(lay) != 3 {
		t.Fatalf("len = %d", len(lay))
	}
	if got := string(lay[1].AsciiRail); got != "`-- " {
		t.Fatalf("depth1 wire = %q", got)
	}
	if lay[1].SubtreeTrunkPrefix != 0 || !lay[1].SubtreeTrunk {
		t.Fatalf("row1 layout = %#v", lay[1])
	}
	if got := string(lay[2].AsciiRail); got != "    `-- " {
		t.Fatalf("depth2 wire = %q", got)
	}
	if lay[2].SubtreeTrunkPrefix != 1 || lay[2].SubtreeTrunk {
		t.Fatalf("row2 layout = %#v", lay[2])
	}
}

func TestHandleThreadOPMainColumnDirectRepliesOnly(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("f", 64)
	d1 := strings.Repeat("e", 64)
	d2 := strings.Repeat("d", 64)
	events := []nostrx.Event{
		{ID: rootID, PubKey: strings.Repeat("1", 64), CreatedAt: 1000, Kind: nostrx.KindTextNote, Content: "root", Sig: "s"},
		{ID: d1, PubKey: strings.Repeat("2", 64), CreatedAt: 1001, Kind: nostrx.KindTextNote, Content: "d1", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}}},
		{ID: d2, PubKey: strings.Repeat("3", 64), CreatedAt: 1002, Kind: nostrx.KindTextNote, Content: "d2", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", d1, "", "reply"}}},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/thread/"+rootID, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `id="note-`+d1+`"`) {
		t.Fatal("expected direct reply d1 in main thread")
	}
	if strings.Contains(body, `id="note-`+d2+`"`) {
		t.Fatal("nested reply d2 must not appear in main thread column (shallow linear slice)")
	}
}

func TestHandleThreadFragmentSummaryIncludesTree(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("a", 64)
	d1 := strings.Repeat("b", 64)
	d2 := strings.Repeat("c", 64)
	d3 := strings.Repeat("d", 64)
	d4 := strings.Repeat("e", 64)
	events := []nostrx.Event{
		{ID: rootID, PubKey: strings.Repeat("1", 64), CreatedAt: 1000, Kind: nostrx.KindTextNote, Content: "root", Sig: "s"},
		{ID: d1, PubKey: strings.Repeat("2", 64), CreatedAt: 1001, Kind: nostrx.KindTextNote, Content: "d1", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}}},
		{ID: d2, PubKey: strings.Repeat("3", 64), CreatedAt: 1002, Kind: nostrx.KindTextNote, Content: "d2", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", d1, "", "reply"}}},
		{ID: d3, PubKey: strings.Repeat("4", 64), CreatedAt: 1003, Kind: nostrx.KindTextNote, Content: "d3", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", d2, "", "reply"}}},
		{ID: d4, PubKey: strings.Repeat("5", 64), CreatedAt: 1004, Kind: nostrx.KindTextNote, Content: "d4", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", d3, "", "reply"}}},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/thread/"+rootID+"?fragment=summary", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `data-thread-tree`) {
		t.Fatalf("expected thread tree marker in summary: %s", body)
	}
	if !strings.Contains(body, `data-thread-tree-note="note-`+rootID+`"`) {
		t.Fatalf("expected root in summary tree: %s", body)
	}
}

func TestHandleThreadTreeFragmentUsesRequestedFocusNote(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("a", 64)
	d1 := strings.Repeat("b", 64)
	d2 := strings.Repeat("c", 64)
	d3 := strings.Repeat("d", 64)
	d4 := strings.Repeat("e", 64)
	d5 := strings.Repeat("f", 64)
	events := []nostrx.Event{
		{ID: rootID, PubKey: strings.Repeat("1", 64), CreatedAt: 1000, Kind: nostrx.KindTextNote, Content: "root", Sig: "s"},
		{ID: d1, PubKey: strings.Repeat("2", 64), CreatedAt: 1001, Kind: nostrx.KindTextNote, Content: "d1", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}}},
		{ID: d2, PubKey: strings.Repeat("3", 64), CreatedAt: 1002, Kind: nostrx.KindTextNote, Content: "d2", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", d1, "", "reply"}}},
		{ID: d3, PubKey: strings.Repeat("4", 64), CreatedAt: 1003, Kind: nostrx.KindTextNote, Content: "d3", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", d2, "", "reply"}}},
		{ID: d4, PubKey: strings.Repeat("5", 64), CreatedAt: 1004, Kind: nostrx.KindTextNote, Content: "d4", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", d3, "", "reply"}}},
		{ID: d5, PubKey: strings.Repeat("6", 64), CreatedAt: 1005, Kind: nostrx.KindTextNote, Content: "d5", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", d4, "", "reply"}}},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/thread/"+rootID+"?fragment=tree&tree_note="+d2, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `data-thread-tree-root-id="`+rootID+`"`) {
		t.Fatalf("expected thread root %s as tree root, not subtree re-root: %s", rootID, body)
	}
	if !strings.Contains(body, `data-thread-tree-note="note-`+d2+`"`) ||
		!strings.Contains(body, `thread-tree-item is-selected`) {
		t.Fatalf("expected tree_note %s to mark a selected row in fragment: %s", d2, body)
	}
	if !strings.Contains(body, `data-thread-focus-id="`+d2+`"`) {
		t.Fatalf("expected highlighted tree note %s in fragment: %s", d2, body)
	}
	if !strings.Contains(body, `class="hn-tree-avatar"`) {
		t.Fatalf("expected hn-tree-avatar links in tree fragment: %s", body)
	}
	// Full-depth tree: d3–d5 appear as rows (no synthetic cont. thread).
	for _, id := range []string{d3, d4, d5} {
		if !strings.Contains(body, `data-thread-tree-note="note-`+id+`"`) {
			t.Fatalf("expected tree row for %s: %s", id, body)
		}
	}
}
