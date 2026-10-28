package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/clientfp"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/redis/go-redis/v9"
)

const reloadManifest = `schema_version = 2
[defaults]
profile = "opus"
[profiles.opus]
client = "claude-code"
provider = "anthropic"
model = "opus"
options = { permission = "manual" }
[profiles.review]
client = "codex"
provider = "openai"
model = "codex-model"
[roles.master]
tier = "boot"
[roles.sentinel]
tier = "boot"
[roles.coder]
tier = "boot"
fallback_profiles = ["review"]
[roles.foureyes]
tier = "boot"
profile = "review"
`

const coderSession = "0c9e2a51-7d4b-4f0e-9a51-2c3d4e5f6a7b"

// installedFP is the client installed now, the one an update episode names;
// a relaunch through the launcher records it as the new process's client_fp.
var installedFP = clientfp.Fingerprint{Path: "/bin/claude", Target: clientfp.FileFP{Path: "/opt/claude/2.1", Ino: 2}}

// reloadWorld is a fake herdr session for reload (lifecycle spec, part C):
// processes by pid, a SIGTERM that ends one unless told otherwise, and a
// relaunch that binds a new process in a new pane, as the launcher does.
type reloadWorld struct {
	t        *testing.T
	b        *bus.Bus
	r        *redis.Client
	mu       sync.Mutex
	procs    map[int]uint64
	calls    []string
	env      map[string]string
	ignore   bool   // the client ignores SIGTERM
	launched string // client_fp the relaunch binds (default installedFP)
	relaunch error  // returned by the relaunch
	noProof  bool   // the relaunch starts nothing
	check    error  // returned by the pre-kill relaunch check
}

