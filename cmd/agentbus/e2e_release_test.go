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
//  3. The real dispatcher (dispatchRuntime: the holder, the client by the
//     terminal's profile) wakes coder twice in a row: each wake typed,
//     submitted, the turn run and ended, the message drained with the real
//     drain, so the second wake needs the screen ready again after a title.
//  4. coder's process is killed; the real self-repair (on, its production
//     wiring: serviceSelfRepair) relaunches it on the holder, resuming its
//     recorded session, proved by the new process's bind.
//
// What it cannot cover: a real Claude Code (its screen is a fake drawn from
// the claude signature and a captured screen, its hooks are called in
// process, not by the client), the real launcher (the holder's launch is the
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
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// The test binary doubles as the fake client the holder runs.
func TestMain(m *testing.M) {
	if os.Getenv("AGENTBUS_E2E_FAKE") == "claude" {
		e2eFakeClaude()
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
	os.Stdout.WriteString("\x1b[?2004h")

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
	}, Start: dispatch.ProcStart, Boot: hostBootID, Env: os.Getenv}, []string{agent, "--pid", strconv.Itoa(os.Getpid())}, io.Discard, &berr); code != 0 {
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
	hook := func(which, event, prompt string) {
		in, _ := json.Marshal(map[string]string{"hook_event_name": event, "session_id": session, "prompt": prompt})
		turnHook([]string{which}, os.Environ(), bytes.NewReader(in), io.Discard, realTurnFlip, func(string, string, string) {})
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
	rule := "\x1b[38;2;136;136;136m" + strings.Repeat("─", cols-2) + "\x1b[0m"
	var history []string
	if s := os.Getenv("AGENTBUS_E2E_RESUMED"); s != "" {
		history = append(history, "resumed "+s)
	}
	// draw is the screen; titled writes Claude's turn-end title where it was
	// in the field, right after `❯` (vt-osc-title).
	draw := func(input string, busy, titled bool) {
		var w strings.Builder
		// Redrawn in place, as Claude's renderer does: a full-screen erase
		// (ED 2) would push the turn's spinner line into the scrollback the
		// dispatcher reads, and the agent would read busy for good.
		w.WriteString("\x1b[H\x1b[J\x1b[1mClaude Code\x1b[0m (e2e fake)\r\n\r\n")
		for _, h := range history {
			w.WriteString("● " + h + "\r\n")
		}
		if busy {
			w.WriteString("✶ Kneading… (1s)\r\n")
		}
		w.WriteString(rule + "\r\n❯\u00a0")
		if titled {
			w.WriteString("\x1b]0;✳ " + project + ":" + agent + "\x07")
		}
		if input == "" {
			w.WriteString("\x1b[2mTry \"fix the tests\"\x1b[0m\r\n")
		} else {
			w.WriteString(input + "\r\n")
		}
		w.WriteString(rule + "\r\n  \x1b[38;5;30mModel: fake\x1b[0m")
		os.Stdout.WriteString(w.String())
	}
	draw("", false, true)

	var input []byte
	pasting := false
	var seq []byte // an escape sequence being read
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return
		}
		for _, c := range buf[:n] {
			if len(seq) > 0 || c == 0x1b {
				seq = append(seq, c)
				switch s := string(seq); {
				case s == "\x1b[200~":
					pasting, seq = true, nil
				case s == "\x1b[201~":
					pasting, seq = false, nil
					draw(string(input), false, false)
				case len(seq) > 6 || len(seq) > 1 && !strings.HasPrefix("\x1b[200~", s) && !strings.HasPrefix("\x1b[201~", s):
					seq = nil // not ours: dropped
				}
				continue
			}
			if c == '\r' && !pasting {
				line := string(input)
				input = input[:0]
				if line == "" {
					continue
				}
				e2eLog("submitted %q", line)
				history = append(history, "SUBMITTED "+line)
				hook("start", "UserPromptSubmit", line)
				draw("", true, false)
				time.Sleep(300 * time.Millisecond)
				if strings.Contains(line, "subscribe --drain "+agent) {
					var out bytes.Buffer
					host, _ := os.Hostname()
					runDrain(ctx, b, agent, host, &out, false)
					history = append(history, "drained "+strconv.Itoa(strings.Count(out.String(), "\"event\":\"cmd\"")))
				}
				hook("end", "Stop", "")
				draw("", false, true)
				continue
			}
			if c >= 0x20 || pasting {
				input = append(input, c)
				if !pasting {
					draw(string(input), false, false)
				}
			}
		}
	}
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

