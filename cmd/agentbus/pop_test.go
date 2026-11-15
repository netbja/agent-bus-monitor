package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// herdr exit §4.2: agentbus pop <role> starts the role as a new terminal of
// the project's holder (agent-spawn's successor): on its own profile or one
// named, resuming a session when asked; a bad name or argument is refused
// before any Start.
func TestPop(t *testing.T) {
	h := &recHost{Host: fake.New()}
	run := func(args ...string) (int, string) {
		var out, errw bytes.Buffer
		code := runPop(context.Background(), h, "demo", args, &out, &errw)
		return code, out.String() + errw.String()
	}
	if code, msg := run("docs"); code != 0 || !strings.Contains(msg, "docs: started") {
		t.Fatalf("pop docs: %d %q", code, msg)
	}
	if code, _ := run("reviewer-codex", "--profile", "codex-review", "--resume", "5f2b1c3d-aa"); code != 0 {
		t.Fatalf("pop with a profile and a session: %d", code)
	}
	s := h.specs[1]
	if s.Project != "demo" || s.Name != "reviewer-codex" || s.Profile != "codex-review" || s.ResumeSession != "5f2b1c3d-aa" {
		t.Fatalf("spec %+v", s)
	}
	for _, bad := range [][]string{{}, {"Bad Role"}, {"docs", "--profile", "a b"}, {"docs", "--resume", "x;rm"}, {"docs", "extra"}} {
		if code, _ := run(bad...); code != 2 {
			t.Errorf("%q accepted (%d)", bad, code)
		}
	}
	if len(h.specs) != 2 {
		t.Fatalf("started %v", h.started())
	}
}
