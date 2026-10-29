package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/redis/go-redis/v9"
)

const swapManifest = `schema_version = 2
[defaults]
profile = "review"
[profiles.review]
client = "codex"
provider = "openai"
model = "codex-model"
[profiles.opus]
client = "claude-code"
provider = "anthropic"
model = "opus"
options = { permission = "manual" }
[profiles.k]
client = "kimi-cli"
provider = "moonshot"
model = "alias"
[roles.foureyes]
tier = "boot"
skills = []
fallback_profiles = ["opus"]
`

// swapWorld is a fake herdr session: processes by pid, one pane per agent,
// and a spawn that binds the new process like the launcher does.
type swapWorld struct {
	t       *testing.T
	b       *bus.Bus
	r       *redis.Client
	mu      sync.Mutex
	procs   map[int]uint64
	calls   []string
	spawnFn func(role, profile string) error
	paneEnv string
}

func newSwapWorld(t *testing.T) (*swapWorld, swapDeps) {
	t.Helper()
	b, r := dialMain(t)
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	path := filepath.Join(t.TempDir(), "roles.toml")
	if err := os.WriteFile(path, []byte(swapManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &swapWorld{t: t, b: b, r: r, procs: map[int]uint64{4242: 7}}
	if _, err := b.Status(context.Background(), "foureyes", "idle", "",
		bus.AgentIdent{Pane: "p-old", PID: 4242, PIDStart: 7, Client: "codex", Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	// The default spawn: a new process in a new pane, bound on the chosen profile.
	w.spawnFn = func(role, profile string) error {
		w.mu.Lock()
		w.procs[5151] = 9
		w.mu.Unlock()
		client, provider := "claude-code", "anthropic"
		if profile == "review" {
			client, provider = "codex", "openai"
		}
		_, err := b.Status(context.Background(), role, "idle", "", bus.AgentIdent{Pane: "p-new", PID: 5151, PIDStart: 9, Client: client, Provider: provider})
		return err
	}
	d := swapDeps{
		Env:      func(k string) string { return map[string]string{"HERDR_PANE_ID": w.paneEnv}[k] },
		Manifest: func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) },
		LookPath: func(s string) (string, error) { return "/bin/" + s, nil },
		ProcStart: func(pid int) (uint64, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			if s, ok := w.procs[pid]; ok {
				return s, nil
			}
			return 0, errors.New("no such process")
		},
		ClosePane: func(_ context.Context, pane string) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.calls = append(w.calls, "close "+pane)
			if pane == "p-old" {
				delete(w.procs, 4242)
			}
			return nil
		},
		Spawn: func(_ context.Context, role, _ string, profile string) error {
			w.mu.Lock()
			w.calls = append(w.calls, "spawn "+role+" "+profile)
			w.mu.Unlock()
			return w.spawnFn(role, profile)
		},
		Now: time.Now, Sleep: func(time.Duration) { time.Sleep(5 * time.Millisecond) },
		PIDGone: 300 * time.Millisecond, Proof: 300 * time.Millisecond, Recent: 2 * time.Minute,
	}
	return w, d
}

func (w *swapWorld) swap(d swapDeps, args ...string) (int, string) {
	var out, errw bytes.Buffer
	code := runSwap(context.Background(), w.b, d, "master", args, &out, &errw)
	return code, out.String() + errw.String()
}

func (w *swapWorld) closed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.calls {
		if strings.HasPrefix(c, "close") {
			return true
		}
	}
	return false
}

func (w *swapWorld) intent() bool {
	ok, err := w.b.Swapping(context.Background(), "foureyes")
	if err != nil {
		w.t.Fatal(err)
	}
	return ok
}

// The whole swap: close, wait for the pid, spawn, proof, intent gone, and the
// accepted task resumed by a directive sent after the proof.
func TestSwapReplacesTheAgentAndProvesIt(t *testing.T) {
	w, d := newSwapWorld(t)
	ctx := context.Background()
	if err := w.r.HSet(ctx, bus.BoardKey(w.b.Project()), "plan-review", `{"owner":"foureyes","state":"accepted"}`).Err(); err != nil {
		t.Fatal(err)
	}
	code, out := w.swap(d, "foureyes", "--profile", "opus")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "swapped foureyes: review -> opus (anthropic, opus), pane p-new, pid 5151") {
		t.Fatalf("output %q", out)
	}
	if got := strings.Join(w.calls, ";"); got != "close p-old;spawn foureyes opus" {
		t.Fatalf("calls %q", got)
	}
	if w.intent() {
		t.Fatal("the swap intent outlived the swap")
	}
	evs, _, _ := w.b.Recent(ctx, []string{"cmd"}, 20)
	found := false
	for _, e := range evs {
		if e.Target == "foureyes" && strings.Contains(e.Message, "plan-review") {
			found = true
		}
	}
	if !found {
		t.Fatal("no resume directive for the accepted task")
	}
}

