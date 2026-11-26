package holder

import (
	"os"
	"testing"
)

// Each process of a terminal's tree carries its resident memory
// (agent-resource-limits §5: rss_kb, from /proc/<pid>/statm).
func TestProcessTreeRSS(t *testing.T) {
	tree := processTree(os.Getpid())
	if len(tree) == 0 || tree[0].PID != os.Getpid() {
		t.Fatalf("tree %+v", tree)
	}
	if tree[0].RSSkb <= 0 {
		t.Fatalf("own rss_kb = %d", tree[0].RSSkb)
	}
	if rssKB(-1) != 0 {
		t.Fatal("a missing process has rss")
	}
}
