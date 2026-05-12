package store

// MaxDepth is the canonical upper bound for web-of-trust BFS depth.
// HTTP request parsing, the settings template, and the JS client constant
// (web/static/js/sort-prefs.js MAX_WEB_OF_TRUST_DEPTH) must all agree with it.
const MaxDepth = 3

// ClampDepth returns depth bounded to [1, MaxDepth].
func ClampDepth(depth int) int {
	if depth < 1 {
		return 1
	}
	if depth > MaxDepth {
		return MaxDepth
	}
	return depth
}