// Every refusal of §6.1 changes nothing: no close, no intent.
func TestSwapRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(w *swapWorld)
		args  []string
		want  string
	}{
		{"not a fallback", nil, []string{"foureyes", "--profile", "k"}, "neither the role's profile"},
		{"unknown role", nil, []string{"ghost", "--profile", "opus"}, "unknown role"},
		{"swap itself", func(w *swapWorld) { w.paneEnv = "p-old" }, []string{"foureyes", "--profile", "opus"}, "cannot swap itself"},
		{"dead process", func(w *swapWorld) { delete(w.procs, 4242) }, []string{"foureyes", "--profile", "opus"}, "not running any more"},
		{"gated author", func(w *swapWorld) {
			_ = w.b.OpenChallenge(context.Background(), "foureyes", "pr-9", "coder|review my fix")
		}, []string{"foureyes", "--profile", "opus"}, "challenge pr-9 gates foureyes"},
		{"challenger", func(w *swapWorld) {
			_ = w.b.OpenChallenge(context.Background(), "coder", "pr-7", "foureyes|please fix")
		}, []string{"foureyes", "--profile", "opus"}, "challenge pr-7 opened by foureyes on coder"},
		{"declared working", func(w *swapWorld) {
			_, _ = w.b.Status(context.Background(), "foureyes", "working", "reviewing", bus.AgentIdent{})
		}, []string{"foureyes", "--profile", "opus"}, "declared working"},
		{"swap in progress", func(w *swapWorld) {
			_ = w.b.BeginSwap(context.Background(), "foureyes", bus.SwapIntent{ToProfile: "opus", By: "charlie"})
		}, []string{"foureyes", "--profile", "opus"}, "already in progress"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, d := newSwapWorld(t)
			if c.setup != nil {
				c.setup(w)
			}
			code, out := w.swap(d, c.args...)
			if code == 0 || !strings.Contains(out, c.want) {
				t.Fatalf("exit %d, %q: want a refusal containing %q", code, out, c.want)
			}
			if w.closed() {
				t.Fatal("a refused swap closed the pane")
			}
		})
	}
}

// --now takes a running turn; --force accepts a profile outside the list.
func TestSwapNowAndForce(t *testing.T) {
	w, d := newSwapWorld(t)
	_, _ = w.b.Status(context.Background(), "foureyes", "working", "reviewing", bus.AgentIdent{})
	if code, out := w.swap(d, "foureyes", "--profile", "opus", "--now"); code != 0 {
		t.Fatalf("--now: exit %d: %s", code, out)
	}
	w2, d2 := newSwapWorld(t)
	w2.spawnFn = func(role, profile string) error {
		w2.procs[5151] = 9
		_, err := w2.b.Status(context.Background(), role, "idle", "", bus.AgentIdent{Pane: "p-new", PID: 5151, PIDStart: 9, Client: "kimi-cli", Provider: "moonshot"})
		return err
	}
	code, out := w2.swap(d2, "foureyes", "--profile", "k", "--force")
	if code != 0 || !strings.Contains(out, "--force") {
		t.Fatalf("--force: exit %d: %s", code, out)
	}
}

// After the close, a failed launch or a missing proof leaves no intent and
// prints the recovery.
func TestSwapFailureAfterTheClose(t *testing.T) {
	for name, spawn := range map[string]func(string, string) error{
		"launch fails": func(string, string) error { return errors.New("agent-spawn: herdr refused") },
		"no proof":     func(string, string) error { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			w, d := newSwapWorld(t)
			w.spawnFn = spawn
			code, out := w.swap(d, "foureyes", "--profile", "opus")
			if code == 0 || !strings.Contains(out, "recover with: agent-spawn foureyes") {
				t.Fatalf("exit %d, %q", code, out)
			}
			if w.intent() {
				t.Fatal("the intent survived a failed swap")
			}
		})
	}
}

