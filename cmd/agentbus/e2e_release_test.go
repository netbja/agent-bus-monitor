package main

// The release's end-to-end test (P0-4, e2e-release-test). It gates the
// promotion dev -> main. What it covers, in one run on the disposable broker:
//
//  1. up of a project whose name is the longest a project takes (32 bytes),
//     on a release-shaped dirty build key: up's socket-fit check, its holder
//     step (install, start, the real readiness probe), the team started on the
//     holder through up's own path (openTeam), the services stubbed.
//  2. Each agent is a fake Claude Code (the test binary, AGENTBUS_E2E_FAKE):
//     it draws Claude's input box as the claude signature reads it (a rule,
//     `❯` + NBSP + a dim placeholder, a rule), binds its pid with the real
//     `bind` code, records its resume with the real bus proof, follows its
//     turns with the real turn hook, and at each turn's end writes Claude's
//     title, OSC 0 "✳ <project>:<agent>", right after `❯`: the bytes that
//     printed on the input line under x/vt (vt-osc-title).
//  3. The real dispatcher (dispatchsvc.Runtime: the holder, the client by the
//     terminal's profile) wakes coder twice in a row: each wake typed,
//     submitted, the turn run and ended, the message drained with the real
//     drain, so the second wake needs the screen ready again after a title.
//  4. coder's process is killed; the real self-repair (on, its production
//     wiring: serviceSelfRepair) relaunches it on the holder, resuming its
//     recorded session, proved by the new process's bind.
//
// The gate's command (it fails, never skips, without a broker):
//
//	AGENTBUS_RELEASE_GATE=1 AGENTBUS_TEST_REDIS_URL=<isolated disposable broker> \
//	  go test ./cmd/agentbus -run '^TestRelease(EndToEnd|UpdateFromPrevious)$' -count=1 -v
//
// Its output must hold a PASS line for both tests (TestReleaseUpdateFromPrevious,
// e2e_update_test.go: an update from the previous release still reads this
// tree's roles.toml).
//
// The turn hooks run as the fake client's children, as Claude Code runs
// them, so the bus proves them by their ancestors; the test sees coder
// working during each turn, then idle.
//
// What it cannot cover: a real Claude Code (its screen is a fake drawn from
// the claude signature and a captured screen; its hooks are the real turn
// hook run as its children, but fired by the fake, not by Claude Code's own
// hook events), the real launcher (the holder's launch is the
// test's, not TemplateLaunch running agent-launch), systemd or launchd (the
// services manager is a fake), docker and the production broker (never
// touched). The self-repair's grace (60 s, three reads) is passed on a
// shifted clock: its decision is the production one, its waiting is not.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/internal/dispatchsvc"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/lifecycle"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// The test binary doubles as the fake client the holder runs.
func TestMain(m *testing.M) {
	// No test reads the live cgroup tree or runs loginctl (roles doctor's host line).
	doctorHostLine = func(context.Context) string { return "cgroup mode: test" }
	switch os.Getenv("AGENTBUS_E2E_FAKE") {
	case "claude":
		e2eFakeClaude()
		return
	case "fakeclient": // agentbus fake-client, its argv in AGENTBUS_FAKE_ARGS (fakeclient_test.go)
		os.Exit(runFakeClient(strings.Split(os.Getenv("AGENTBUS_FAKE_ARGS"), "\x1f"), os.Stdin, os.Stdout, os.Stderr))
	case "turnhook": // a hook the client runs as its child, as Claude Code does
		turnHook([]string{os.Getenv("AGENTBUS_E2E_HOOK")}, os.Environ(), os.Stdin, io.Discard, func(ctx context.Context, project, agent, state string) error {
			err := realTurnFlip(ctx, project, agent, state)
			if err != nil {
				e2eLog("turn %s: %v", state, err)
			}
			return err
		}, func(string, string, string) {})
		return
	}
	os.Exit(m.Run())
}

// e2eRelease is a release-shaped build id: a tag description of a dirty
// tree, the shape whose key put the holder's socket over its limit.
const e2eRelease = "v0.30.0-23-g0123456789ab-dirty"

