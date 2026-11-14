package main

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// The prefix gate (spec 4.6): free attaches; bound is refused, naming the
// keys free for the client; unknown is refused unless the person chose the
// key (then one warning line); an observer is never gated.
func TestAttachGate(t *testing.T) {
	claude := agentstate.Profile{Client: "claude", Keys: map[string]string{"f12": "free", "f11": "free", "ctrl+g": "bound"}}
	codex := agentstate.Profile{Client: "codex"}
	for name, c := range map[string]struct {
		prefix   string
		explicit bool
		observe  bool
		p        agentstate.Profile
		ok       bool
		say      string
	}{
		"free":                 {"f12", false, false, claude, true, ""},
		"bound":                {"ctrl+g", true, false, claude, false, "f11, f12"},
		"unknown, default":     {"f12", false, false, codex, false, "--prefix"},
		"unknown, explicit":    {"f12", true, false, codex, true, "not verified for codex: press it twice"},
		"observer never gated": {"ctrl+g", false, true, claude, true, ""},
		"no client, default":   {"f12", false, false, agentstate.Profile{}, false, "--prefix"},
	} {
		warn, err := attachGate(c.prefix, c.explicit, c.observe, c.p)
		if (err == nil) != c.ok {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got := warn
		if err != nil {
			got = err.Error()
		}
		if !strings.Contains(got, c.say) {
			t.Errorf("%s: %q lacks %q", name, got, c.say)
		}
	}
}

// attachBuf is a buffer the session writes and the test reads.
type attachBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *attachBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *attachBuf) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// A whole attach on a real holder: the agent's terminal found by role, the
// person's keys reach it, F12 d detaches, the agent lives on, and the outer
// terminal is restored.
func TestAttachSession(t *testing.T) {
	w := newHolderWorld(t)
	dir := w.client(t, "cat")
	writeExe(t, dir+"/fake-client", "#!/bin/sh\nexec cat\n")
	w.writeEnv(t, map[string]string{"PATH": "/usr/bin:/bin:" + dir, service.HolderRepoEnv: w.repo})
	cl := w.start(t, w.tmp)
	if _, err := cl.Call(holder.Start{Role: "coder"}); err != nil {
		t.Fatalf("start: %v\n%s", err, w.out.String())
	}
	getenv := func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return w.run
		}
		return ""
	}
	sock, term, err := findAgentTerm(getenv, "demo", "coder")
	if err != nil || term.Role != "coder" || !term.Alive || term.Profile != "claude" {
		t.Fatalf("find: %+v %v", term, err)
	}
	in, typed := io.Pipe()
	out := &attachBuf{}
	done := make(chan error, 1)
	go func() {
		done <- runAttach(attachSession{Socket: sock, Term: term, Prefix: "f12", In: in, Out: out,
			Size: func() (int, int) { return 100, 30 }})
	}()
	io.WriteString(typed, "hello\r")
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), "hello"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no echo: %q", out.String())
		}
	}
	// The outer terminal's focus reports reach the holder as Focus, never
	// as keys: the terminal reads no viewer focused after a blur.
	focused := func() int {
		r, err := cl.Call(holder.ActivityReq{Term: term.ID})
		if err != nil || r.Activity == nil {
			t.Fatalf("activity: %+v %v", r, err)
		}
		return r.Activity.FocusedViewers
	}
	if !strings.Contains(out.String(), "\x1b[?1004h") {
		t.Error("attach did not ask the outer terminal for focus reports")
	}
	io.WriteString(typed, "\x1b[O")
	for deadline := time.Now().Add(5 * time.Second); focused() != 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a blur left the viewer focused")
		}
	}
	io.WriteString(typed, "\x1b[I")
	for deadline := time.Now().Add(5 * time.Second); focused() != 1; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a focus-in did not focus the viewer")
		}
	}
	io.WriteString(typed, "\x1b[24~d")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("detach: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("F12 d did not detach")
	}
	if !strings.Contains(out.String(), "\x1b[?1049l") || !strings.Contains(out.String(), "\x1b[?25h") {
		t.Errorf("the outer terminal was not restored: %q", out.String())
	}
	r, err := cl.Call(holder.List{})
	if err != nil || len(r.Terms) != 1 || !r.Terms[0].Alive {
		t.Fatalf("after detach: %+v %v", r.Terms, err)
	}
	if _, _, err := findAgentTerm(getenv, "demo", "nosuch"); err == nil {
		t.Fatal("an agent with no terminal was found")
	}
}