// Codex review: during a hold, the swap's resume shares the episode mark, so
// the dispatcher's release finds it set; the intent lasts until it is sent.
func TestSwapResumeSharesTheHoldMark(t *testing.T) {
	w, d := newSwapWorld(t)
	ctx := context.Background()
	_, _ = w.b.Status(ctx, "foureyes", "working", "reviewing", bus.AgentIdent{})
	w.r.HSet(ctx, bus.QuotaKey(w.b.Project()), "hold:openai", "1792500000")
	code, out := w.swap(d, "foureyes", "--profile", "opus", "--now")
	if code != 0 || !strings.Contains(out, "resume sent to foureyes") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if m, _ := w.b.QuotaMarks(ctx); m["resumed:openai:foureyes"] != "1792500000" {
		t.Fatalf("marks %v", m)
	}
}

// A wake record that cannot be read stops the swap before anything closes.
func TestSwapAbortsWhenTheWakeRecordIsUnreadable(t *testing.T) {
	w, d := newSwapWorld(t)
	w.r.Set(context.Background(), bus.WakeKey(w.b.Project(), "foureyes"), "not a hash", 0)
	code, out := w.swap(d, "foureyes", "--profile", "opus")
	if code == 0 || w.closed() || !strings.Contains(out, "nothing was replaced") {
		t.Fatalf("exit %d, closed %v: %s", code, w.closed(), out)
	}
}

// Codex review: the release published its resume just before the intent; it
// is still unread, so the new process gets it from the queue and the swap
// sends no second one.
func TestSwapDoesNotDoubleAQueuedReleaseResume(t *testing.T) {
	w, d := newSwapWorld(t)
	ctx := context.Background()
	_, _ = w.b.Status(ctx, "foureyes", "working", "reviewing", bus.AgentIdent{})
	w.r.HSet(ctx, bus.QuotaKey(w.b.Project()), "hold:openai", "1792500000")
	w.releaseResume(ctx, "1792500000")
	code, out := w.swap(d, "foureyes", "--profile", "opus", "--now")
	if code != 0 || !strings.Contains(out, "already queued") {
		t.Fatalf("exit %d: %s", code, out)
	}
	evs, _, _ := w.b.Recent(ctx, []string{"cmd"}, 50)
	for _, e := range evs {
		if e.From == "master" && e.Target == "foureyes" {
			t.Fatalf("a second resume was sent: %q", e.Message)
		}
	}
}

// The same, once the hold is gone and with the resume delivered but not
// acknowledged (the next read reclaims it).
func TestSwapSeesAPendingReleaseResumeAfterTheHold(t *testing.T) {
	w, d := newSwapWorld(t)
	ctx := context.Background()
	_, _ = w.b.Status(ctx, "foureyes", "working", "reviewing", bus.AgentIdent{})
	w.releaseResume(ctx, "1792500000")
	stream := bus.StreamKey(w.b.Project(), "cmd")
	if _, err := w.r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "foureyes", Consumer: "old", Streams: []string{stream, ">"}, Count: 10}).Result(); err != nil {
		t.Fatal(err)
	}
	code, out := w.swap(d, "foureyes", "--profile", "opus", "--now")
	if code != 0 || !strings.Contains(out, "already queued") {
		t.Fatalf("exit %d: %s", code, out)
	}
}

// releaseResume writes the dispatcher's resume of an episode as FencedCmdOnce
// does: the entry with its episode field, and the mark.
func (w *swapWorld) releaseResume(ctx context.Context, episode string) {
	w.t.Helper()
	mark := "resumed:openai:foureyes"
	if err := w.r.XAdd(ctx, &redis.XAddArgs{Stream: bus.StreamKey(w.b.Project(), "cmd"), Values: []any{"from", "dispatch",
		"target", "foureyes", "type", "directive", "ref", "", "command", "Your provider's limit (openai) has reset. Resume it.",
		"text_complete", "yes", "episode", mark + "=" + episode}}).Err(); err != nil {
		w.t.Fatal(err)
	}
	w.r.HSet(ctx, bus.QuotaKey(w.b.Project()), mark, episode)
}