// e2eLog appends a line to the fake's log (AGENTBUS_E2E_LOG), which the test
// prints when it fails: the fake has no other voice.
func e2eLog(format string, a ...any) {
	if p := os.Getenv("AGENTBUS_E2E_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%s %s: %s\n", time.Now().Format("15:04:05.000"), os.Getenv("AGENT_BUS_AGENT"), fmt.Sprintf(format, a...))
			f.Close()
		}
	}
}

// e2eFakeClaude is a Claude Code as the bus sees it (see the file's head).
func e2eFakeClaude() {
	buildinfo.Version = os.Getenv("AGENTBUS_E2E_VERSION")
	agent, project := os.Getenv("AGENT_BUS_AGENT"), os.Getenv("AGENT_BUS_PROJECT")
	session := os.Getenv("AGENTBUS_E2E_RESUMED")
	if session == "" {
		session = os.Getenv("AGENTBUS_E2E_SESSION")
	}
	old, err := term.MakeRaw(0)
	if err != nil {
		e2eLog("no raw: %v", err)
		return
	}
	defer term.Restore(0, old)

	ctx := context.Background()
	client, err := bus.Connect("")
	if err != nil {
		e2eLog("broker: %v", err)
		return
	}
	b, err := bus.Open(client, project)
	if err != nil {
		e2eLog("bus: %v", err)
		return
	}
	// What agent-launch does before it execs the client: bind this pid.
	var berr bytes.Buffer
	if code := runBind(ctx, bindDeps{Bind: b.Bind, Procs: func(ctx context.Context, t string) ([]termhost.Proc, string, error) {
		return termProcs(ctx, project, t)
	}, Start: dispatch.ProcStart, Boot: lifecycle.HostBootID, Env: os.Getenv}, []string{agent, "--pid", strconv.Itoa(os.Getpid())}, io.Discard, &berr); code != 0 {
		e2eLog("bind = %d: %s", code, berr.String())
	}
	// What Claude Code's SessionStart hooks do: record the resume (the bus
	// proves the caller by its ancestors; here the client is the caller) and
	// end the turn.
	self, _ := dispatch.ProcStart(os.Getpid())
	chain := []bus.ProcID{{PID: os.Getpid(), Start: self}}
	for _, a := range dispatch.Ancestors(os.Getpid()) {
		chain = append(chain, bus.ProcID{PID: a.PID, Start: a.Start})
	}
	if err := b.RecordResume(ctx, agent, chain, "", session); err != nil {
		e2eLog("record resume: %v", err)
	}
	// The turn hooks run as the client's children, as Claude Code runs them:
	// the bus proves the caller by its ancestors.
	exe, _ := os.Executable()
	hook := func(which, event, prompt string) {
		in, _ := json.Marshal(map[string]string{"hook_event_name": event, "session_id": session, "prompt": prompt})
		c := exec.Command(exe, "-test.run=^$")
		c.Env = append(os.Environ(), "AGENTBUS_E2E_FAKE=turnhook", "AGENTBUS_E2E_HOOK="+which)
		c.Stdin = bytes.NewReader(in)
		if err := c.Run(); err != nil {
			e2eLog("hook %s: %v", which, err)
		}
	}
	hook("end", "SessionStart", "")
	// Its boot steps (the boot line's turn): presence published as
	// `agentbus status idle` does, with the caller's identity.
	if _, err := b.Status(ctx, agent, "idle", agent+" online", statusIdent(ctx)); err != nil {
		e2eLog("status: %v", err)
	}

	cols := 100
	if w, _, err := term.GetSize(1); err == nil && w > 20 {
		cols = w
	}
	ui := newFakeClaudeUI(project, agent, cols, os.Stdout)
	if s := os.Getenv("AGENTBUS_E2E_RESUMED"); s != "" {
		ui.history = append(ui.history, "resumed "+s)
	}
	ui.draw("", false, true)
	ui.loop(os.Stdin, func(line string) {
		e2eLog("submitted %q", line)
		hook("start", "UserPromptSubmit", line)
	}, func(line string) {
		time.Sleep(300 * time.Millisecond)
		if strings.Contains(line, "subscribe --drain "+agent) {
			var out bytes.Buffer
			host, _ := os.Hostname()
			runDrain(ctx, b, agent, host, &out, false)
			ui.history = append(ui.history, "drained "+strconv.Itoa(strings.Count(out.String(), "\"event\":\"cmd\"")))
		}
		hook("end", "Stop", "")
	})
}

