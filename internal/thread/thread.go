package thread

import (
	"sort"
	"strings"

	"ptxt-nstr/internal/nostrx"
)

// NormalizeHexEventID trims id and, when it is exactly 64 ASCII hex digits,
// lowercases it so lookups match SQLite event ids (always stored as lower
// hex). Some relays or clients emit uppercase hex inside e-tags; without
// normalization ParentID/resolveThreadRootID would point at ids that never
// hit the local cache.
func NormalizeHexEventID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) != 64 {
		return id
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return id
	}
	return strings.ToLower(id)
}

func nip10Marker(tag []string) string {
	if len(tag) < 4 {
		return ""
	}
	return strings.TrimSpace(tag[3])
}

type Node struct {
	Event      nostrx.Event
	Depth      int
	ParentID   string
	Children   []Node
	ReplyCount int
}

type View struct {
	Root       *nostrx.Event
	Selected   *nostrx.Event
	Nodes      []Node
	ReplyCount int
	MaxDepth   int

	// Focus mode metadata. FocusMode is true when the selected note is not
	// the root and could be located in the built reply tree. In focus mode,
	// the page shows the selected note as the new top-level note, with its
	// parent rendered above it and the selected note's own descendants
	// rendered below it. Everything else (the root, ancestors above the
	// parent, siblings of nodes on the path, etc.) is hidden behind a
	// "view other replies" toggle.
	FocusMode       bool
	ParentNode      *Node
	SelectedNode    *Node
	HiddenNodeCount int
	// HiddenAncestors is the chain from the OP through every ancestor strictly
	// above the rendered parent, oldest first (OP first, then replies toward
	// the parent). The focus header shows ParentNode only, so expanding
	// "show messages above" must include Thread.Root when the parent is not
	// the OP.
	HiddenAncestors []Node
}

func Build(root nostrx.Event, replies []nostrx.Event) View {
	return BuildSelected(root, root, replies)
}

func BuildSelected(root nostrx.Event, selected nostrx.Event, replies []nostrx.Event) View {
	children := make(map[string][]nostrx.Event)
	for _, reply := range replies {
		if reply.ID == root.ID {
			continue
		}
		parentID := parentFor(root.ID, reply)
		if parentID == "" {
			parentID = root.ID
		}
		children[parentID] = append(children[parentID], reply)
	}
	for parentID, items := range children {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].CreatedAt == items[j].CreatedAt {
				return items[i].ID < items[j].ID
			}
			return items[i].CreatedAt < items[j].CreatedAt
		})
		children[parentID] = items
	}
	view := View{Root: &root, Selected: &selected, ReplyCount: len(replies)}
	view.Nodes = walk(root.ID, 0, children, map[string]bool{})
	view.MaxDepth = maxNodeDepth(view.Nodes)
	if selected.ID == "" || selected.ID == root.ID {
		return view
	}
	path := findFocusPath(view.Nodes, selected.ID)
	if len(path) == 0 {
		return view
	}
	selectedNode := path[len(path)-1]
	var parentNode *Node
	if len(path) >= 2 {
		parentNode = path[len(path)-2]
	}
	view.FocusMode = true
	rebased := rebasedSubtree(*selectedNode, 0)
	view.SelectedNode = &rebased
	view.ParentNode = parentNode
	// Hidden stack: OP (when not the visible parent) plus path nodes strictly
	// between OP and ParentNode, as compact cards when the user expands.
	if parentNode != nil {
		if root.ID != "" && root.ID != parentNode.Event.ID {
			view.HiddenAncestors = append(view.HiddenAncestors, leafNode(Node{Event: root, Depth: 1, ParentID: ""}))
		}
		for _, ancestor := range path[:len(path)-2] {
			view.HiddenAncestors = append(view.HiddenAncestors, leafNode(*ancestor))
		}
	} else if len(path) > 1 {
		for _, ancestor := range path[:len(path)-1] {
			view.HiddenAncestors = append(view.HiddenAncestors, leafNode(*ancestor))
		}
	}
	view.HiddenNodeCount = countHiddenForFocus(view.Nodes, selectedNode, parentNode)
	return view
}

// leafNode returns a copy of node without its children. Used for rendering
// ancestor cards in focus mode where we want a single compact reply card per
// ancestor without its full subtree.
func leafNode(node Node) Node {
	return Node{
		Event:      node.Event,
		Depth:      1,
		ParentID:   node.ParentID,
		ReplyCount: node.ReplyCount,
	}
}