func TestReleaseEndToEnd(t *testing.T) {
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
	w := &e2eWorld{b: b, project: project, log: filepath.Join(root, "fake.log"), session: map[string]string{}}
	var mu sync.Mutex
	launch := func(s holder.Start) (holder.Command, error) {
		mu.Lock()
		defer mu.Unlock()
		if _, ok := w.session[s.Role]; !ok {
			w.session[s.Role] = fmt.Sprintf("e2e%08x-%s", time.Now().UnixNano()&0xffffffff, s.Role)
		}
		return holder.Command{Argv: []string{self, "-test.run=^$"}, Client: "claude-code", Env: []string{
			"AGENTBUS_E2E_FAKE=claude", "AGENTBUS_E2E_VERSION=" + e2eRelease, "AGENTBUS_E2E_LOG=" + w.log,
			"AGENTBUS_E2E_SESSION=" + w.session[s.Role], "AGENTBUS_E2E_RESUMED=" + s.ResumeSession,
			"AGENT_BUS_AGENT=" + s.Role, "AGENT_BUS_PROJECT=" + project, "AGENT_BUS_CLIENT=claude-code", "AGENT_BUS_PROVIDER=anthropic"}}, nil
	}
	srv, err := holder.NewServer(holder.Config{Project: project, Build: raw, Socket: sock, Launch: launch,
		Grace: time.Second, SubmitWait: 2 * time.Second, PersonQuiet: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen(); err != nil {
		t.Fatalf("the holder does not listen on %s (%d bytes): %v", sock, len(sock), err)
	}
	sctx, scancel := context.WithCancel(context.Background())
	go srv.Serve(sctx)
	t.Cleanup(func() { scancel(); srv.Shutdown() })
	if w.h, err = holder.NewHost(project, raw, os.Getenv); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if t.Failed() {
			t.Logf("fake clients' log:\n%s", w.fakeLog())
		}
	}()

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
		HolderAnswers: func(p, key string) bool { return holderReady(os.Getenv, p, key, 2*time.Second) },
		Team:          func(p string) (teamHost, error) { return holder.NewHost(p, holder.BuildID(), os.Getenv) },
		Resumes:       func(string) (map[string]bus.ResumeRecord, error) { return b.ResumeRecords(ctx) },
		Notice:        func(_, text string) error { t.Logf("up notice: %s", text); return nil },
	}
	u := withServices(t, &deps)
	u.exe = filepath.Join(root, "agentbus")
	if err := os.WriteFile(u.exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hm := &fakeHolderManager{fakeManager: u.m, holders: map[string]service.HolderSpec{}, active: map[string]bool{}}
	deps.Services = func() (service.Manager, string) { return hm, "" }
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

	// 3. The dispatcher, as the service runs it, with its self-repair.
	rt, err := dispatchRuntime(project, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	chore, src := serviceSelfRepair(b, os.Getenv, nil)
	if chore.mode != "on" || chore.relaunch == nil {
		t.Fatalf("self-repair is %q, not on", chore.mode)
	}
	var killed atomic.Bool
	var shifts atomic.Int64
	read := chore.read
	chore.read = func(ctx context.Context) (selfRepairReads, error) {
		r, err := read(ctx)
		if killed.Load() { // the grace on a shifted clock: each read 25 s later
			r.Now = r.Now.Add(time.Duration(shifts.Add(1)) * 25 * time.Second)
		}
		return r, err
	}
	src.Every = 200 * time.Millisecond
	cfg := dispatch.DefaultConfig()
	// dispatch.retry (60 s) spaces two wakes of one agent even after a drain
	// (rule 5: the record keeps its At, controller ruling 1): 2 s here.
	cfg.Retry, cfg.EscalateAfter = 2*time.Second, time.Hour
	cfg.Tick, cfg.Lease, cfg.Renew, cfg.MinPTTL = 50*time.Millisecond, 2*time.Second, 100*time.Millisecond, 300*time.Millisecond
	var dlog e2eBuf
	cfg.Log = &dlog
	defer func() {
		if t.Failed() {
			t.Logf("dispatcher log:\n%s", dlog.String())
		}
	}()
	cfg.SelfRepair = src
	dctx, dcancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatch.Run(dctx, b, rt, cfg) }()
	t.Cleanup(func() { dcancel(); <-done })

	// 2-3. Two wakes in a row: each typed, submitted, its turn ended with
	// Claude's title, the message drained.
	for i, text := range []string{"first task", "second task"} {
		if _, err := b.Cmd(ctx, "master", "coder", bus.CmdDirective, "", text); err != nil {
			t.Fatal(err)
		}
		want := "drained " + "1"
		var sc string
		if !until(20*time.Second, func() bool {
			sc, _ = w.screen(t, "coder")
			return strings.Count(sc, "SUBMITTED [agentbus#") == i+1 && strings.Count(sc, want) == i+1
		}) {
			t.Fatalf("wake %d never landed (a title on the input line reads the agent unknown):\n%s", i+1, sc)
		}
		if strings.Contains(sc, project+":coder") {
			t.Fatalf("wake %d: Claude's title is on the screen:\n%s", i+1, sc)
		}
		if !until(5*time.Second, func() bool {
			snaps, _ := b.Agents(ctx)
			return snaps["coder"].State == "idle"
		}) {
			snaps, _ := b.Agents(ctx)
			t.Fatalf("wake %d: coder's turn never ended: state %q (%+v)", i+1, snaps["coder"].State, snaps["coder"])
		}
	}

	// 4. coder dies; the self-repair resumes it on the holder.
	_, old := w.screen(t, "coder")
	before, _ := b.Agents(ctx)
	if err := b.SetBudget(ctx, "anthropic", bus.BudgetSnapshot{Provider: "anthropic", ObservedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	killed.Store(true)
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
