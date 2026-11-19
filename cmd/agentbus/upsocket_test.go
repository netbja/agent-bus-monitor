package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// P0-2: a holder socket path too long for this OS refuses up before anything
// starts (no bootstrap, no service, no holder), naming the path and the fix,
// instead of leaving a holder that never listens and a dispatcher waiting.
func TestUpRefusesAHolderSocketThatCannotFit(t *testing.T) {
	work, _, h, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.HolderBuild = func() string { return "v0.28.0-877-g1691ae95+dirty" }
	deep := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	env0 := deps.Env
	deps.Env = func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return deep
		}
		return env0(k)
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 1 {
		t.Fatalf("up = %d, want 1\n%s\n%s", code, out.String(), errw.String())
	}
	if len(*boot) != 0 || len(u.m.Calls) != 0 || len(h.started()) != 0 {
		t.Fatalf("something started: bootstrap %q, manager %q, agents %v", *boot, u.m.Calls, h.started())
	}
	for _, want := range []string{"agentbus up:", "holder socket path", "XDG_RUNTIME_DIR", "nothing started"} {
		if !strings.Contains(errw.String(), want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errw.String())
		}
	}
}
