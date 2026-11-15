package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// agentbus checkback (master-idle §4): set, replace, clear, list; the due
// time is Redis's; a bad duration or text is refused.
func TestCheckbackCmd(t *testing.T) {
	b, _ := dialMain(t)
	ctx := context.Background()
	run := func(self string, args ...string) (int, string) {
		var out, errw bytes.Buffer
		code := runCheckback(ctx, b, self, args, &out, &errw)
		return code, out.String() + errw.String()
	}
	if code, msg := run("master", "30m", "k1/k2 checks on 7d27262c, fa2d796f"); code != 0 || !strings.Contains(msg, "check back in 30m: k1/k2 checks") {
		t.Fatalf("set: %d %q", code, msg)
	}
	if code, msg := run("master", "1h", "k1/k2 checks on 7d27262c, fa2d796f"); code != 0 || !strings.Contains(msg, "(set 2 times") {
		t.Fatalf("again: %d %q", code, msg)
	}
	run("coder", "10m", "a long build")
	code, msg := run("master")
	if code != 0 || !strings.Contains(msg, "master  due in ") || !strings.Contains(msg, "set 2 times since") || !strings.Contains(msg, "coder  due in ") {
		t.Fatalf("list: %d %q", code, msg)
	}
	if code, msg := run("master", "--clear"); code != 0 || !strings.Contains(msg, "cleared") {
		t.Fatalf("clear: %d %q", code, msg)
	}
	if code, msg := run("master", "--clear"); code != 0 || !strings.Contains(msg, "no check-back") {
		t.Fatalf("clear again: %d %q", code, msg)
	}
	for _, bad := range [][]string{{"0m", "x"}, {"soon", "x"}, {"30m"}, {"30m", strings.Repeat("x", 201)}, {"200h", "x"}} {
		if code, _ := run("master", bad...); code != 2 {
			t.Errorf("%q accepted (%d)", bad, code)
		}
	}
	// A past due time reads as due, how long ago.
	if _, err := b.SetCheckback(ctx, "master", time.Millisecond, "k1/k2"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, msg := run("master"); !strings.Contains(msg, "master  due ") || !strings.Contains(msg, " ago") {
		t.Fatalf("past due: %q", msg)
	}
}

// Setting and clearing write as the caller: the identity guard checks them;
// the list is a read.
func TestCheckbackReadOnly(t *testing.T) {
	if !readOnlyCommand("checkback", nil) || readOnlyCommand("checkback", []string{"30m", "x"}) || readOnlyCommand("checkback", []string{"--clear"}) {
		t.Fatal("checkback's writes read as read-only, or its list not")
	}
}
