package dispatch

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
)

// popWorld: security, a verified pop idle on pane w1:p3; the fake pane is
// ready; pop_retire_after is 0 so the declaration is old enough at once.
type popWorld struct {
	t  *testing.T
	b  *bus.Bus
	r  *redis.Client
	rt *fakeRT
	l  *loop
}

func newPopWorld(t *testing.T, tier string) *popWorld {
	t.Helper()
	b, r := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "pop-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	who := bus.AgentIdent{Pane: "w1:p3", PID: testPID, PIDStart: testStart, Client: "codex", Provider: "openai", Tier: tier}
	if _, err := b.Status(ctx, "security", "idle", "review sent", who); err != nil {
		t.Fatal(err)
	}
	w := &popWorld{t: t, b: b, r: r}
	w.rt = newFakeRT(func(string, int) Inspection { return readyIn() })
	w.l = &loop{b: b, rt: w.rt, token: "pop-holder", last: map[string]string{},
		cfg: Config{EscalateTo: "master", Log: io.Discard, PopRetire: PopRetireConfig{On: true,
			Missions: func(context.Context, string) ([]string, error) { return nil, nil }}}}
	return w
}

func (w *popWorld) pass() {
	w.t.Helper()
	snaps, err := w.b.Agents(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	w.l.snaps = snaps
	if err := w.l.popRetirePass(context.Background(), snaps); err != nil {
		w.t.Fatalf("popRetirePass: %v", err)
	}
}

// directives are the dispatcher's cmd entries to master, as wake|command.
func (w *popWorld) directives() []string {
	var out []string
	for _, m := range entries(w.t, w.r, w.b, "cmd") {
		if m.Values["from"] == dispatchFrom && m.Values["target"] == "master" {
			wake, _ := m.Values["wake"].(string)
			out = append(out, wake+"|"+m.Values["command"].(string))
		}
	}
	return out
}

func TestPopRetirePassTellsMasterOncePerEpisode(t *testing.T) {
	w := newPopWorld(t, "pop")
	ctx := context.Background()
	w.pass()
	w.pass()
	got := w.directives()
	snaps, _ := w.b.Agents(ctx)
	episode := snaps["security"].TS
	if len(got) != 1 || !strings.HasPrefix(got[0], "|") ||
		!strings.Contains(got[0], "agentbus retire security --forget --episode "+strconv.FormatInt(episode, 10)+` --reason "work done"`) {
		t.Fatalf("directives %q, want one waking directive naming episode %d", got, episode)
	}
	// It works and idles again: a new episode, a new directive.
	time.Sleep(2 * time.Millisecond)
	w.b.Status(ctx, "security", "working", "again", bus.AgentIdent{})
	w.pass()
	time.Sleep(2 * time.Millisecond)
	w.b.Status(ctx, "security", "idle", "done again", bus.AgentIdent{})
	w.pass()
	if got := w.directives(); len(got) != 2 {
		t.Fatalf("after a new episode: %q", got)
	}
}

func TestPopRetirePassSendsNothing(t *testing.T) {
	ctx := context.Background()
	for name, arrange := range map[string]func(w *popWorld){
		"a boot role": nil,
		"off":         func(w *popWorld) { w.l.cfg.PopRetire.On = false },
		"open work":   func(w *popWorld) { w.b.Cmd(ctx, "foureyes", "security", "directive", "", "more") },
		"focused": func(w *popWorld) {
			w.rt.script = func(string, int) Inspection { in := readyIn(); in.Focused = true; return in }
		},
		"master held": func(w *popWorld) {
			w.b.Status(ctx, "master", "idle", "", bus.AgentIdent{Provider: "anthropic"})
			w.l.quota.held = map[string]bool{"anthropic": true}
		},
	} {
		tier := "pop"
		if name == "a boot role" {
			tier = "boot"
		}
		w := newPopWorld(t, tier)
		if arrange != nil {
			arrange(w)
		}
		w.pass()
		if got := w.directives(); len(got) != 0 {
			t.Errorf("%s: %q", name, got)
		}
	}
}

// closePop makes security's process gone and its pane unknown to herdr,
// after it declared working (pop-closed-by-hand: closed by hand mid-turn).
func (w *popWorld) closePop() {
	w.t.Helper()
	time.Sleep(2 * time.Millisecond)
	if _, err := w.b.Status(context.Background(), "security", "working", "on it", bus.AgentIdent{}); err != nil {
		w.t.Fatal(err)
	}
	w.l.cfg.PopRetire.ProcStart = func(pid int) (uint64, error) {
		if pid == testPID {
			return 0, errors.New("no such process")
		}
		return testStart, nil
	}
	w.rt.script = func(string, int) Inspection { return Inspection{Gone: true} }
}

func TestPopRetirePassTellsMasterOfAClosedPop(t *testing.T) {
	w := newPopWorld(t, "pop")
	w.closePop()
	w.pass()
	w.pass()
	snaps, _ := w.b.Agents(context.Background())
	episode := strconv.FormatInt(snaps["security"].TS, 10)
	got := w.directives()
	if len(got) != 1 || !strings.HasPrefix(got[0], "|") || !strings.Contains(got[0], "closed") ||
		!strings.Contains(got[0], "agentbus retire security --forget --episode "+episode) {
		t.Fatalf("directives %q, want one waking directive for the closed pop, episode %s", got, episode)
	}
}

func TestPopRetirePassKeepsAPopNotClosed(t *testing.T) {
	ctx := context.Background()
	for name, arrange := range map[string]func(w *popWorld){
		"a boot role": nil,
		"process alive": func(w *popWorld) {
			w.l.cfg.PopRetire.ProcStart = func(int) (uint64, error) { return testStart, nil }
		},
		"pane still runs a process": func(w *popWorld) {
			w.rt.script = func(string, int) Inspection { return readyIn() }
		},
		"pane unreadable": func(w *popWorld) {
			w.rt.script = func(string, int) Inspection { return Inspection{Err: errors.New("herdr down")} }
		},
		"no process reader": func(w *popWorld) { w.l.cfg.PopRetire.ProcStart = nil },
		"messages wait":     func(w *popWorld) { w.b.Cmd(ctx, "foureyes", "security", "directive", "", "more") },
	} {
		tier := "pop"
		if name == "a boot role" {
			tier = "boot"
		}
		w := newPopWorld(t, tier)
		w.closePop()
		if arrange != nil {
			arrange(w)
		}
		w.pass()
		if got := w.directives(); len(got) != 0 {
			t.Errorf("%s: %q", name, got)
		}
	}
}
