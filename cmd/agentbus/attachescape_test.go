package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// The key as the person reads it on screen: F12, Ctrl-].
func TestKeyLabel(t *testing.T) {
	for k, want := range map[string]string{"f12": "F12", "f7": "F7", "ctrl+]": "Ctrl-]", "ctrl+\\": "Ctrl-\\", "ctrl+g": "Ctrl-G"} {
		if got := keyLabel(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
}

// attachWorld is a holder running coder as cat (it echoes what reaches it),
// and coder's terminal found as attach finds it.
func attachWorld(t *testing.T) (*holder.Client, string, holder.TermInfo) {
	t.Helper()
	w := newHolderWorld(t)
	dir := w.client(t, "cat")
	writeExe(t, dir+"/fake-client", "#!/bin/sh\nexec cat\n")
	w.writeEnv(t, map[string]string{"PATH": "/usr/bin:/bin:" + dir, service.HolderRepoEnv: w.repo})
	cl := w.start(t, w.tmp)
	if _, err := cl.Call(holder.Start{Role: "coder"}); err != nil {
		t.Fatalf("start: %v\n%s", err, w.out.String())
	}
	sock, term, err := findAgentTerm(func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return w.run
		}
		return ""
	}, "demo", "coder")
	if err != nil {
		t.Fatal(err)
	}
	return cl, sock, term
}

func waitFor(t *testing.T, out *attachBuf, s string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), s); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in %q", s, out.String())
		}
	}
}

// P1-8: the person could not leave an attach (F12 was their desktop's, and
// nothing on screen named the way out). The detach key is shown on screen
// as the attach starts, before the agent's screen, and stays in the outer
// terminal's title; a key ends the banner at once and still reaches the agent.
func TestAttachShowsTheDetachKey(t *testing.T) {
	_, sock, term := attachWorld(t)
	in, typed := io.Pipe()
	out := &attachBuf{}
	done := make(chan error, 1)
	go func() {
		done <- runAttach(attachSession{Socket: sock, Term: term, Prefix: "ctrl+]", In: in, Out: out,
			Size: func() (int, int) { return 100, 30 }, Banner: time.Minute})
	}()
	waitFor(t, out, "To leave: press Ctrl-] then d.")
	io.WriteString(typed, "hello\r") // ends the banner long before its minute
	waitFor(t, out, "hello")
	got := out.String()
	if i := strings.Index(got, "To leave: press Ctrl-] then d."); i < 0 || i > strings.Index(got, "hello") {
		t.Fatalf("the banner is not shown before the agent's screen: %q", got)
	}
	if !strings.Contains(got, "\x1b]2;agentbus attach coder: Ctrl-] then d detaches\x07") {
		t.Fatalf("the title does not name the way out: %q", got)
	}
	io.WriteString(typed, "\x1dd")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ctrl-] d did not detach")
	}
}

// The way out never reaches the agent: after hello, Ctrl-] d detaches, and
// the agent's screen, read by an observer, holds hello and neither the
// prefix (cat's terminal echoes it as ^]) nor the d.
func TestAttachEscapeNeverReachesTheAgent(t *testing.T) {
	_, sock, term := attachWorld(t)
	in, typed := io.Pipe()
	out := &attachBuf{}
	done := make(chan error, 1)
	go func() {
		done <- runAttach(attachSession{Socket: sock, Term: term, Prefix: "ctrl+]", In: in, Out: out,
			Size: func() (int, int) { return 100, 30 }})
	}()
	io.WriteString(typed, "hello\r")
	waitFor(t, out, "hello")
	io.WriteString(typed, "\x1dd")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // anything that did reach cat is echoed by now

	oin, otyped := io.Pipe()
	seen := &attachBuf{}
	odone := make(chan error, 1)
	go func() {
		odone <- runAttach(attachSession{Socket: sock, Term: term, Prefix: "ctrl+]", Observe: true, In: oin, Out: seen,
			Size: func() (int, int) { return 100, 30 }})
	}()
	waitFor(t, seen, "hello")
	time.Sleep(200 * time.Millisecond)
	io.WriteString(otyped, "\x1dd")
	if err := <-odone; err != nil {
		t.Fatal(err)
	}
	screen := seen.String()
	if i := strings.Index(screen, "detaches\x07"); i >= 0 { // past the title naming the key
		screen = screen[i:]
	}
	if strings.Contains(screen, "^]") || strings.Contains(screen, "\x1d") || strings.Contains(screen, "hello\r\nd") {
		t.Fatalf("the escape reached the agent: %q", screen)
	}
}