// e2eBuf is a buffer the dispatcher writes from its goroutine.
type e2eBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *e2eBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *e2eBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// e2eWorld is the project the release test runs.
type e2eWorld struct {
	b       *bus.Bus
	project string
	h       *holder.Host
	log     string
	session map[string]string // each role's first session

	raw, sock string
	launch    func(holder.Start) (holder.Command, error)
	hm        *fakeHolderManager
	mu        sync.Mutex
	srv       *holder.Server // the holder's current process
	scancel   context.CancelFunc
	resumed   []string // the roles started on a recorded session, in order

	killed atomic.Bool // the self-repair's grace runs on a shifted clock once set
	dlog   e2eBuf      // the dispatcher's log
}

// serve starts a holder server on the world's socket, as the unit starts
// the holder process.
func (w *e2eWorld) serve(t *testing.T) {
	t.Helper()
	paths, err := service.PathsFrom(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := holder.NewServer(holder.Config{Project: w.project, Build: w.raw, Socket: w.sock, Launch: w.launch,
		TermsFile: holderTermsFile(paths, w.project, w.raw), // as holderConfig sets it
		Grace:     time.Second, SubmitWait: 2 * time.Second, PersonQuiet: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen(); err != nil {
		t.Fatalf("the holder does not listen on %s (%d bytes): %v", w.sock, len(w.sock), err)
	}
	sctx, scancel := context.WithCancel(context.Background())
	go srv.Serve(sctx)
	w.mu.Lock()
	w.srv, w.scancel = srv, scancel
	w.mu.Unlock()
}

// stop ends the holder's current process: its terminals closed, its socket
// gone.
func (w *e2eWorld) stop() {
	w.mu.Lock()
	srv, scancel := w.srv, w.scancel
	w.srv, w.scancel = nil, nil
	w.mu.Unlock()
	if srv != nil {
		scancel()
		srv.Shutdown()
	}
}

func (w *e2eWorld) fakeLog() string {
	b, _ := os.ReadFile(w.log)
	return string(b)
}

// screen is role's live terminal's screen ("" when it has none).
func (w *e2eWorld) screen(t *testing.T, role string) (string, termhost.Term) {
	t.Helper()
	terms, err := w.h.List(context.Background(), w.project)
	if err != nil {
		t.Fatal(err)
	}
	for _, tm := range terms {
		if tm.Name == role && tm.Alive {
			sc, err := w.h.Screen(context.Background(), tm.ID, 0)
			if err != nil {
				return "", tm
			}
			return strings.Join(sc.Lines, "\n"), tm
		}
	}
	return "", termhost.Term{}
}

// until polls cond every 50 ms for d.
func until(d time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// e2eGate is set by the release gate (dev -> main): the test must run, so a
// missing broker fails it instead of skipping it (a skip reads ok).
const e2eGate = "AGENTBUS_RELEASE_GATE"

func TestReleaseEndToEnd(t *testing.T) {
	w := e2eUp(t)
	ctx := context.Background()
	b := w.b

	// 3. The dispatcher, as the service runs it, with its self-repair.
	w.startDispatch(t)

	// 2-3. Two wakes in a row: each typed, submitted, its turn ended with
	// Claude's title, the message drained.
	for _, text := range []string{"first task", "second task"} {
		w.wake(t, "coder", text)
	}

	// 4. coder dies; the self-repair resumes it on the holder.
	_, old := w.screen(t, "coder")
	before, _ := b.Agents(ctx)
	if err := b.SetBudget(ctx, "anthropic", bus.BudgetSnapshot{Provider: "anthropic", ObservedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	w.killed.Store(true)
	if err := syscall.Kill(old.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var sc string
	var now termhost.Term
	if !until(30*time.Second, func() bool {
		sc, now = w.screen(t, "coder")
		return now.ID != "" && now.ID != old.ID && strings.Contains(sc, "resumed "+w.session["coder"])
	}) {
		evs, _ := b.SwapLog(ctx, "coder", 20)
		t.Fatalf("coder was not relaunched on its session %s (swap ledger %+v):\n%s", w.session["coder"], evs, sc)
	}
	if !until(10*time.Second, func() bool {
		snaps, _ := b.Agents(ctx)
		s := snaps["coder"]
		return s.PID == now.PID && s.PID != before["coder"].PID
	}) {
		t.Fatalf("the relaunched coder never proved its process")
	}
}

// e2eUp is the release test's step 1 (see the file's head): the project, the
// machine, this build's holder serving, up run with the services stubbed,
// and master and coder bound on the holder.
func e2eUp(t *testing.T) *e2eWorld {
	t.Helper()
	if os.Getenv(e2eGate) == "1" && os.Getenv("AGENTBUS_TEST_REDIS_URL") == "" {
		t.Fatal("release gate: AGENTBUS_TEST_REDIS_URL must name an isolated disposable broker; the release test does not skip under the gate")
	}
	testredis.Configure(t)
	ctx := context.Background()

	// A project name of 32 bytes, the longest ValidName takes.
	project := "e2e" + strconv.FormatInt(time.Now().UnixNano(), 36)
	project += strings.Repeat("x", 32-len(project))
	if len(project) != 32 || !bus.ValidName(project) {
		t.Fatalf("project %q", project)
	}
	client, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	t.Cleanup(func() {
		_, _ = bus.DeleteProject(context.Background(), client, project)
		client.Close()
	})
	b, err := bus.Open(client, project)
	if err != nil {
		t.Fatal(err)
	}

	// The machine: a run directory as long as /run/user/<uid>, a home with no
	// config of the person's, the templates and the manifest of the project.
	run, err := os.MkdirTemp("/tmp", "e2e")
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(run, 0o700)
	t.Cleanup(func() { os.RemoveAll(run) })
	root := t.TempDir()
	templates, work, home := filepath.Join(root, "projects"), filepath.Join(root, "work"), filepath.Join(root, "home")
	for _, d := range []string{templates, work, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, "roles.toml")
	if err := os.WriteFile(manifest, []byte("schema_version = 2\n[defaults]\nprofile = \"claude\"\n"+
		"[profiles.claude]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n"+
		"[roles.master]\ntier = \"boot\"\nrequired = true\nskills = []\n[roles.coder]\ntier = \"boot\"\nrequired = true\nskills = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"XDG_RUNTIME_DIR": run, "HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME": filepath.Join(home, ".state"), "XDG_DATA_HOME": filepath.Join(home, ".data"),
		"AGENTBUS_PROJECTS_DIR": templates, "ROLES_TOML": manifest, "AGENT_BUS_PROJECT": "", "AGENT_BUS_AGENT": "", "AGENTBUS_TERM_ID": ""} {
		t.Setenv(k, v)
	}
	prev := buildinfo.Version
	buildinfo.Version = e2eRelease
	t.Cleanup(func() { buildinfo.Version = prev })
	raw := holder.BuildID()

	// The holder: this build's, on its own socket, its launch the fake client.
	sock, err := holder.SocketPath(os.Getenv, project, service.BuildKey(raw))
	if err != nil {
		t.Fatalf("the holder's socket for a 32-byte project on %s: %v", raw, err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := &e2eWorld{b: b, project: project, log: filepath.Join(root, "fake.log"), session: map[string]string{}, raw: raw, sock: sock}
	w.launch = func(s holder.Start) (holder.Command, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		if s.ResumeSession != "" {
			w.resumed = append(w.resumed, s.Role)
		}
		if _, ok := w.session[s.Role]; !ok {
			w.session[s.Role] = fmt.Sprintf("e2e%08x-%s", time.Now().UnixNano()&0xffffffff, s.Role)
		}
		return holder.Command{Argv: []string{self, "-test.run=^$"}, Client: "claude-code", Env: []string{
			"AGENTBUS_E2E_FAKE=claude", "AGENTBUS_E2E_VERSION=" + e2eRelease, "AGENTBUS_E2E_LOG=" + w.log,
			"AGENTBUS_E2E_SESSION=" + w.session[s.Role], "AGENTBUS_E2E_RESUMED=" + s.ResumeSession,
			"AGENT_BUS_AGENT=" + s.Role, "AGENT_BUS_PROJECT=" + project, "AGENT_BUS_CLIENT=claude-code", "AGENT_BUS_PROVIDER=anthropic"}}, nil
	}
	w.serve(t)
	t.Cleanup(w.stop)
	if w.h, err = holder.NewHost(project, raw, os.Getenv); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("fake clients' log:\n%s", w.fakeLog())
		}
	})

	// 1. up: the services stubbed, the holder step with the real probe, the
	// team on the holder.
	deps := upDeps{
		Bootstrap: func(args []string) error {
			tab := func(role string) string {
				return "\n[[tabs]]\nname = \"" + role + "\"\ncommand = \"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch " + role + " " + project + "\"\n"
			}
			return os.WriteFile(filepath.Join(templates, project+".toml"), []byte("name = \""+project+"\"\nworking_dir = \""+work+"\"\n"+tab("master")+tab("coder")), 0o644)
		},
		Env:           os.Getenv,
		HolderBuild:   holder.BuildID,
		HolderAnswers: func(p, key string) bool { return holder.Ready(os.Getenv, p, key, 2*time.Second) },
		Team:          func(p string) (teamHost, error) { return holder.NewHost(p, holder.BuildID(), os.Getenv) },
		Resumes:       func(string) (map[string]bus.ResumeRecord, error) { return b.ResumeRecords(ctx) },
		Notice:        func(_, text string) error { t.Logf("up notice: %s", text); return nil },
	}
	u := withServices(t, &deps)
	u.exe = filepath.Join(root, "agentbus")
	if err := os.WriteFile(u.exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.hm = &fakeHolderManager{fakeManager: u.m, holders: map[string]service.HolderSpec{}, active: map[string]bool{}}
	deps.Services = func() (service.Manager, string) { return w.hm, "" }
	var out, errw bytes.Buffer
	if code := runUp([]string{project, work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d\n%s\n%s", code, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "started as a fake service (no agent runs on it yet)") {
		t.Fatalf("up did not find this build's holder answering:\n%s", out.String())
	}
	for _, role := range []string{"master", "coder"} {
		role := role
		if !until(10*time.Second, func() bool {
			snaps, err := b.Agents(ctx)
			return err == nil && snaps[role].PID > 0 && snaps[role].Client == "claude-code"
		}) {
			t.Fatalf("%s never bound its process:\n%s", role, out.String())
		}
	}

	return w
}

// startDispatch runs the dispatcher as the service runs it (its runtime and
// self-repair from dispatchsvc.Config, the production wiring, on the
// world's service manager), on fast ticks; once w.killed is set, each
// self-repair read is 25 s later than the last (Deps.SelfRepairSkew).
func (w *e2eWorld) startDispatch(t *testing.T) {
	t.Helper()
	b := w.b
	var shifts atomic.Int64
	var last string
	var selfCheck func(context.Context) (health.Outcome, error)
	scfg, rt := w.serviceConfig(t, func(now time.Time) time.Time {
		if out, _ := selfCheck(context.Background()); out.Detail != last { // the previous pass's verdict, when it changes
			last = out.Detail
			fmt.Fprintf(&w.dlog, "self-repair %s: %s\n", out.Status, out.Detail)
		}
		if w.killed.Load() { // the grace on a shifted clock: each read 25 s later
			return now.Add(time.Duration(shifts.Add(1)) * 25 * time.Second)
		}
		return now
	})
	if mode := config.SelfRepairMode(config.Machine(os.Getenv)); mode != "on" || scfg.SelfRepair.Pass == nil {
		t.Fatalf("self-repair is %q, not on", mode)
	}
	selfCheck = e2eHealthCheck(t, scfg, "self-repair")
	src := scfg.SelfRepair
	src.Every = 200 * time.Millisecond
	cfg := dispatch.DefaultConfig()
	// dispatch.retry (60 s) spaces two wakes of one agent even after a drain
	// (rule 5: the record keeps its At, controller ruling 1): 2 s here.
	cfg.Retry, cfg.EscalateAfter = 2*time.Second, time.Hour
	cfg.Tick, cfg.Lease, cfg.Renew, cfg.MinPTTL = 50*time.Millisecond, 2*time.Second, 100*time.Millisecond, 300*time.Millisecond
	cfg.Log = &w.dlog
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("dispatcher log:\n%s", w.dlog.String())
		}
	})
	cfg.SelfRepair = src
	dctx, dcancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatch.Run(dctx, b, rt, cfg) }()
	t.Cleanup(func() { dcancel(); <-done })

}

// serviceConfig is the dispatch service's configuration of this world: the
// production Deps on its service manager, the self-repair read skewed by
// skew (nil: unskewed).
func (w *e2eWorld) serviceConfig(t *testing.T, skew func(time.Time) time.Time) (dispatch.Config, dispatch.Runtime) {
	t.Helper()
	paths, err := service.PathsFrom(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	d := serviceDispatchDeps(w.b, "", "", paths, io.Discard)
	d.Manager, d.SelfRepairSkew = w.hm, skew
	var cfg dispatch.Config
	rt, err := dispatchsvc.Config(d, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, rt
}

// e2eHealthCheck is cfg's health check id, run as the dispatcher runs it.
func e2eHealthCheck(t *testing.T, cfg dispatch.Config, id string) func(context.Context) (health.Outcome, error) {
	t.Helper()
	for _, c := range cfg.Health.Checks {
		if c.Definition.ID == id {
			return c.Run
		}
	}
	t.Fatalf("no %s check in the service's config", id)
	return nil
}

// wake sends role a directive and follows it: the turn hooks read working,
// the wake typed and submitted, a drain after it (the directive no longer
// pending), the turn ended idle. Counted from the screen as it was before
// the directive, so another message to role (self-repair tells master)
// does not upset the count.
func (w *e2eWorld) wake(t *testing.T, role, text string) {
	t.Helper()
	ctx := context.Background()
	b, project := w.b, w.project
	sc, _ := w.screen(t, role)
	subs, drains := strings.Count(sc, "SUBMITTED [agentbus#"), strings.Count(sc, "drained ")
	from := "master"
	if role == "master" {
		from = "coder"
	}
	id, err := b.Cmd(ctx, from, role, bus.CmdDirective, "", text)
	if err != nil {
		t.Fatal(err)
	}
	if !until(20*time.Second, func() bool {
		snaps, _ := b.Agents(ctx)
		return snaps[role].State == "working"
	}) {
		t.Fatalf("wake %q: %s never read working: the turn hooks did not run", text, role)
	}
	if !until(20*time.Second, func() bool {
		sc, _ = w.screen(t, role)
		pending, err := b.PendingFor(ctx, role, id)
		return strings.Count(sc, "SUBMITTED [agentbus#") > subs && strings.Count(sc, "drained ") > drains && err == nil && !pending
	}) {
		t.Fatalf("wake %q of %s never landed (a title on the input line reads the agent unknown):\n%s", text, role, sc)
	}
	if strings.Contains(sc, project+":"+role) {
		t.Fatalf("wake %q: Claude's title is on %s's screen:\n%s", text, role, sc)
	}
	if !until(5*time.Second, func() bool {
		snaps, _ := b.Agents(ctx)
		return snaps[role].State == "idle"
	}) {
		snaps, _ := b.Agents(ctx)
		t.Fatalf("wake %q: %s's turn never ended: state %q (%+v)", text, role, snaps[role].State, snaps[role])
	}
}
