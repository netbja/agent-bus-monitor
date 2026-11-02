package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// popRT is the pane of the pop "security": ready and unfocused unless
// onInspect, called on the n-th inspection (1 = retire's first check, 2
// after the begin, 3 right before the close), changes the world first.
type popRT struct {
	mu        sync.Mutex
	n         int
	focused   bool
	onInspect func(n int)
}

func (p *popRT) Inspect(context.Context, string) dispatch.Inspection {
	p.mu.Lock()
	p.n++
	n, hook := p.n, p.onInspect
	p.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return dispatch.Inspection{State: "idle", Ready: true, Focused: p.focused, PIDs: []int{6161}, Starts: map[int]uint64{6161: 61}}
}
func (*popRT) Prompt(context.Context, string, string, string) (bool, error) { return false, nil }
func (*popRT) Clear(context.Context, string) error                          { return nil }

// newPopWorld is the swap world plus a pop, security, idle on pane p-sec,
// whose close ends its process; afterClose runs once the pane is closed.
func newPopWorld(t *testing.T) (*swapWorld, swapDeps, *popRT, *func()) {
	t.Helper()
	w, d := newSwapWorld(t)
	ctx := context.Background()
	w.procs[6161] = 61
	if _, err := w.b.Status(ctx, "security", "idle", "review sent", bus.AgentIdent{Pane: "p-sec", PID: 6161, PIDStart: 61,
		Client: "codex", Provider: "openai", Tier: "pop"}); err != nil {
		t.Fatal(err)
	}
	afterClose := func() {}
	close := d.ClosePane
	d.ClosePane = func(ctx context.Context, pane string) error {
		err := close(ctx, pane)
		if pane == "p-sec" {
			w.mu.Lock()
			delete(w.procs, 6161)
			w.mu.Unlock()
			afterClose()
		}
		return err
	}
	rt := &popRT{}
	d.Runtime, d.PopRetireAfter = rt, 0
	return w, d, rt, &afterClose
}

func (w *swapWorld) episode(t *testing.T) string {
	t.Helper()
	snaps, err := w.b.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return strconv.FormatInt(snaps["security"].TS, 10)
}

func (w *swapWorld) retireForget(d swapDeps, episode string) (int, string) {
	var out, errw bytes.Buffer
	code := runRetire(context.Background(), w.b, d, "master", []string{"security", "--forget", "--episode", episode, "--reason", "work done"}, &out, &errw)
	return code, out.String() + errw.String()
}

func (w *swapWorld) registered(t *testing.T, role string) bool {
	t.Helper()
	snaps, err := w.b.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, ok := snaps[role]
	return ok
}

func TestRetireForgetEndsAFinishedPop(t *testing.T) {
	w, d, _, _ := newPopWorld(t)
	ctx := context.Background()
	if code, out := w.retireForget(d, w.episode(t)); code != 0 || !strings.Contains(out, "unregistered") {
		t.Fatalf("retire --forget: %d %s", code, out)
	}
	if w.registered(t, "security") {
		t.Fatal("the pop is still registered")
	}
	log, _ := w.b.SwapLog(ctx, "security", 0)
	if len(log) == 0 || log[0].Event != bus.SwapRetire || log[0].Forgot != "true" {
		t.Fatalf("ledger %+v", log)
	}
	var out, errw bytes.Buffer
	if runSwaps(ctx, w.b, []string{"--role", "security"}, &out, &errw); !strings.Contains(out.String(), "forgot") {
		t.Fatalf("swaps: %q", out.String())
	}
	if ok, _ := w.b.Swapping(ctx, "security"); ok {
		t.Fatal("intent left behind")
	}
}

func TestRetireForgetRefusals(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, args, want string
	}{
		{"no episode", "security --forget", "needs --episode"},
		{"with --now", "security --forget --episode 5 --now", "refuses --now"},
		{"episode without forget", "security --episode 5", "needs --episode"},
	} {
		w, d, _, _ := newPopWorld(t)
		var out, errw bytes.Buffer
		if code := runRetire(ctx, w.b, d, "master", strings.Fields(c.args), &out, &errw); code != 2 || !strings.Contains(errw.String(), c.want) || w.closed() {
			t.Errorf("%s: %d %q", c.name, code, errw.String())
		}
	}
	// A boot role, and a pop in another episode, are refused before anything.
	w, d, _, _ := newPopWorld(t)
	var out, errw bytes.Buffer
	if code := runRetire(ctx, w.b, d, "master", []string{"foureyes", "--forget", "--episode", "5"}, &out, &errw); code != 1 ||
		!strings.Contains(errw.String(), "not a pop") || w.closed() {
		t.Fatalf("boot role: %d %q", code, errw.String())
	}
	if code, msg := w.retireForget(d, "5"); code != 1 || !strings.Contains(msg, "another episode") || w.closed() {
		t.Fatalf("another episode: %d %q", code, msg)
	}
	if log, _ := w.b.SwapLog(ctx, "security", 0); len(log) != 0 {
		t.Fatalf("a refused retire wrote the ledger: %+v", log)
	}
}