func newReloadWorld(t *testing.T) (*reloadWorld, reloadDeps) {
	t.Helper()
	b, r := dialMain(t)
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "roles.toml")
	if err := os.WriteFile(path, []byte(reloadManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &reloadWorld{t: t, b: b, r: r, procs: map[int]uint64{4242: 7, 6060: 3}, env: map[string]string{}, launched: installedFP.Encode()}
	if err := b.MarkServicesInstalled(ctx, bus.ServicesInstall{}); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(ctx, "coder", bus.AgentIdent{Pane: "p-old", PID: 4242, PIDStart: 7, Client: "claude-code", Provider: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	if err := b.ProveSession(ctx, "coder", 4242, 7, coderSession, 100); err != nil {
		t.Fatal(err)
	}
	w.status("coder", "idle")
	if err := b.Bind(ctx, "foureyes", bus.AgentIdent{Pane: "p-four", PID: 6060, PIDStart: 3, Client: "codex", Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Status(ctx, "foureyes", "idle", "", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	w.update("coder", "foureyes")
	d := reloadDeps{
		Env:      func(k string) string { w.mu.Lock(); defer w.mu.Unlock(); return w.env[k] },
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
		Terminate: func(pid int) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.calls = append(w.calls, "term "+itoa(pid))
			if !w.ignore {
				delete(w.procs, pid)
			}
			return nil
		},
		CheckRelaunch: func(context.Context, string) error { w.mu.Lock(); defer w.mu.Unlock(); return w.check },
		Relaunch: func(_ context.Context, role, session string) error {
			w.mu.Lock()
			w.calls = append(w.calls, "relaunch "+role+" "+session)
			fail, none, fp := w.relaunch, w.noProof, w.launched
			if fail == nil && !none {
				w.procs[5151] = 9
			}
			w.mu.Unlock()
			if fail != nil || none {
				return fail
			}
			client, provider := "claude-code", "anthropic"
			if role == "foureyes" { // its own profile is review (codex)
				client, provider = "codex", "openai"
			}
			return b.Bind(context.Background(), role, bus.AgentIdent{Pane: "p-new", PID: 5151, PIDStart: 9, Client: client, Provider: provider, ClientFP: fp})
		},
		Now: time.Now, Sleep: func(time.Duration) { time.Sleep(5 * time.Millisecond) },
		PIDGone: 200 * time.Millisecond, Proof: 200 * time.Millisecond, Recent: 2 * time.Minute,
	}
	return w, d
}

func (w *reloadWorld) status(agent, state string) {
	w.t.Helper()
	if _, err := w.b.Status(context.Background(), agent, state, "", bus.AgentIdent{}); err != nil {
		w.t.Fatal(err)
	}
}

// update marks agents' clients as outdated in {p}:observed, as the observe
// pass does on a client-update episode.
func (w *reloadWorld) update(agents ...string) {
	w.t.Helper()
	ctx := context.Background()
	if alive, _ := w.b.DispatchAlive(ctx); !alive {
		if ok, err := w.b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
			w.t.Fatalf("AcquireDispatch = %v, %v", ok, err)
		}
	}
	obs := map[string]bus.Observation{}
	for _, a := range agents {
		obs[a] = bus.Observation{At: time.Now().UnixMilli(), Update: &bus.ClientUpdate{Hash: installedFP.Hash(), Source: "fingerprint"}}
	}
	if err := w.b.FencedPutObserved(ctx, "tok", obs); err != nil {
		w.t.Fatal(err)
	}
}

func (w *reloadWorld) run(d reloadDeps, self string, args ...string) (int, string) {
	var out, errw bytes.Buffer
	code := runReload(context.Background(), w.b, d, self, args, &out, &errw)
	return code, out.String() + errw.String()
}

func (w *reloadWorld) callList() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.calls, ";")
}

func (w *reloadWorld) ledger(role string) []bus.SwapEvent {
	log, _ := w.b.SwapLog(context.Background(), role, 0)
	return log
}

func (w *reloadWorld) intent(role string) bool {
	live, _ := w.b.Swapping(context.Background(), role)
	return live
}

// Sentinel reloads an outdated agent: SIGTERM to the verified pid, the role's
// relaunch resuming its proved session, the proof, the ledger.
func TestReloadRelaunchesTheAgentResumed(t *testing.T) {
	w, d := newReloadWorld(t)
	code, out := w.run(d, "sentinel", "coder", "--reason", "client update")
	if code != 0 {
		t.Fatalf("reload: %d %s", code, out)
	}
	if got := w.callList(); got != "term 4242;relaunch coder "+coderSession {
		t.Fatalf("calls %q", got)
	}
	log := w.ledger("coder")
	if len(log) != 2 || log[0].Event != bus.SwapReloaded || log[0].NewPID != 5151 || log[0].NewPane != "p-new" ||
		log[1].Event != bus.SwapBegin || log[1].Kind != bus.SwapKindReload || log[1].Reason != "client update" || log[1].By != "sentinel" {
		t.Fatalf("ledger %+v", log)
	}
	if w.intent("coder") || !strings.Contains(out, "reloaded coder") || !strings.Contains(out, "p-new") {
		t.Fatalf("intent left %v, out %q", w.intent("coder"), out)
	}
}

// Every preflight refusal changes nothing: no signal, no relaunch, no ledger
// entry, no intent.
func TestReloadPreflightRefusesWithoutChangingAnything(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		self string
		args []string
		set  func(w *reloadWorld)
		want string
	}{
		"master reloads a peer":       {"master", []string{"coder"}, nil, "master may reload only sentinel"},
		"a peer reloads a peer":       {"foureyes", []string{"coder"}, nil, "may not reload"},
		"sentinel reloads itself":     {"sentinel", []string{"sentinel"}, nil, "itself"},
		"no update for it":            {"sentinel", []string{"coder"}, func(w *reloadWorld) { w.update("foureyes") }, "update"},
		"reloads are off":             {"sentinel", []string{"coder"}, func(w *reloadWorld) { w.env["AGENT_BUS_RELOAD"] = "off" }, "AGENT_BUS_RELOAD"},
		"reloads are proposed":        {"sentinel", []string{"coder"}, func(w *reloadWorld) { w.env["AGENT_BUS_RELOAD"] = "propose" }, "propose"},
		"master under propose":        {"master", []string{"sentinel"}, func(w *reloadWorld) { reloadableSentinel(w); w.env["AGENT_BUS_RELOAD"] = "propose" }, "propose"},
		"unknown role":                {"sentinel", []string{"ghost"}, nil, "unknown role"},
		"its own pane":                {"sentinel", []string{"coder"}, func(w *reloadWorld) { w.env["HERDR_PANE_ID"] = "p-old" }, "own pane"},
		"its process is gone":         {"sentinel", []string{"coder"}, func(w *reloadWorld) { delete(w.procs, 4242) }, "not running"},
		"on a fallback":               {"sentinel", []string{"coder"}, onFallback, "swap coder --back"},
		"working":                     {"sentinel", []string{"coder"}, func(w *reloadWorld) { w.status("coder", "working") }, "working"},
		"blocked":                     {"sentinel", []string{"coder"}, func(w *reloadWorld) { w.status("coder", "blocked") }, "idle"},
		"an accepted task it owns":    {"sentinel", []string{"coder"}, acceptedTask, "lifecycle-9"},
		"an open challenge":           {"sentinel", []string{"coder"}, openChallenge, "challenge"},
		"an open ask, fresh":          {"sentinel", []string{"coder", "--fresh"}, openAsk, "ask"},
		"no relaunch path":            {"sentinel", []string{"coder"}, func(w *reloadWorld) { w.check = errors.New("not inside herdr") }, "not inside herdr"},
		"no session to resume":        {"sentinel", []string{"foureyes"}, nil, "--fresh"},
		"no pilot for master":         {"sentinel", []string{"master"}, nil, "verified pane"},
		"master while another pilots": {"sentinel", []string{"master"}, masterPilotedByOther, "pilot"},
	} {
		t.Run(name, func(t *testing.T) {
			w, d := newReloadWorld(t)
			if c.set != nil {
				c.set(w)
			}
			code, out := w.run(d, c.self, c.args...)
			if code == 0 || !strings.Contains(out, c.want) {
				t.Fatalf("exit %d, out %q (want %q)", code, out, c.want)
			}
			if w.callList() != "" || w.intent(c.args[0]) {
				t.Fatalf("calls %q, intent %v", w.callList(), w.intent(c.args[0]))
			}
			if log := w.ledger(c.args[0]); len(log) != 0 {
				t.Fatalf("ledger %+v", log)
			}
			_ = ctx
		})
	}
}

func onFallback(w *reloadWorld) {
	fb := &bus.FallbackRecord{OwnProfile: "opus", OwnProvider: "anthropic", Profile: "review", Provider: "openai"}
	if err := w.b.FinishSwap(context.Background(), bus.SwapEvent{Role: "coder", ToProfile: "review"}, fb); err != nil {
		w.t.Fatal(err)
	}
	// FinishSwap logged a done entry; the test counts the reload's own entries only.
	w.r.Del(context.Background(), bus.SwapsKey(w.b.Project()))
}

func acceptedTask(w *reloadWorld) {
	ctx := context.Background()
	if err := w.b.BoardClaim(ctx, "lifecycle-9", "coder", "feat/x"); err != nil {
		w.t.Fatal(err)
	}
	if err := w.b.BoardState(ctx, "lifecycle-9", "accepted"); err != nil {
		w.t.Fatal(err)
	}
}

func openChallenge(w *reloadWorld) {
	if err := w.b.OpenChallenge(context.Background(), "coder", "ref-1", "foureyes|review x"); err != nil {
		w.t.Fatal(err)
	}
}

func openAsk(w *reloadWorld) {
	def := bus.AskDef{From: "coder", Title: "which?", Questions: []bus.AskQuestion{{ID: "q1", Header: "Pick", Question: "Which one?",
		Options: []bus.AskOption{{Label: "a"}, {Label: "b"}}}}}
	if _, _, err := w.b.Ask(context.Background(), def, ""); err != nil {
		w.t.Fatal(err)
	}
}

func masterPilotedByOther(w *reloadWorld) {
	ctx := context.Background()
	if err := w.b.Bind(ctx, "master", bus.AgentIdent{Pane: "p-master", PID: 7070, PIDStart: 4, Client: "claude-code", Provider: "anthropic"}); err != nil {
		w.t.Fatal(err)
	}
	w.procs[7070] = 4
	w.status("master", "idle")
	w.update("master")
	if err := w.b.Pilot(ctx, "bernard", time.Minute); err != nil {
		w.t.Fatal(err)
	}
}

// Master may reload sentinel, and a person anyone, by an explicit --as.
func TestReloadCallerTable(t *testing.T) {
	w, d := newReloadWorld(t)
	if code, out := w.run(d, "coder", "foureyes", "--as", "bernard", "--fresh"); code != 0 {
		t.Fatalf("a person's reload: %d %s", code, out)
	}
	if got := w.callList(); got != "term 6060;relaunch foureyes " {
		t.Fatalf("fresh relaunch calls %q", got)
	}
	if code, out := w.run(d, "master", "coder", "--as", "Not A Name"); code == 0 || !strings.Contains(out, "--as") {
		t.Fatalf("an invalid person: %d %s", code, out)
	}
}

// A client that ignores SIGTERM is a failure, never a SIGKILL; a failed
// relaunch and a missing proof are failures with their recovery.
func TestReloadFailuresAreLoggedWithTheirRecovery(t *testing.T) {
	for name, c := range map[string]struct {
		set       func(w *reloadWorld)
		calls     string
		step, say string
	}{
		"SIGTERM ignored": {func(w *reloadWorld) { w.ignore = true }, "term 4242", "stop", "did not exit"},
		"relaunch failed": {func(w *reloadWorld) { w.relaunch = errors.New("split refused") }, "term 4242;relaunch coder " + coderSession, "relaunch",
			"agent-launch coder"},
		"no proof": {func(w *reloadWorld) { w.noProof = true }, "term 4242;relaunch coder " + coderSession, "proof", "agentbus agents"},
		"still the old client": {func(w *reloadWorld) {
			w.launched = clientfp.Fingerprint{Path: "/bin/claude", Target: clientfp.FileFP{Path: "/opt/claude/2.0", Ino: 1}}.Encode()
		}, "term 4242;relaunch coder " + coderSession, "proof", "installed"},
	} {
		t.Run(name, func(t *testing.T) {
			w, d := newReloadWorld(t)
			c.set(w)
			code, out := w.run(d, "sentinel", "coder")
			if code == 0 || !strings.Contains(out, c.say) {
				t.Fatalf("exit %d, out %q", code, out)
			}
			if got := w.callList(); got != c.calls {
				t.Fatalf("calls %q, want %q", got, c.calls)
			}
			log := w.ledger("coder")
			if len(log) != 2 || log[0].Event != bus.SwapFailed || log[0].Kind != bus.SwapKindReload || !strings.HasPrefix(log[0].Error, c.step+":") {
				t.Fatalf("ledger %+v", log)
			}
			if w.intent("coder") {
				t.Fatal("intent left behind")
			}
		})
	}
}

// --dry-run checks everything and writes nothing.
func TestReloadDryRunWritesNothing(t *testing.T) {
	w, d := newReloadWorld(t)
	code, out := w.run(d, "sentinel", "coder", "--dry-run")
	if code != 0 || !strings.Contains(out, "would reload coder") || !strings.Contains(out, coderSession) {
		t.Fatalf("dry run: %d %q", code, out)
	}
	if w.callList() != "" || len(w.ledger("coder")) != 0 || w.intent("coder") {
		t.Fatalf("a dry run changed something: %q %+v", w.callList(), w.ledger("coder"))
	}
}

// Work that opens between the first check and the intent is caught after the
// intent: the reload fails before any signal.
func TestReloadRechecksOpenWorkAfterTheIntent(t *testing.T) {
	w, d := newReloadWorld(t)
	d.AfterIntent = func() { acceptedTask(w) }
	code, out := w.run(d, "sentinel", "coder")
	if code == 0 || !strings.Contains(out, "lifecycle-9") {
		t.Fatalf("exit %d, out %q", code, out)
	}
	if w.callList() != "" || w.intent("coder") {
		t.Fatalf("calls %q, intent %v", w.callList(), w.intent("coder"))
	}
	if log := w.ledger("coder"); len(log) != 2 || log[0].Event != bus.SwapFailed || log[0].Kind != bus.SwapKindReload {
		t.Fatalf("ledger %+v", log)
	}
}

// From a pane without AGENT_BUS_AGENT, a person may reload with an explicit
// --as (as with answer --as); without it the guard refuses, like any write.
func TestReloadPassesTheIdentityGuardOnlyWithAs(t *testing.T) {
	get := func(k string) string { return map[string]string{"HERDR_PANE_ID": "w1:p9"}[k] }
	if err := identityGuard(get, "reload", []string{"coder", "--as", "bernard"}); err != nil {
		t.Fatalf("reload --as refused: %v", err)
	}
	if err := identityGuard(get, "reload", []string{"coder"}); err == nil {
		t.Fatal("reload without --as passed the guard")
	}
}

// reloadableSentinel gives sentinel a verified, idle identity with a proved
// session: everything a reload needs.
func reloadableSentinel(w *reloadWorld) {
	ctx := context.Background()
	w.procs[8080] = 5
	if err := w.b.Bind(ctx, "sentinel", bus.AgentIdent{Pane: "p-sent", PID: 8080, PIDStart: 5, Client: "claude-code", Provider: "anthropic"}); err != nil {
		w.t.Fatal(err)
	}
	if err := w.b.ProveSession(ctx, "sentinel", 8080, 5, "5e5e5e5e-0000-4000-8000-000000000001", 100); err != nil {
		w.t.Fatal(err)
	}
	w.status("sentinel", "idle")
}

// Master reloads sentinel by default (no update episode needed).
func TestReloadMasterReloadsSentinel(t *testing.T) {
	w, d := newReloadWorld(t)
	reloadableSentinel(w)
	if code, out := w.run(d, "master", "sentinel"); code != 0 {
		t.Fatalf("reload: %d %s", code, out)
	}
	if got := w.callList(); got != "term 8080;relaunch sentinel 5e5e5e5e-0000-4000-8000-000000000001" {
		t.Fatalf("calls %q", got)
	}
}

// Between the preflight and the stop, the process the reload checked may be
// replaced, recycled, or start a turn: nothing is signalled, and the stop
// (or readiness) step is a failed entry (foureyes' review of 3b).
func TestReloadSignalsOnlyTheCheckedIdleProcess(t *testing.T) {
	for name, c := range map[string]struct {
		change func(w *reloadWorld)
		step   string
	}{
		"the snapshot names a new process": {func(w *reloadWorld) {
			w.procs[4343] = 8
			if err := w.b.Bind(context.Background(), "coder", bus.AgentIdent{Pane: "p-old", PID: 4343, PIDStart: 8, Client: "claude-code", Provider: "anthropic"}); err != nil {
				w.t.Fatal(err)
			}
		}, "stop"},
		"the pid was recycled": {func(w *reloadWorld) { w.procs[4242] = 99 }, "stop"},
		"it declared working":  {func(w *reloadWorld) { w.status("coder", "working") }, "not idle"},
		"the observation aged out": {func(w *reloadWorld) {
			w.r.Del(context.Background(), bus.ObservedKey(w.b.Project())) // seen fresh at preflight, gone at the stop
		}, "not idle"},
		"the observation is unreadable": {func(w *reloadWorld) {
			w.r.Set(context.Background(), bus.ObservedKey(w.b.Project()), "not a hash", 0) // WRONGTYPE on read
		}, "not idle"},
	} {
		t.Run(name, func(t *testing.T) {
			w, d := newReloadWorld(t)
			d.AfterIntent = func() { c.change(w) }
			code, out := w.run(d, "sentinel", "coder")
			if code == 0 || w.callList() != "" {
				t.Fatalf("exit %d, calls %q, out %q", code, w.callList(), out)
			}
			log := w.ledger("coder")
			if len(log) != 2 || log[0].Event != bus.SwapFailed || !strings.HasPrefix(log[0].Error, c.step) {
				t.Fatalf("ledger %+v", log)
			}
		})
	}
}

// reload's --as has no short spelling (foureyes' review of reload-deny):
// expandArgs would turn it into --as after the deny rules matched the text,
// so the only spellings are the ones the shared deny names.
func TestReloadAsHasNoShortFlag(t *testing.T) {
	for _, c := range commands {
		if c.Name != "reload" {
			continue
		}
		for _, f := range c.Flags {
			if f.Long == "as" && f.Short != 0 {
				t.Fatalf("reload --as has the short -%c", f.Short)
			}
		}
	}
	for _, args := range [][]string{{"reload", "coder", "-a", "bernard"}, {"reload", "-a", "bernard", "coder"}} {
		if got := expandArgs(args); slices.Contains(got, "--as") {
			t.Fatalf("expandArgs(%q) = %q: a spelling the deny does not name", args, got)
		}
	}
}

// reload-fix-2: everything the relaunch needs is checked before the kill. A
// relaunch that cannot run (outside herdr, no tab and no pop path) refuses
// the reload with nothing signalled, the dry run included.
func TestReloadChecksTheRelaunchBeforeSigterm(t *testing.T) {
	for _, args := range [][]string{{"coder"}, {"coder", "--dry-run"}} {
		w, d := newReloadWorld(t)
		w.check = errors.New("not inside herdr: run it in the project's herdr session")
		code, out := w.run(d, "sentinel", args...)
		if code == 0 || !strings.Contains(out, "not inside herdr") || !strings.Contains(out, "nothing was signalled") {
			t.Fatalf("%v: exit %d, out %q", args, code, out)
		}
		if w.callList() != "" || w.intent("coder") || len(w.ledger("coder")) != 0 {
			t.Fatalf("%v: calls %q, intent %v, ledger %+v", args, w.callList(), w.intent("coder"), w.ledger("coder"))
		}
	}
}

// An agent's own open ask waits on the person: a resumed session keeps the
// conversation and the ask lives in Redis, so a resume reload goes ahead. A
// fresh one would lose the context the ask was put in, so it is refused.
func TestReloadResumesWithItsOwnOpenAsk(t *testing.T) {
	w, d := newReloadWorld(t)
	openAsk(w)
	if code, out := w.run(d, "sentinel", "coder"); code != 0 || !strings.Contains(out, "reloaded coder") {
		t.Fatalf("resume reload with its own ask: %d %s", code, out)
	}
	w, d = newReloadWorld(t)
	openAsk(w)
	if code, out := w.run(d, "sentinel", "coder", "--fresh"); code == 0 || !strings.Contains(out, "ask") {
		t.Fatalf("fresh reload with its own ask: %d %s", code, out)
	}
}
