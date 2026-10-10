package dispatch

import (
	"os"
	"testing"
)

func TestAncestorAmongFindsParent(t *testing.T) {
	parent := os.Getppid()
	got, ok := AncestorAmong([]int{999999999, parent}, os.Getpid())
	if !ok || got != parent {
		t.Fatalf("AncestorAmong = %d, %v; want parent %d", got, ok, parent)
	}
	if _, ok := AncestorAmong([]int{999999999}, os.Getpid()); ok {
		t.Fatal("AncestorAmong found an ancestor among non-ancestors")
	}
	if _, ok := AncestorAmong(nil, os.Getpid()); ok {
		t.Fatal("AncestorAmong found an ancestor among no candidates")
	}
	// Self is never its own ancestor: {self, parent} -> parent.
	got, ok = AncestorAmong([]int{os.Getpid(), parent}, os.Getpid())
	if !ok || got != parent {
		t.Fatalf("AncestorAmong({self, parent}) = %d, %v; want parent %d", got, ok, parent)
	}
	if _, ok := AncestorAmong([]int{os.Getpid()}, os.Getpid()); ok {
		t.Fatal("AncestorAmong counted self as an ancestor")
	}
}

// The agent is the outermost foreground ancestor, not a tool shell under it.
// Chain: 50 (agentbus) -> 40 (tool shell) -> 30 (agent) -> 20 (pane shell) -> 1.
func TestAncestorAmongPrefersOutermost(t *testing.T) {
	ppid := map[int]int{50: 40, 40: 30, 30: 20, 20: 1}
	lookup := func(pid int) (int, error) {
		p, ok := ppid[pid]
		if !ok {
			return 0, os.ErrNotExist
		}
		return p, nil
	}
	got, ok := ancestorAmong([]int{50, 40, 30}, 50, lookup)
	if !ok || got != 30 {
		t.Fatalf("ancestorAmong = %d, %v; want outermost 30", got, ok)
	}
	if got, ok := ancestorAmong([]int{40}, 50, lookup); !ok || got != 40 {
		t.Fatalf("single candidate: %d, %v; want 40", got, ok)
	}
	if _, ok := ancestorAmong([]int{99}, 50, lookup); ok {
		t.Fatal("found a non-ancestor")
	}
}

func TestProcStartStable(t *testing.T) {
	a, err := ProcStart(os.Getpid())
	if err != nil {
		t.Fatalf("ProcStart(self): %v", err)
	}
	b, err := ProcStart(os.Getpid())
	if err != nil || a != b || a == 0 {
		t.Fatalf("ProcStart not stable: %d then %d (%v)", a, b, err)
	}
	p, err := ProcStart(os.Getppid())
	if err != nil || p == 0 || p > a {
		t.Fatalf("parent start %d (%v) should be non-zero and not after self %d", p, err, a)
	}
	if _, err := ProcStart(999999999); err == nil {
		t.Fatal("ProcStart of a missing pid succeeded")
	}
}

// A comm with spaces and a closing paren must not shift the fields.
func TestParseStatOddComm(t *testing.T) {
	line := "4242 (we (ird) name) S 77 4242 4242 0 -1 4194560 1 0 0 0 0 0 0 0 20 0 1 0 123456 0 0"
	f, err := parseStat(line)
	if err != nil {
		t.Fatal(err)
	}
	if f.ppid != 77 || f.start != 123456 {
		t.Fatalf("parseStat = %+v; want ppid 77 start 123456", f)
	}
}
