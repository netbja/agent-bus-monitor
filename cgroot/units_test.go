package cgroot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// resource-limits §12.5, step 4b-2: a scope's name read back (the start
// sweep), the user manager's list and stop, and agent-enter's word to the
// holder that the scope launch reached it.

func TestParseScopeUnit(t *testing.T) {
	for _, c := range []struct {
		unit, p, key, role, term string
		ok                       bool
	}{
		{ScopeUnit("demo", "v0.35.0.abc1234567", "coder", "t2"), "demo", "v0.35.0.abc1234567", "coder", "t2", true},
		// Dashes in the project and the role: the key, the one part with a
		// dot (no project or role holds one), splits them.
		{ScopeUnit("agent-bus-monitor", "dev.0123456789", "reviewer-codex", "t14"), "agent-bus-monitor", "dev.0123456789", "reviewer-codex", "t14", true},
		{"agentbus-demo-dev.0123456789-coder-t2.service", "", "", "", "", false},
		{"agentbus-demo-coder-t2.scope", "", "", "", "", false},                // no key
		{"agentbus-demo-a.1-b.2-coder-t2.scope", "", "", "", "", false},        // two
		{"agentbus-dev.0123456789-coder-t2.scope", "", "", "", "", false},      // no project
		{"agentbus-demo-dev.0123456789-t2.scope", "", "", "", "", false},       // no role
		{"agentbus-demo-dev.0123456789-coder-x2.scope", "", "", "", "", false}, // not a terminal
		{"agentbus-holder.demo.dev.0123456789.scope", "", "", "", "", false},   // not ours
		{"other-demo-dev.0123456789-coder-t2.scope", "", "", "", "", false},
	} {
		p, key, role, term, ok := ParseScopeUnit(c.unit)
		if ok != c.ok || p != c.p || key != c.key || role != c.role || term != c.term {
			t.Errorf("%s: %q %q %q %q %v", c.unit, p, key, role, term, ok)
		}
	}
}

// fakeSystemctl records its argv in log, one line per call, and prints out.
func fakeSystemctl(t *testing.T, out string, code int) (bin, log string) {
	t.Helper()
	d := t.TempDir()
	bin, log = filepath.Join(d, "systemctl"), filepath.Join(d, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncat <<'END'\n" + out + "END\nexit " + itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	b, _ := os.ReadFile(log)
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func TestUserScopesListAndStop(t *testing.T) {
	own := ScopeUnit("demo", "dev.0123456789", "coder", "t2")
	other := ScopeUnit("demo", "old.0123456789", "architect", "t1")
	cousin := ScopeUnit("demo-x", "dev.0123456789", "coder", "t3") // the glob reaches project demo-x too
	bin, log := fakeSystemctl(t, own+" loaded active running x\n"+other+"   loaded active running y\n"+cousin+" loaded active running z\n", 0)
	u := UserScopes{Systemctl: bin}
	got, err := u.List(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{own, other}) {
		t.Errorf("list %q", got)
	}
	if c := calls(t, log); len(c) != 1 || c[0] != "--user list-units --all --plain --no-legend --type=scope agentbus-demo-*.scope" {
		t.Errorf("calls %q", c)
	}
	os.Remove(log)
	if err := u.Stop(context.Background(), own, other); err != nil {
		t.Fatal(err)
	}
	if c := calls(t, log); len(c) != 1 || c[0] != "--user stop -- "+own+" "+other {
		t.Errorf("calls %q", c)
	}
	os.Remove(log)
	if err := u.Stop(context.Background()); err != nil || len(calls(t, log)) != 0 {
		t.Errorf("no unit: %v, %q", err, calls(t, log))
	}
	// A failing manager is an error, never an empty list.
	bad, _ := fakeSystemctl(t, "Failed to connect to bus\n", 1)
	if _, err := (UserScopes{Systemctl: bad}).List(context.Background(), "demo"); err == nil || !strings.Contains(err.Error(), "Failed to connect to bus") {
		t.Errorf("list over a failed manager: %v", err)
	}
	if err := (UserScopes{Systemctl: bad}).Stop(context.Background(), own); err == nil {
		t.Error("stop over a failed manager: no error")
	}
}

// agent-enter tells the holder it was reached (EnterFDEnv): one byte on
// that fd, closed before the exec, and the variable is not the client's.
func TestEnterAndExecSaysEntered(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	scope := userSvc + "/agentbus.slice/agentbus-demo-k1-coder-t2.scope"
	d := t.TempDir()
	tree(t, d, scope, delegated(true))
	c := exec.Command(self, "--", "sh", "-c", `printf 'env=%s\n' "${`+EnterFDEnv+`-unset}"; if [ -e /proc/$$/fd/3 ]; then echo fd3; fi`)
	c.Env = append(os.Environ(), "CGROOT_TEST_ENTER="+d, "CGROOT_TEST_SELF="+scope, EnterFDEnv+"=3")
	c.ExtraFiles = []*os.File{w}
	out, err := c.Output()
	w.Close()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	b := make([]byte, 4)
	n, _ := r.Read(b)
	if string(b[:n]) != "y" {
		t.Errorf("word %q, want y", b[:n])
	}
	lines := strings.Fields(string(out))
	if len(lines) == 0 || lines[0] != "env=unset" {
		t.Errorf("the client saw %q", out)
	}
	if slices.Contains(lines, "fd3") {
		t.Errorf("fd 3 reached the client: %q", out)
	}
}
