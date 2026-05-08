package httpx

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"

	"ptxt-nstr/internal/store"
)

// TestWebOfTrustMaxDepthMatchesClient guards against drift between the Go
// canonical constant (store.MaxDepth) and the JS client constant
// (MAX_WEB_OF_TRUST_DEPTH in web/static/js/sort-prefs.js). Bumping one
// without the other silently breaks the settings UI vs server behavior.
func TestWebOfTrustMaxDepthMatchesClient(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	jsPath := filepath.Join(repoRoot, "web", "static", "js", "sort-prefs.js")
	body, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("read %s: %v", jsPath, err)
	}
	re := regexp.MustCompile(`MAX_WEB_OF_TRUST_DEPTH\s*=\s*(\d+)`)
	match := re.FindStringSubmatch(string(body))
	if len(match) != 2 {
		t.Fatalf("could not find MAX_WEB_OF_TRUST_DEPTH in %s", jsPath)
	}
	jsValue, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	if jsValue != store.MaxDepth {
		t.Fatalf("MAX_WEB_OF_TRUST_DEPTH (%d) != store.MaxDepth (%d); update both",
			jsValue, store.MaxDepth)
	}
}
