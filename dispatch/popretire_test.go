package dispatch

import (
	"context"
	"encoding/json"
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
			return 0, ErrNoProcess
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
		"process unreadable": func(w *popWorld) {
			w.l.cfg.PopRetire.ProcStart = func(int) (uint64, error) { return 0, errors.New("permission denied") }
		},
		"messages wait": func(w *popWorld) { w.b.Cmd(ctx, "foureyes", "security", "directive", "", "more") },
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

// reviewer-codex: a closed pop's hand-off report is named in master's
// directive, to be read before it is unregistered.
func TestPopRetirePassNamesAClosedPopsHandoff(t *testing.T) {
	w := newPopWorld(t, "pop")
	ctx := context.Background()
	w.closePop()
	snaps, _ := w.b.Agents(ctx)
	h, _ := json.Marshal(bus.Handoff{Report: "1793000000000-0", PID: testPID, PIDStart: testStart, At: snaps["security"].TS})
	if err := w.r.HSet(ctx, bus.HandoffKey(w.b.Project()), "security", h).Err(); err != nil {
		t.Fatal(err)
	}
	w.pass()
	got := w.directives()
	if len(got) != 1 || !strings.Contains(got[0], "agentbus reports 1793000000000-0") {
		t.Fatalf("directives %q, want the hand-off report named", got)
	}
}

// pop-retire-auto: with End set the dispatcher ends a done pop itself and
// tells master with one notice that wakes nobody; once per episode.
func TestPopRetireEndsThePopItself(t *testing.T) {
	w := newPopWorld(t, "pop")
	var calls []string
	w.l.cfg.PopRetire.End = func(_ context.Context, token, role string, episode int64) PopEnd {
		calls = append(calls, token+" "+role+" "+strconv.FormatInt(episode, 10))
		return PopEnd{Ended: true, Detail: "pane w1:p3 closed, pid gone, unregistered (its consumer group kept)"}
	}
	w.pass()
	snaps, _ := w.b.Agents(context.Background())
	ep := strconv.FormatInt(snaps["security"].TS, 10)
	if len(calls) != 1 || calls[0] != "pop-holder security "+ep {
		t.Fatalf("End called %q", calls)
	}
	got := w.directives()
	if len(got) != 1 || !strings.HasPrefix(got[0], "no|") || !strings.Contains(got[0], "security was ended by the dispatcher") ||
		!strings.Contains(got[0], "pane w1:p3 closed") || strings.Contains(got[0], "agentbus retire") {
		t.Fatalf("master told %q", got)
	}
	w.pass() // the fake End unregisters nothing: the same episode is not ended twice
	if len(calls) != 1 || len(w.directives()) != 1 {
		t.Fatalf("a second pass: calls %q, master %q", calls, w.directives())
	}
}

// A wait (a turn running, a wake being typed) does nothing and tells nobody:
// the next pass judges again. A failure after the begin wakes master with the
// reason and the command to end it by hand.
func TestPopRetireEndWaitsOrFails(t *testing.T) {
	w := newPopWorld(t, "pop")
	n := 0
	w.l.cfg.PopRetire.End = func(context.Context, string, string, int64) PopEnd {
		n++
		if n == 1 {
			return PopEnd{Wait: "its pane runs a turn"}
		}
		return PopEnd{Failed: "pane w1:p3 closed but pid 4242 is still running after 15s"}
	}
	w.pass()
	if len(w.directives()) != 0 {
		t.Fatalf("a wait told master %q", w.directives())
	}
	w.pass()
	got := w.directives()
	if len(got) != 1 || !strings.HasPrefix(got[0], "|") || !strings.Contains(got[0], "could not end security") ||
		!strings.Contains(got[0], "still running") || !strings.Contains(got[0], "agentbus retire security --forget --episode") {
		t.Fatalf("master told %q", got)
	}
	w.pass()
	if n != 2 || len(w.directives()) != 1 {
		t.Fatalf("after a failure the episode is told once and not retried: calls %d, master %q", n, w.directives())
	}
}

// Never a boot role: PopDone judges pops only, End is never called.
func TestPopRetireEndNeverABootRole(t *testing.T) {
	w := newPopWorld(t, "boot")
	w.l.cfg.PopRetire.End = func(context.Context, string, string, int64) PopEnd {
		t.Fatal("End called for a boot role")
		return PopEnd{}
	}
	w.pass()
}