// Codex review: an older episode's resume still unread does not stand for
// the current one: the swap sends its own.
func TestSwapIgnoresAnOlderEpisodesResume(t *testing.T) {
	w, d := newSwapWorld(t)
	ctx := context.Background()
	_, _ = w.b.Status(ctx, "foureyes", "working", "reviewing", bus.AgentIdent{})
	w.releaseResume(ctx, "1700000000")
	w.r.HSet(ctx, bus.QuotaKey(w.b.Project()), "hold:openai", "1792500000", "resumed:openai:foureyes", "")
	w.r.HDel(ctx, bus.QuotaKey(w.b.Project()), "resumed:openai:foureyes")
	code, out := w.swap(d, "foureyes", "--profile", "opus", "--now")
	if code != 0 || !strings.Contains(out, "resume sent to foureyes") {
		t.Fatalf("exit %d: %s", code, out)
	}
}

// v0.15.1: a swap leaves begin and done in the ledger and a fallback record;
// --back returns, writes back and clears it; once back on the same model,
// --back refuses; a model that drifted is relaunched.
func TestSwapLedgerFallbackAndBack(t *testing.T) {
	w, d := newSwapWorld(t)
	ctx := context.Background()
	w.r.HSet(ctx, bus.QuotaKey(w.b.Project()), "hold:openai", "1792500000")
	if code, out := w.swap(d, "foureyes", "--profile", "opus", "--reason", "ask 42: swap until monday"); code != 0 {
		t.Fatalf("swap: %d %s", code, out)
	}
	fbs, _ := w.b.Fallbacks(ctx)
	if r := fbs["foureyes"]; r.OwnProfile != "review" || r.Profile != "opus" || r.HeldEpisode != "1792500000" || r.Reason != "ask 42: swap until monday" {
		t.Fatalf("fallback = %+v", r)
	}
	// Back: the new process (pid 5151) is replaced by one on review.
	w.mu.Lock()
	w.procs = map[int]uint64{5151: 9}
	w.calls = nil
	w.mu.Unlock()
	w.spawnFn = func(role, profile string) error {
		w.mu.Lock()
		w.procs[6161] = 3
		w.mu.Unlock()
		_, err := w.b.Status(ctx, role, "idle", "", bus.AgentIdent{Pane: "p-back", PID: 6161, PIDStart: 3, Client: "codex", Provider: "openai", Model: "codex-model"})
		return err
	}
	d.ClosePane = func(_ context.Context, pane string) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.procs, 5151)
		return nil
	}
	if code, out := w.swap(d, "foureyes", "--back", "--now"); code != 0 {
		t.Fatalf("back: %d %s", code, out)
	}
	if fbs, _ := w.b.Fallbacks(ctx); len(fbs) != 0 {
		t.Fatalf("fallback kept after back: %+v", fbs)
	}
	log, _ := w.b.SwapLog(ctx, "foureyes", 0)
	var events []string
	for _, e := range log {
		events = append(events, e.Event)
	}
	if strings.Join(events, ",") != "back,begin,done,begin" {
		t.Fatalf("ledger = %v", events)
	}
	if code, out := w.swap(d, "foureyes", "--back"); code == 0 || !strings.Contains(out, "already on its own profile") {
		t.Fatalf("a second back: %d %s", code, out)
	}
	// The manifest moved on: the same profile, another model, is drift.
	_, _ = w.b.Status(ctx, "foureyes", "idle", "", bus.AgentIdent{Pane: "p-back", PID: 6161, PIDStart: 3, Client: "codex", Provider: "openai", Model: "codex-old"})
	w.spawnFn = func(role, profile string) error {
		w.mu.Lock()
		w.procs[7171] = 4
		w.mu.Unlock()
		_, err := w.b.Status(ctx, role, "idle", "", bus.AgentIdent{Pane: "p-new2", PID: 7171, PIDStart: 4, Client: "codex", Provider: "openai", Model: "codex-model"})
		return err
	}
	d.ClosePane = func(_ context.Context, pane string) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.procs, 6161)
		return nil
	}
	if code, out := w.swap(d, "foureyes", "--back"); code != 0 {
		t.Fatalf("drift relaunch: %d %s", code, out)
	}
}

