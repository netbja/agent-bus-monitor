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
