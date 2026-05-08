package httpx

import (
	"testing"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

func flattenThreadHNRows(nodes []thread.Node) []thread.Node {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]thread.Node, 0, 64)
	var walk func([]thread.Node)
	walk = func(ns []thread.Node) {
		for _, n := range ns {
			out = append(out, n)
			if len(n.Children) > 0 {
				walk(n.Children)
			}
		}
	}
	walk(nodes)
	return out
}

func TestFlattenThreadHNRowsPreorder(t *testing.T) {
	root := nostrx.Event{ID: "root"}
	a := nostrx.Event{ID: "a"}
	b := nostrx.Event{ID: "b"}
	nodes := []thread.Node{
		{Event: a, Depth: 1, ParentID: root.ID, Children: []thread.Node{
			{Event: b, Depth: 2, ParentID: a.ID},
		}},
	}
	rows := flattenThreadHNRows(nodes)
	if len(rows) != 2 {
		t.Fatalf("len=%d rows=%+v", len(rows), rows)
	}
	if rows[0].Event.ID != "a" || rows[1].Event.ID != "b" {
		t.Fatalf("order: %#v", rows)
	}
}

func TestHnTreeIndentPx(t *testing.T) {
	if hnTreeIndentPx(0) != 0 || hnTreeIndentPx(1) != 0 || hnTreeIndentPx(2) != 40 || hnTreeIndentPx(3) != 80 {
		t.Fatal(hnTreeIndentPx(0), hnTreeIndentPx(1), hnTreeIndentPx(2), hnTreeIndentPx(3))
	}
	// depth 6: five full steps (d=5)
	if got := hnTreeIndentPx(6); got != 200 {
		t.Fatalf("depth 6 indent = %d, want 200", got)
	}
	// depth 7: first tight step (d=6)
	if got := hnTreeIndentPx(7); got != 214 {
		t.Fatalf("depth 7 indent = %d, want 214", got)
	}
}

func TestHnPathIndentPx(t *testing.T) {
	if hnPathIndentPx(0) != 0 || hnPathIndentPx(1) != 40 || hnPathIndentPx(2) != 80 {
		t.Fatal(hnPathIndentPx(0), hnPathIndentPx(1), hnPathIndentPx(2))
	}
}

