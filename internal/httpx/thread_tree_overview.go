package httpx

import (
	"strings"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

// threadTreeAsciiRail is the left gutter wire for tree drawing in tests and
// optional HTML rendering (ASCII elbow / trunk characters).
type threadTreeAsciiRail string

// ThreadTreeOverview describes direct replies to the root and a single
// linear continuation under each when the thread OP page is not in focus mode.
type ThreadTreeOverview struct {
	Branches []ThreadTreeBranch
}

// ThreadTreeBranch is one top-level reply node under the root plus nested
// rows forming at most one single-child chain beneath it.
type ThreadTreeBranch struct {
	Event     nostrx.Event
	Rows      []ThreadTreeOverviewRow
	AsciiRail threadTreeAsciiRail
}

// ThreadTreeOverviewRow is one note in a branch continuation (excluding the
// branch head, which lives on ThreadTreeBranch.Event).
type ThreadTreeOverviewRow struct {
	Event              nostrx.Event
	Depth              int
	IsContinue         bool
	AsciiRail          threadTreeAsciiRail
	SubtreeTrunkPrefix int
	SubtreeTrunk       bool
}

type threadTraversalLayout struct {
	AsciiRail          threadTreeAsciiRail
	SubtreeTrunkPrefix int
	SubtreeTrunk       bool
}

func buildThreadTreeOverview(root nostrx.Event, view thread.View) *ThreadTreeOverview {
	if view.FocusMode || root.ID == "" {
		return nil
	}
	if view.Root != nil && view.Root.ID != root.ID {
		return nil
	}
	top := view.Nodes
	if len(top) == 0 {
		return &ThreadTreeOverview{Branches: nil}
	}
	branches := make([]ThreadTreeBranch, 0, len(top))
	for i := range top {
		node := top[i]
		br := ThreadTreeBranch{
			Event:     node.Event,
			AsciiRail: overviewBranchAsciiRail(i, len(top)),
			Rows:      nil,
		}
		chain := overviewSingleChildChain(node)
		for j, rn := range chain {
			rowLast := j == len(chain)-1
			br.Rows = append(br.Rows, ThreadTreeOverviewRow{
				Event:              rn.Event,
				Depth:              rn.Depth,
				IsContinue:         false,
				AsciiRail:          overviewRowAsciiRail(rn.Depth),
				SubtreeTrunkPrefix: j,
				SubtreeTrunk:       !rowLast,
			})
		}
		branches = append(branches, br)
	}
	return &ThreadTreeOverview{Branches: branches}
}

func overviewSingleChildChain(head thread.Node) []thread.Node {
	var out []thread.Node
	cur := head
	for len(cur.Children) == 1 {
		cur = cur.Children[0]
		out = append(out, cur)
	}
	return out
}

func overviewBranchAsciiRail(index, nSiblings int) threadTreeAsciiRail {
	if nSiblings == 1 {
		return threadTreeAsciiRail("    `-- ")
	}
	if index < nSiblings-1 {
		return threadTreeAsciiRail("|   |-- ")
	}
	return threadTreeAsciiRail("    `-- ")
}

func overviewRowAsciiRail(depth int) threadTreeAsciiRail {
	n := 4 * overviewDepthSpaces(depth)
	return threadTreeAsciiRail(strings.Repeat(" ", n) + "`-- ")
}

// overviewDepthSpaces maps thread node depth to leading space columns for
// the `-- elbow pattern used in tests (depth 1 root-child and depth 2 share
// the same gutter width; deeper levels add four spaces per level).
func overviewDepthSpaces(depth int) int {
	if depth <= 1 {
		return 1
	}
	return depth - 1
}

func buildTraversalPathLayouts(path []nostrx.Event) []threadTraversalLayout {
	if len(path) == 0 {
		return nil
	}
	lay := make([]threadTraversalLayout, len(path))
	for i := range path {
		if i == 0 {
			continue
		}
		last := i == len(path)-1
		var rail threadTreeAsciiRail
		if last {
			rail = threadTreeAsciiRail("    `-- ")
		} else {
			rail = threadTreeAsciiRail("`-- ")
		}
		lay[i] = threadTraversalLayout{
			AsciiRail:          rail,
			SubtreeTrunkPrefix: i - 1,
			SubtreeTrunk:       !last,
		}
	}
	return lay
}

func threadContinueThreadHref(rootID, branchID string) string {
	return "/thread/" + branchID + "?back=" + rootID + "&back_note=" + branchID
}
