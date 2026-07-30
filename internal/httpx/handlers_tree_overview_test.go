package httpx

import (
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

func TestThreadTreeMainBodyTextStripsQuoteReferenceLink(t *testing.T) {
	quoteID := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	ev := overviewEvent("root", 1, [][]string{{"q", quoteID}})
	ev.Content = "please welcome my gf she is new and shy nostr:" + nostrx.EncodeNEvent(quoteID, "")

	got := threadTreeMainBodyText(ev, nil)
	if strings.Contains(got, "note:"+short(quoteID)) {
		t.Fatalf("quote link should be stripped from thread tree body: %q", got)
	}
	if got != "please welcome my gf she is new and shy" {
		t.Fatalf("body = %q", got)
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