// findFocusPath returns the chain of nodes from the topmost reply down to the
// selected node, inclusive. The returned slice is empty when the selected ID
// is not present in the tree. The root event itself is not part of this slice
// since the tree is rooted at the root event's children.
func findFocusPath(nodes []Node, selectedID string) []*Node {
	for i := range nodes {
		node := &nodes[i]
		if node.Event.ID == selectedID {
			return []*Node{node}
		}
		if sub := findFocusPath(node.Children, selectedID); len(sub) > 0 {
			return append([]*Node{node}, sub...)
		}
	}
	return nil
}

// rebasedSubtree returns a copy of node with Depth recomputed so that the
// returned root sits at the provided baseDepth. This makes it safe to render
// the selected note's descendants with the regular comment template at the
// indentation depths the focused view expects (1, 2, 3, ...).
func rebasedSubtree(node Node, baseDepth int) Node {
	out := Node{
		Event:      node.Event,
		Depth:      baseDepth,
		ParentID:   node.ParentID,
		ReplyCount: node.ReplyCount,
	}
	for _, child := range node.Children {
		out.Children = append(out.Children, rebasedSubtree(child, baseDepth+1))
	}
	return out
}

func ExplicitRootID(event nostrx.Event) string {
	if event.Kind != nostrx.KindTextNote {
		return ""
	}
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "e" && strings.EqualFold(nip10Marker(tag), "root") {
			return NormalizeHexEventID(tag[1])
		}
	}
	return ""
}

func RootID(event nostrx.Event) string {
	if event.Kind != nostrx.KindTextNote {
		return ""
	}
	if rootID := ExplicitRootID(event); rootID != "" {
		return rootID
	}
	var firstE string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "e" {
			continue
		}
		if firstE == "" {
			firstE = NormalizeHexEventID(tag[1])
		}
	}
	return firstE
}

func ParentID(rootID string, event nostrx.Event) string {
	return parentFor(rootID, event)
}

const MaxDepth = 20

func walk(parentID string, depth int, children map[string][]nostrx.Event, seen map[string]bool) []Node {
	if depth >= MaxDepth {
		return nil
	}
	var nodes []Node
	for _, event := range children[parentID] {
		if seen[event.ID] {
			continue
		}
		seen[event.ID] = true
		node := Node{
			Event:    event,
			Depth:    depth + 1,
			ParentID: parentID,
		}
		node.Children = walk(event.ID, depth+1, children, seen)
		node.ReplyCount = countDescendants(node.Children)
		nodes = append(nodes, node)
	}
	return nodes
}

func maxNodeDepth(nodes []Node) int {
	max := 0
	for _, node := range nodes {
		if node.Depth > max {
			max = node.Depth
		}
		if childMax := maxNodeDepth(node.Children); childMax > max {
			max = childMax
		}
	}
	return max
}

func countDescendants(nodes []Node) int {
	total := 0
	for _, node := range nodes {
		total++
		total += countDescendants(node.Children)
	}
	return total
}

func parentFor(rootID string, event nostrx.Event) string {
	if event.Kind != nostrx.KindTextNote {
		return ""
	}
	rootID = NormalizeHexEventID(rootID)
	var firstE string
	var replyE string
	var rootE string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "e" {
			continue
		}
		ref := NormalizeHexEventID(tag[1])
		if firstE == "" {
			firstE = ref
		}
		switch strings.ToLower(nip10Marker(tag)) {
		case "reply":
			replyE = ref
		case "root":
			rootE = ref
		}
	}
	if replyE != "" {
		return replyE
	}
	if rootE != "" && rootE != rootID {
		return rootE
	}
	return firstE
}

// countHiddenForFocus counts every node in the tree that is NOT going to be
// rendered in focus mode. Visible in focus mode = the selected node itself
// plus all of its descendants. The parent (if any) is rendered as a header
// but does NOT count as a hidden reply. Everything else (other ancestors,
// siblings of nodes on the path, the parent's other replies, etc.) is hidden.
func countHiddenForFocus(roots []Node, selectedNode, parentNode *Node) int {
	if selectedNode == nil {
		return 0
	}
	visibleSubtree := 1 + countDescendants(selectedNode.Children)
	total := countDescendants(roots)
	hidden := total - visibleSubtree
	if parentNode != nil {
		hidden--
	}
	if hidden < 0 {
		return 0
	}
	return hidden
}
