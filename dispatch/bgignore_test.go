package dispatch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// background-ignore-helpers: the client's own helper, busy and under a
// shell (codebase-memory indexing), no longer holds a hand-off's /clear or
// a pop's retire (BackgroundSkip ""); a go test beside it still does.
func TestHostRuntimeIgnoresListedHelpers(t *testing.T) {
	f := newHostFixture(t)
	f.rt.Hooks = func(string, int) (agentstate.HookFacts, bool) { return agentstate.HookFacts{}, true }
	f.rt.Ignore = []string{"codebase-memory", "MainThread"}
	p := f.term.PID
	procs := func(cpu time.Duration, extra ...termhost.Proc) Inspection {
		all := append([]termhost.Proc{{PID: p, Start: f.term.PIDStart, Command: "claude"},
			{PID: p + 1, PPID: p, Start: 9, Command: "bash"},
			{PID: p + 2, PPID: p + 1, Start: 9, Command: "codebase-memory", CPU: cpu},
			{PID: p + 3, PPID: p + 2, Start: 9, Command: "MainThread", CPU: cpu}}, extra...)
		all[1].CPU = 0
		f.h.SetProcesses(f.term.ID, all...)
		return f.inspect()
	}
	procs(time.Second)
	f.now = f.now.Add(10 * time.Second)
	// The bash that only wraps listed helpers is their wrapper: not counted
	// either, so the default list needs no shell in it.
	if why := procs(9 * time.Second).BackgroundSkip(); why != "" {
		t.Fatalf("helpers only, under their bash wrapper: %q", why)
	}
	gotest := termhost.Proc{PID: p + 4, PPID: p + 1, Start: 9, Command: "go", CPU: 9 * time.Second}
	if why := procs(9*time.Second, gotest).BackgroundSkip(); !strings.Contains(why, "process 'go'") || !strings.Contains(why, "process 'bash'") {
		t.Fatalf("a go test beside them, under the same bash: %q", why)
	}
	// A shell with no child is a command running (a loop), never a wrapper.
	f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude"},
		termhost.Proc{PID: p + 1, PPID: p, Start: 9, Command: "bash", CPU: 9 * time.Second})
	if why := f.inspect().BackgroundSkip(); why != "background work: process 'bash'" {
		t.Fatalf("a lone shell: %q", why)
	}
	// An empty list ignores none: the helpers count (the bug's behaviour).
	f.rt.Ignore = []string{}
	if why := procs(9 * time.Second).BackgroundSkip(); why == "" {
		t.Fatal("an empty list, yet the busy helpers were ignored")
	}
	// A non-shell parent of an ignored helper is no wrapper: it counts, and so
	// does the shell holding it (under the client's shell, so no helper branch).
	f.rt.Ignore = []string{"codebase-memory", "MainThread"}
	f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude"},
		termhost.Proc{PID: p + 1, PPID: p, Start: 9, Command: "bash"},
		termhost.Proc{PID: p + 5, PPID: p + 1, Start: 9, Command: "node", CPU: 9 * time.Second},
		termhost.Proc{PID: p + 2, PPID: p + 5, Start: 9, Command: "codebase-memory", CPU: 9 * time.Second})
	if why := f.inspect().BackgroundSkip(); !strings.Contains(why, "process 'node'") || !strings.Contains(why, "process 'bash'") {
		t.Fatalf("a non-shell parent: %q", why)
	}
}

// Every runtime has the list without being given it (the dispatcher's and
// the holder drain's): nil reads dispatch.background_ignore, here its
// default.
func TestHostRuntimeReadsTheIgnoreSetting(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f := newHostFixture(t)
	f.rt.Hooks = func(string, int) (agentstate.HookFacts, bool) { return agentstate.HookFacts{}, true }
	p := f.term.PID
	set := func() Inspection {
		f.h.SetProcesses(f.term.ID, termhost.Proc{PID: p, Start: f.term.PIDStart, Command: "claude"},
			termhost.Proc{PID: p + 1, PPID: p, Start: 9, Command: "bash"},
			termhost.Proc{PID: p + 2, PPID: p + 1, Start: 9, Command: "codebase-memory", CPU: 9 * time.Second})
		return f.inspect()
	}
	set()
	f.now = f.now.Add(10 * time.Second)
	if why := set().BackgroundSkip(); why != "" {
		t.Fatalf("the default list was not read: %q", why)
	}
}

// The setting: the default list, and config.toml's.
func TestBackgroundIgnore(t *testing.T) {
	cfg := t.TempDir()
	env := map[string]string{"XDG_CONFIG_HOME": cfg}
	getenv := func(k string) string { return env[k] }
	if got := BackgroundIgnore(getenv); !slices.Equal(got, []string{"codebase-memory", "codex-code-mode", "MainThread", "import"}) {
		t.Fatalf("default %q", got)
	}
	_ = os.MkdirAll(filepath.Join(cfg, "agentbus"), 0o700)
	_ = os.WriteFile(filepath.Join(cfg, "agentbus", "config.toml"), []byte("[dispatch]\nbackground_ignore = [\"codebase-memory\", \"bash\"]\n"), 0o600)
	if got := BackgroundIgnore(getenv); !slices.Equal(got, []string{"codebase-memory", "bash"}) {
		t.Fatalf("config.toml %q", got)
	}
}