// Staleness (foureyes, reviewer-codex): what arrives after the dispatcher's
// judgement is read at each check (1 before the begin, 2 after it, 3 right
// before the close); before the begin nothing is written,
// after it a failed entry, and nothing is closed either way.
func TestRetireForgetReJudgesAtEachCheck(t *testing.T) {
	ctx := context.Background()
	changes := map[string]func(w *swapWorld, rt *popRT){
		"a directive": func(w *swapWorld, _ *popRT) { w.b.Cmd(ctx, "master", "security", "directive", "", "one more") },
		"a board task": func(w *swapWorld, _ *popRT) {
			w.r.HSet(ctx, bus.BoardKey(w.b.Project()), "t9", `{"owner":"security","state":"accepted"}`)
		},
		"an open ask": func(w *swapWorld, _ *popRT) {
			w.b.Ask(ctx, bus.AskDef{From: "security", Title: "T", Questions: []bus.AskQuestion{
				{ID: "a", Header: "A", Question: "A?", Options: []bus.AskOption{{Label: "yes"}, {Label: "no"}}}}}, "")
		},
		"the focus": func(_ *swapWorld, rt *popRT) { rt.focused = true },
		"a pause":   func(w *swapWorld, _ *popRT) { w.b.SetPause(ctx, "master", "master", "test", time.Now().Add(time.Hour)) },
		"a hold":    func(w *swapWorld, _ *popRT) { w.r.HSet(ctx, bus.QuotaKey(w.b.Project()), "hold:openai", "x") },
		"a new idle": func(w *swapWorld, _ *popRT) {
			time.Sleep(2 * time.Millisecond) // a declaration in another millisecond: another episode
			w.b.Status(ctx, "security", "idle", "again", bus.AgentIdent{})
		},
		"a reload intent": func(w *swapWorld, _ *popRT) {},
	}
	for name, change := range changes {
		for _, at := range []int{1, 2, 3} {
			if name == "a reload intent" && at == 1 {
				continue // check 1 refuses any intent; covered by the PopDone table
			}
			w, d, rt, _ := newPopWorld(t)
			episode := w.episode(t)
			apply := func() {
				if name == "a reload intent" {
					w.r.Set(ctx, bus.SwapKey(w.b.Project(), "security"), `{"kind":"reload"}`, 0)
					return
				}
				change(w, rt)
			}
			// A check reads the bus, then inspects the pane: a change made at
			// the (at-1)-th inspection is what check at reads first; the focus
			// is read by the inspection itself.
			trigger := at - 1
			if name == "the focus" {
				trigger = at
			}
			if trigger == 0 {
				apply()
			} else {
				rt.onInspect = func(n int) {
					if n == trigger {
						apply()
					}
				}
			}
			code, out := w.retireForget(d, episode)
			if code == 0 || w.closed() || !w.registered(t, "security") {
				t.Errorf("%s at check %d: code %d closed=%v: %s", name, at, code, w.closed(), out)
				continue
			}
			log, _ := w.b.SwapLog(ctx, "security", 0)
			// Another intent before the begin: the begin itself is refused
			// (SET NX), nothing written.
			before := at == 1 || name == "a reload intent" && at == 2
			switch {
			case before && len(log) != 0:
				t.Errorf("%s at check 1 wrote the ledger: %+v", name, log)
			case !before && (len(log) == 0 || log[0].Event != bus.SwapFailed || !strings.Contains(log[0].Error, "popdone")):
				t.Errorf("%s at check %d: ledger %+v", name, at, log)
			}
		}
	}
}

// A directive sent between the kill and the forget keeps the registration
// and the group, and the entry waits for the next spawn (foureyes).
func TestRetireForgetKeepsWhatArrivesAfterTheKill(t *testing.T) {
	w, d, _, afterClose := newPopWorld(t)
	ctx := context.Background()
	var sent string
	*afterClose = func() { sent, _ = w.b.Cmd(ctx, "master", "security", "directive", "", "late work") }
	code, out := w.retireForget(d, w.episode(t))
	if code != 1 || !w.closed() || !strings.Contains(out, sent) || !strings.Contains(out, "registration is kept") {
		t.Fatalf("code %d closed=%v: %s", code, w.closed(), out)
	}
	if !w.registered(t, "security") {
		t.Fatal("the registration went")
	}
	log, _ := w.b.SwapLog(ctx, "security", 0)
	if len(log) == 0 || log[0].Event != bus.SwapRetire || log[0].Forgot != "false" {
		t.Fatalf("ledger %+v", log)
	}
	var got []string
	if _, err := w.b.DrainCmd(ctx, "security", "security", bus.DefaultHorizon, func(e bus.Event) error { got = append(got, e.Message); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "late work" {
		t.Fatalf("the kept group gave %q", got)
	}
}

// A respawn between the close and the forget (reviewer-codex): the new
// process's registration is kept whole, and retire says so.
func TestRetireForgetKeepsARespawn(t *testing.T) {
	w, d, _, afterClose := newPopWorld(t)
	ctx := context.Background()
	*afterClose = func() {
		w.mu.Lock()
		w.procs[7171] = 71
		w.mu.Unlock()
		w.b.Status(ctx, "security", "idle", "respawned", bus.AgentIdent{Pane: "p-sec2", PID: 7171, PIDStart: 71, Tier: "pop"})
	}
	code, out := w.retireForget(d, w.episode(t))
	if code != 1 || !strings.Contains(out, "another process") {
		t.Fatalf("code %d: %s", code, out)
	}
	snaps, _ := w.b.Agents(ctx)
	if s := snaps["security"]; s.PID != 7171 || s.Pane != "p-sec2" {
		t.Fatalf("the respawn's registration = %+v", s)
	}
}
