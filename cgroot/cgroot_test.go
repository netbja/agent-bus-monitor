package cgroot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resource-limits §12.4, §12.9 step 4a (spec
// docs/superpowers/specs/2026-11-25-agent-resource-limits-design.md): the
// cgroup root of an agent, found from /proc/self/cgroup (a seam) in a fake
// tree; no live systemd.

// tree makes dir/rel with the given files (name → content).
func tree(t *testing.T, dir, rel string, files map[string]string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(p, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func fsAt(dir, self string) FS {
	return FS{Mount: dir, GOOS: "linux", ProcSelf: func() ([]byte, error) { return []byte("0::" + self + "\n"), nil }}
}

const userSvc = "/user.slice/user-1000.slice/user@1000.service"

func delegated(kill bool) map[string]string {
	f := map[string]string{"cgroup.controllers": "cpu memory pids", "cgroup.subtree_control": "", "cgroup.freeze": "0",
		"memory.current": "1048576", "memory.max": "8589934592", "memory.events": "low 0\nhigh 2\nmax 3\noom 5\noom_kill 1\n"}
	if kill {
		f["cgroup.kill"] = ""
	}
	return f
}

func TestSelfModes(t *testing.T) {
	scope := userSvc + "/agentbus.slice/agentbus-demo-k1-coder-t2.scope"
	holder := "/user.slice/user-1000.slice/user@1000.service/agentbus.slice/agentbus-holder.demo.k1.service/holder/agents/demo.coder.t2"
	for name, c := range map[string]struct {
		dir, self string
		mode      Mode
		kill      KillMethod
	}{
		"scope, client leaf":      {scope, scope + "/client", ModeScope, KillCgroup},
		"scope, its root":         {scope, scope, ModeScope, KillCgroup},
		"holder agents, client":   {holder, holder + "/client", ModeHolder, KillCgroup},
		"holder agents, cmd leaf": {holder, holder + "/cmd-42", ModeHolder, KillCgroup},
		"a scope at the top":      {"/agentbus-demo-k1-coder-t2.scope", "/agentbus-demo-k1-coder-t2.scope/client", ModeScope, KillCgroup},
	} {
		d := t.TempDir()
		tree(t, d, c.dir, delegated(true))
		r, err := fsAt(d, c.self).Self()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.Mode() != c.mode || !r.Delegated() || r.Kill() != c.kill || r.Path() != filepath.Join(d, c.dir) {
			t.Errorf("%s: mode %s delegated %v kill %s path %s", name, r.Mode(), r.Delegated(), r.Kill(), r.Path())
		}
	}
}

func TestSelfSoft(t *testing.T) {
	scope := userSvc + "/agentbus.slice/agentbus-demo-k1-coder-t2.scope"
	notDelegated := delegated(true)
	notDelegated["cgroup.controllers"] = "cpu pids"
	for name, c := range map[string]struct {
		fs     func(d string) FS
		reason string
	}{
		"not an agent's cgroup": {func(d string) FS {
			tree(t, d, userSvc+"/app.slice/x.scope", delegated(true))
			return fsAt(d, userSvc+"/app.slice/x.scope")
		}, "not in an agent's cgroup"},
		"an agentbus- unit that is no scope": {func(d string) FS { // foureyes on 4a
			tree(t, d, userSvc+"/agentbus-demo-k1-coder-t2.service", delegated(true))
			return fsAt(d, userSvc+"/agentbus-demo-k1-coder-t2.service")
		}, "not in an agent's cgroup"},
		"agents without holder above": {func(d string) FS {
			tree(t, d, userSvc+"/x/agents/demo.coder.t2", delegated(true))
			return fsAt(d, userSvc+"/x/agents/demo.coder.t2/client")
		}, "not in an agent's cgroup"},
		"subtree_control not writable": {func(d string) FS {
			p := tree(t, d, scope, delegated(true))
			if err := os.Chmod(filepath.Join(p, "cgroup.subtree_control"), 0o444); err != nil {
				t.Fatal(err)
			}
			return fsAt(d, scope+"/client")
		}, "memory not delegated"},
		"memory not delegated": {func(d string) FS { tree(t, d, scope, notDelegated); return fsAt(d, scope+"/client") }, "memory not delegated"},
		"macOS":                {func(d string) FS { f := fsAt(d, scope); f.GOOS = "darwin"; return f }, "no cgroups on darwin"},
		"unreadable /proc/self/cgroup": {func(d string) FS {
			return FS{Mount: d, GOOS: "linux", ProcSelf: func() ([]byte, error) { return nil, os.ErrNotExist }}
		}, "cannot read /proc/self/cgroup"},
		"a cgroup v1 line only": {func(d string) FS {
			return FS{Mount: d, GOOS: "linux", ProcSelf: func() ([]byte, error) { return []byte("4:memory:/x\n"), nil }}
		}, "no cgroup v2"},
	} {
		r, err := c.fs(t.TempDir()).Self()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.Mode() != ModeSoft || r.Delegated() || !strings.Contains(r.Reason(), c.reason) {
			t.Errorf("%s: %s %q", name, r.Mode(), r.Reason())
		}
		if _, _, err := r.Child("cmd-1", "1G", "", ""); err == nil {
			t.Errorf("%s: a soft root made a child", name)
		}
	}
}

// Without cgroup.kill but with cgroup.freeze: freeze-and-kill; with
// neither: none.
func TestKillMethod(t *testing.T) {
	scope := userSvc + "/agentbus.slice/agentbus-demo-k1-coder-t2.scope"
	d := t.TempDir()
	tree(t, d, scope, delegated(false))
	if r, _ := fsAt(d, scope).Self(); r.Kill() != KillFreeze {
		t.Errorf("freeze only: %s", r.Kill())
	}
	os.Remove(filepath.Join(d, scope, "cgroup.freeze"))
	if r, _ := fsAt(d, scope).Self(); r.Kill() != KillNone {
		t.Errorf("neither: %s", r.Kill())
	}
}

func TestChildAndUsage(t *testing.T) {
	scope := userSvc + "/agentbus.slice/agentbus-demo-k1-coder-t2.scope"
	d := t.TempDir()
	tree(t, d, scope, delegated(true))
	r, err := fsAt(d, scope+"/client").Self()
	if err != nil {
		t.Fatal(err)
	}
	fd, path, err := r.Child("cmd-42", "4G", "3G", "0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFD(fd)
	if path != filepath.Join(d, scope, "cmd-42") || fd < 0 {
		t.Fatalf("child %d %s", fd, path)
	}
	for f, want := range map[string]string{"memory.max": "4G", "memory.high": "3G", "memory.swap.max": "0"} {
		if b, _ := os.ReadFile(filepath.Join(path, f)); string(b) != want {
			t.Errorf("%s = %q, want %q", f, b, want)
		}
	}
	_, bare, err := r.Child("cmd-43", "1G", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"memory.high", "memory.swap.max"} {
		if _, err := os.Stat(filepath.Join(bare, f)); err == nil {
			t.Errorf("an empty %s was written", f)
		}
	}
	if _, _, err := r.Child("../escape", "1G", "", ""); err == nil {
		t.Error("a child name with a slash")
	}
	if _, _, err := r.Child("", "1G", "", ""); err == nil { // foureyes on 4a
		t.Error("an empty child name")
	}
	if _, _, err := r.Child("client", "1G", "", ""); err == nil {
		t.Error("a child named client")
	}
	cur, max, ooms, err := r.Usage()
	if err != nil || cur != 1048576 || max != 8589934592 || ooms != 1 {
		t.Fatalf("usage %d %d %d %v", cur, max, ooms, err)
	}
	os.WriteFile(filepath.Join(d, scope, "memory.max"), []byte("max\n"), 0o644)
	if _, max, _, err := r.Usage(); err != nil || max != 0 {
		t.Fatalf("max unlimited: %d %v", max, err)
	}
}

// Host: what the user manager offers the holder (the doctor line).
func TestHostAndDoctorLine(t *testing.T) {
	d := t.TempDir()
	tree(t, d, userSvc, delegated(true))
	h := fsAt(d, userSvc+"/app.slice/term.scope").Host()
	if h.Mode() != ModeScope || h.Kill() != KillCgroup {
		t.Fatalf("host: %s %s %q", h.Mode(), h.Kill(), h.Reason())
	}
	if got := DoctorLine(h, "yes"); got != "cgroup mode: scope, memory delegated, kill: cgroup.kill, linger on" {
		t.Errorf("line %q", got)
	}
	if got := DoctorLine(h, "no"); got != "cgroup mode: scope, memory delegated, kill: cgroup.kill, linger off: holder stops at logout" {
		t.Errorf("line %q", got)
	}
	soft := fsAt(t.TempDir(), "/system.slice/sshd.service").Host()
	if got := DoctorLine(soft, ""); got != "cgroup mode: soft: no systemd user manager, linger unknown" {
		t.Errorf("soft line %q", got)
	}
}