// A failed swap writes failed; a begin left by a dead caller is closed as
// interrupted by the next swap.
func TestSwapLedgerFailureAndInterruption(t *testing.T) {
	w, d := newSwapWorld(t)
	ctx := context.Background()
	w.spawnFn = func(string, string) error { return errors.New("herdr refused") }
	if code, _ := w.swap(d, "foureyes", "--profile", "opus"); code == 0 {
		t.Fatal("a failed launch exited 0")
	}
	log, _ := w.b.SwapLog(ctx, "foureyes", 1)
	if len(log) != 1 || log[0].Event != bus.SwapFailed || !strings.HasPrefix(log[0].Error, "launch:") {
		t.Fatalf("ledger = %+v", log)
	}
	if err := w.b.BeginSwap(ctx, "coder", bus.SwapIntent{ToProfile: "opus", By: "master"}); err != nil {
		t.Fatal(err)
	}
	_ = w.b.EndSwap(ctx, "coder") // the caller died: begin without an end
	if bad, _, _ := w.b.Interrupted(ctx, "coder"); !bad {
		t.Fatal("not seen as interrupted")
	}
}

func TestSwapUsageBackAndProfileExclusive(t *testing.T) {
	w, d := newSwapWorld(t)
	for _, args := range [][]string{{"foureyes"}, {"foureyes", "--back", "--profile", "opus"}} {
		if code, _ := w.swap(d, args...); code != 2 {
			t.Fatalf("%v: exit %d, want 2", args, code)
		}
	}
}

// v0.15.1 §11.1: retire closes one agent's tab, launches nothing, logs a
// retire entry and drops its fallback; it refuses like swap does.
func TestRetireClosesOneAgent(t *testing.T) {
	w, d := newSwapWorld(t)
	ctx := context.Background()
	var out, errw bytes.Buffer
	if code := runRetire(ctx, w.b, d, "master", []string{"foureyes", "--reason", "recommendation delivered"}, &out, &errw); code != 0 {
		t.Fatalf("retire: %d %s", code, errw.String())
	}
	if got := strings.Join(w.calls, ";"); got != "close p-old" {
		t.Fatalf("calls %q (a retire must launch nothing)", got)
	}
	log, _ := w.b.SwapLog(ctx, "foureyes", 0)
	if len(log) != 2 || log[0].Event != bus.SwapRetire || log[1].Event != bus.SwapBegin || log[0].Reason != "recommendation delivered" {
		t.Fatalf("ledger %+v", log)
	}
	if w.intent() {
		t.Fatal("intent left behind")
	}
	out.Reset()
	if code := runSwaps(ctx, w.b, nil, &out, &errw); code != 0 || !strings.Contains(out.String(), "retire") {
		t.Fatalf("swaps: %d %q", code, out.String())
	}
	w2, d2 := newSwapWorld(t)
	w2.paneEnv = "p-old"
	if code := runRetire(ctx, w2.b, d2, "foureyes", []string{"foureyes"}, &out, &errw); code == 0 || w2.closed() {
		t.Fatal("an agent retired itself")
	}
}

// A reload whose caller died is closed by the next swap as a failed reload
// (lifecycle spec, part C), with its kind, and listed as one.
func TestSwapClosesAnInterruptedReloadAsAReload(t *testing.T) {
	w, d := newSwapWorld(t)
	ctx := context.Background()
	if err := w.b.BeginSwap(ctx, "foureyes", bus.SwapIntent{Kind: bus.SwapKindReload, By: "sentinel"}); err != nil {
		t.Fatal(err)
	}
	_ = w.b.EndSwap(ctx, "foureyes") // the reload's caller died
	if code, out := w.swap(d, "foureyes", "--profile", "opus"); code != 0 {
		t.Fatalf("swap: %d %s", code, out)
	}
	log, _ := w.b.SwapLog(ctx, "foureyes", 0)
	var closed *bus.SwapEvent
	for i := range log {
		if log[i].Event == bus.SwapFailed {
			closed = &log[i]
		}
	}
	if closed == nil || closed.Kind != bus.SwapKindReload || !strings.Contains(closed.Error, "reload") {
		t.Fatalf("ledger %+v", log)
	}
	var out, errw bytes.Buffer
	if code := runSwaps(ctx, w.b, nil, &out, &errw); code != 0 || !strings.Contains(out.String(), "[reload]") {
		t.Fatalf("swaps: %d %q", code, out.String())
	}
}
