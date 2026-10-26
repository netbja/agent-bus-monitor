package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/projlist"
)

func pickRows(names ...string) []projlist.Row {
	var rows []projlist.Row
	for _, n := range names {
		rows = append(rows, projlist.Row{Name: n, WorkingDir: "/src/" + n, Path: "/cfg/" + n + ".toml"})
	}
	return rows
}

// brokerKeys is a fake existence check: keys for the projects listed, none for the others.
func brokerKeys(present ...string) projlist.KeysFunc {
	return func(_ context.Context, p string) ([]string, error) {
		for _, q := range present {
			if q == p {
				return []string{p + ":status"}, nil
			}
		}
		return nil, nil
	}
}

func TestPickFilterIsSubstring(t *testing.T) {
	m := newPickModel("/cfg", []projlist.Row{
		{Name: "hyperliquid", WorkingDir: "/data/projects/perp-bot"},
		{Name: "ai-tradex-solana", WorkingDir: "/data/projects/tradex"},
	}, nil)
	names := func() []string {
		var out []string
		for _, r := range m.visible() {
			out = append(out, r.Name)
		}
		return out
	}
	for _, c := range []struct {
		filter string
		want   []string
	}{
		{"", []string{"hyperliquid", "ai-tradex-solana"}},
		{"liq", []string{"hyperliquid"}},
		{"SOL", []string{"ai-tradex-solana"}},
		{"hl", nil},                      // not a substring of either name or dir: fuzzy matching is deliberately out
		{"BOT", []string{"hyperliquid"}}, // the working dir counts too
	} {
		m.filter = c.filter
		if got := names(); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("filter %q: %v, want %v", c.filter, got, c.want)
		}
	}
}

func TestPickOpenPresentRunsUp(t *testing.T) {
	m := newPickModel("/cfg", pickRows("demo"), nil)
	var calls []string
	run := func(p string) error { calls = append(calls, p); return nil }
	if !m.open(context.Background(), brokerKeys("demo"), run) {
		t.Fatal("a present project whose up succeeds closes the picker")
	}
	if fmt.Sprint(calls) != "[demo]" {
		t.Fatalf("run calls %v, want [demo]", calls)
	}

	calls = nil
	failing := func(p string) error { calls = append(calls, p); return errors.New("bootstrap failed: boom") }
	if m.open(context.Background(), brokerKeys("demo"), failing) {
		t.Fatal("a failed up keeps the picker open")
	}
	if len(calls) != 1 {
		t.Fatalf("run calls %v, want one", calls)
	}
	if st, isErr := m.statusLine(); !isErr || !strings.Contains(st, "bootstrap failed: boom") {
		t.Fatalf("status %q (err %v) does not hold the failure", st, isErr)
	}
	if m.busyText() != "" {
		t.Fatal("open leaves the picker busy")
	}
}

func TestPickOpenAbsentRefuses(t *testing.T) {
	m := newPickModel("/cfg", []projlist.Row{{Name: "demo", WorkingDir: "/src/demo"}}, nil)
	ran := false
	if m.open(context.Background(), brokerKeys(), func(string) error { ran = true; return nil }) {
		t.Fatal("an absent project keeps the picker open")
	}
	if ran {
		t.Fatal("up ran for a project with no keys on the broker")
	}
	st, isErr := m.statusLine()
	if !isErr || !strings.Contains(st, "agentbus up demo /src/demo") || !strings.Contains(st, "no keys on the broker") {
		t.Fatalf("status %q", st)
	}
	if got := m.rows[0].Broker; got != projlist.Absent {
		t.Fatalf("the fresh check is not shown on the row: %v", got)
	}
}

func TestPickOpenUnknownRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		keys projlist.KeysFunc
		want string
	}{
		{"error", func(context.Context, string) ([]string, error) { return nil, errors.New("connection refused") }, "connection refused"},
		{"timeout", func(ctx context.Context, _ string) ([]string, error) {
			<-ctx.Done()
			time.Sleep(time.Second)
			return nil, nil
		}, "deadline exceeded"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newPickModel("/cfg", pickRows("demo"), nil)
			m.timeout = 50 * time.Millisecond
			ran := false
			start := time.Now()
			if m.open(context.Background(), c.keys, func(string) error { ran = true; return nil }) {
				t.Fatal("an unknown state keeps the picker open")
			}
			if d := time.Since(start); d > 500*time.Millisecond {
				t.Fatalf("open waited %v: the check is not bounded", d)
			}
			if ran {
				t.Fatal("up ran although the broker could not be read: absence or presence was inferred")
			}
			if st, isErr := m.statusLine(); !isErr || !strings.Contains(st, c.want) {
				t.Fatalf("status %q (err %v), want it to name %q", st, isErr, c.want)
			}
		})
	}
}

func TestPickProductionTimeoutIsTwoSeconds(t *testing.T) {
	if pickTimeout != 2*time.Second {
		t.Fatalf("pickTimeout = %v", pickTimeout)
	}
	if m := newPickModel("/cfg", nil, nil); m.timeout != pickTimeout {
		t.Fatalf("default model timeout %v", m.timeout)
	}
}

// The picker reads the broker through the keys function and nothing else:
// listing and opening go through it exactly once per probe, and run is the
// only other effect.
func TestPickListingAndOpeningOnlyRead(t *testing.T) {
	var probes atomic.Int32
	keys := func(_ context.Context, p string) ([]string, error) {
		probes.Add(1)
		return []string{p + ":status"}, nil
	}
	rows := projlist.Annotate(context.Background(), pickRows("a", "b", "c"), keys, pickTimeout)
	m := newPickModel("/cfg", nil, nil)
	m.setAnnotated(rows, nil)
	if probes.Load() != 3 {
		t.Fatalf("probes %d", probes.Load())
	}
	m.open(context.Background(), keys, func(string) error { return nil })
	if probes.Load() != 4 {
		t.Fatalf("open is not one fresh check: %d probes", probes.Load())
	}
}

func TestPickBrokerDownIsEveryRowUnknown(t *testing.T) {
	m := newPickModel("/cfg", pickRows("a", "b"), nil)
	if m.brokerDown() {
		t.Fatal("down before any check")
	}
	m.setAnnotated(pickRows("a", "b"), nil) // every row Unknown
	if !m.brokerDown() {
		t.Fatal("every row unknown should read broker unreachable")
	}
	rows := pickRows("a", "b")
	rows[1].Broker = projlist.Absent
	m.setAnnotated(rows, nil)
	if m.brokerDown() {
		t.Fatal("one answered row means the broker answered")
	}
	m = newPickModel("/cfg", nil, nil)
	m.setAnnotated(nil, errors.New("dial tcp: refused"))
	if !m.brokerDown() {
		t.Fatal("a failed connection with no rows is still a down broker")
	}
	if len(m.visible()) != 0 {
		t.Fatal("rows invented")
	}
}

func TestAgentbusBeside(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	beside := filepath.Join(filepath.Dir(exe), "agentbus")
	got := agentbusBeside()
	if _, err := os.Stat(beside); err == nil {
		if got != beside {
			t.Fatalf("got %q, want %q", got, beside)
		}
	} else if got != "agentbus" {
		t.Fatalf("got %q, want the PATH fallback", got)
	}
}

func TestUpRunnerReportsStderrTailAndTimeout(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "agentbus")
	script := "#!/bin/sh\necho \"args: $*\"\nif [ \"$2\" = slow ]; then sleep 2; exit 0; fi\necho first line >&2\necho \"agentbus up: bootstrap failed: $PICK_TEST_VAR\" >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICK_TEST_VAR", "env passed")
	err := upRunner(fake, 5*time.Second)("demo")
	if err == nil || err.Error() != "bootstrap failed: env passed" {
		t.Fatalf("err %v: want the stderr tail with the environment passed through", err)
	}
	if strings.Contains(err.Error(), "first line") || strings.Contains(err.Error(), "args:") {
		t.Fatalf("err %v: only the stderr tail belongs on one status line", err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	start := time.Now()
	err = upRunner(fake, 100*time.Millisecond)("slow")
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("err %v: want a timeout that says up was left running", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("the wait is not bounded")
	}
}

// pickHarness drives a picker without an event loop, as harness does for the
// dashboard: keys go through the capture, then the root.
type pickHarness struct {
	t   *testing.T
	sim tcell.SimulationScreen
	app *tview.Application
	p   *picker
	ran []string
}

func newPickHarness(t *testing.T, m *pickModel, keys projlist.KeysFunc) *pickHarness {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	app := tview.NewApplication().SetScreen(sim)
	h := &pickHarness{t: t, sim: sim, app: app}
	h.p = newPicker(app, m, keys, func(p string) error { h.ran = append(h.ran, p); return nil })
	h.p.sync = true // open on the caller's goroutine, so a test sees its result
	h.p.quit = func() {}
	t.Cleanup(sim.Fini)
	return h
}

func (h *pickHarness) draw(w, hh int) string {
	h.sim.SetSize(w, hh)
	h.p.view.SetRect(0, 0, w, hh)
	h.p.view.Draw(h.sim)
	h.sim.Show()
	return screenText(h.sim)
}

func (h *pickHarness) key(ev *tcell.EventKey) {
	if out := h.p.handleKey(ev); out != nil {
		h.p.view.InputHandler()(out, func(p tview.Primitive) { h.app.SetFocus(p) })
	}
}

func TestPickKeys(t *testing.T) {
	m := newPickModel("/cfg", pickRows("alpha", "beta", "gamma"), nil)
	h := newPickHarness(t, m, brokerKeys("alpha", "beta", "gamma"))
	h.draw(100, 20)
	if got := h.p.list.Selected(); got != "alpha" {
		t.Fatalf("first row not selected: %q", got)
	}
	h.key(key(tcell.KeyDown))
	h.draw(100, 20)
	if got := h.p.list.Selected(); got != "beta" {
		t.Fatalf("down: %q", got)
	}
	for _, r := range "amm" { // q, j, k are text here: typing filters
		h.key(runeKey(r))
	}
	screen := h.draw(100, 20)
	if m.filter != "amm" || len(m.visible()) != 1 || h.p.list.Selected() != "gamma" {
		t.Fatalf("filter %q visible %v selected %q", m.filter, m.visible(), h.p.list.Selected())
	}
	if !strings.Contains(screen, "amm") {
		t.Fatalf("the filter is not shown:\n%s", screen)
	}
	h.key(key(tcell.KeyBackspace2))
	if m.filter != "am" {
		t.Fatalf("backspace: %q", m.filter)
	}
	h.key(key(tcell.KeyEnter))
	if fmt.Sprint(h.ran) != "[gamma]" {
		t.Fatalf("enter ran %v", h.ran)
	}
	if !h.p.closed {
		t.Fatal("a successful open closes the picker")
	}
}

func TestPickErrorStaysUntilAKey(t *testing.T) {
	m := newPickModel("/cfg", pickRows("demo"), nil)
	h := newPickHarness(t, m, brokerKeys())
	h.draw(100, 20)
	h.key(key(tcell.KeyEnter))
	if !strings.Contains(h.draw(100, 20), "no keys on the broker") {
		t.Fatal("the refusal is not on screen")
	}
	if h.p.closed {
		t.Fatal("a refusal closed the picker")
	}
	h.key(key(tcell.KeyDown))
	if st, _ := m.statusLine(); st != "" {
		t.Fatalf("a key did not clear the status: %q", st)
	}
	if strings.Contains(h.draw(100, 20), "no keys on the broker") {
		t.Fatal("the refusal outlived the next key")
	}
}

func TestPickEscCloses(t *testing.T) {
	h := newPickHarness(t, newPickModel("/cfg", pickRows("demo"), nil), brokerKeys("demo"))
	h.draw(80, 20)
	h.key(key(tcell.KeyEscape))
	if !h.p.closed || len(h.ran) != 0 {
		t.Fatalf("esc: closed %v ran %v", h.p.closed, h.ran)
	}
}

func TestPickLookup(t *testing.T) {
	for _, c := range []struct {
		ev   *tcell.EventKey
		want Action
	}{
		{key(tcell.KeyUp), ActUp}, {key(tcell.KeyDown), ActDown}, {key(tcell.KeyEnter), ActOpen},
		{key(tcell.KeyEscape), ActClose}, {runeKey('q'), ActPickType}, {runeKey('j'), ActPickType},
		{key(tcell.KeyBackspace2), ActPickType}, {tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl), ActQuit},
	} {
		if got := lookup(ModePick, PanelAgents, TierWide, keyOf(c.ev)); got != c.want {
			t.Errorf("%v: %v, want %v", c.ev.Name(), got, c.want)
		}
	}
	if f := footerHints(ModePick, PanelAgents, TierWide, 120).text(); !strings.Contains(f, "enter open") || !strings.Contains(f, "esc close") {
		t.Fatalf("footer %q", f)
	}
}

// TestPickRunsAboveTheProjectExit runs main in a child process (the only way
// to see an os.Exit) with no project and an unreachable broker.
func TestPickRunsAboveTheProjectExit(t *testing.T) {
	if os.Getenv("BUSMON_PICK_MAIN_HELPER") == "1" {
		return
	}
	for _, args := range [][]string{{"--pick"}, {"--pick", "--reset", "--yes"}, {"--reset", "--yes", "--pick"}, {"--pick", "--delete", "demo", "--yes"}} {
		dir := t.TempDir()
		cmd := exec.Command(os.Args[0], "-test.run=^TestPickMainHelper$")
		cmd.Env = append(cleanEnv(), "BUSMON_PICK_MAIN_HELPER=1", "BUSMON_PICK_ARGS="+strings.Join(args, " "),
			"HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir,
			"REDIS_URL=unix://"+filepath.Join(dir, "no-broker.sock"), "AGENT_BUS_NO_UPDATE_CHECK=1")
		out, err := cmd.CombinedOutput()
		s := string(out)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, s)
		}
		if !strings.Contains(s, "PICKER RAN") {
			t.Fatalf("%v: the picker did not run:\n%s", args, s)
		}
		for _, bad := range []string{"project required", "Purged", "Deleted", "Redis connection failed"} {
			if strings.Contains(s, bad) {
				t.Fatalf("%v: %q in output:\n%s", args, bad, s)
			}
		}
	}
}

func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "AGENT_BUS_PROJECT", "REDIS_URL", "REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "AGENT_BUS_HUMAN", "AGENTBUS_PROJECTS_DIR":
			continue
		}
		env = append(env, kv)
	}
	return env
}

func TestPickMainHelper(t *testing.T) {
	if os.Getenv("BUSMON_PICK_MAIN_HELPER") != "1" {
		t.Skip("helper for TestPickRunsAboveTheProjectExit")
	}
	runPicker = func(host, theme string) int { fmt.Println("PICKER RAN"); return 0 }
	os.Args = append([]string{"busmon"}, strings.Fields(os.Getenv("BUSMON_PICK_ARGS"))...)
	main()
}

// pickAlignCases are the picker's fixtures in TestAlignmentSuite.
func pickAlignCases() []struct {
	name  string
	model func() *pickModel
	setup func(h *pickHarness)
} {
	hostileRows := func() []projlist.Row {
		return []projlist.Row{
			{Name: "alpha", WorkingDir: "/data/" + hostileText, Broker: projlist.Present},
			{Name: "b" + strings.Repeat("x", 31), WorkingDir: hostileText + strings.Repeat("/deep", 30), Broker: projlist.Absent},
			{Name: "gamma", WorkingDir: "", Broker: projlist.Unknown},
		}
	}
	return []struct {
		name  string
		model func() *pickModel
		setup func(h *pickHarness)
	}{
		{"pick empty dir", func() *pickModel {
			m := newPickModel("/home/"+hostileText+"/.config/agentbus/projects", nil, nil)
			m.setAnnotated(nil, nil)
			return m
		}, func(*pickHarness) {}},
		{"pick broker down", func() *pickModel {
			m := newPickModel("/cfg", nil, nil)
			rows := hostileRows()
			for i := range rows {
				rows[i].Broker = projlist.Unknown
			}
			m.setAnnotated(rows, errors.New(hostileText))
			return m
		}, func(h *pickHarness) { h.key(key(tcell.KeyEnter)) }},
		{"pick 3 rows", func() *pickModel {
			m := newPickModel("/cfg", nil, nil)
			m.setAnnotated(hostileRows(), nil)
			return m
		}, func(h *pickHarness) { h.key(key(tcell.KeyDown)); h.key(runeKey('a')) }},
		{"pick busy", func() *pickModel {
			m := newPickModel("/cfg", nil, nil)
			m.setAnnotated(hostileRows(), nil)
			m.busy = "agentbus up " + hostileText
			return m
		}, func(*pickHarness) {}},
		{"pick reason", func() *pickModel {
			m := newPickModel("/cfg", hostileRows(), nil)
			m.setStatus(hostileText, false)
			return m
		}, func(*pickHarness) {}},
	}
}

// checkPick is the picker's part of TestAlignmentSuite.
func checkPick(t *testing.T, palette string) {
	t.Helper()
	for _, c := range pickAlignCases() {
		for _, sz := range alignSizes {
			name := fmt.Sprintf("%s/%s/%dx%d", palette, c.name, sz[0], sz[1])
			h := newPickHarness(t, c.model(), func(context.Context, string) ([]string, error) { return nil, errors.New(hostileText) })
			h.draw(sz[0], sz[1])
			c.setup(h)
			screen := h.draw(sz[0], sz[1])
			x, y, w, hh := h.p.list.GetRect()
			checkFrame(t, h.sim, name+" list", Rect{x, y, w, hh}, true)
			if x != 0 || w != sz[0] || y+hh != sz[1]-1 {
				t.Errorf("%s: list frame %v does not span the screen above the footer", name, Rect{x, y, w, hh})
			}
			lines := strings.Split(screen, "\n")
			if !strings.Contains(lines[0], "Projects") {
				t.Errorf("%s: header lacks the title: %q", name, lines[0])
			}
			if c.name == "pick broker down" && sz[0] >= 40 && !strings.Contains(lines[0], "broker unreachable") {
				t.Errorf("%s: header does not say the broker is unreachable: %q", name, lines[0])
			}
			if c.name == "pick broker down" && !strings.Contains(screen, "?") {
				t.Errorf("%s: rows are not marked ?", name)
			}
			if c.name == "pick empty dir" && hh >= 3 && sz[0] >= 40 && !strings.Contains(screen, "no project templates") {
				t.Errorf("%s: the empty directory is not explained", name)
			}
			rec := newRecScreen(t, sz[0], sz[1], Rect{0, 0, sz[0], sz[1]})
			h.p.view.Draw(rec)
			for _, b := range rec.bad {
				t.Errorf("%s: write off screen %s", name, b)
			}
			if strings.ContainsAny(screen, "\x1b\t\u202e") {
				t.Errorf("%s: control characters reached the screen", name)
			}
			if t.Failed() {
				t.Logf("%s:\n%s", name, screen)
				return
			}
		}
	}
}

// A picker that stops waiting (timeout, esc, exit) leaves up to finish: no
// pipe for it to die on, no tie to the picker's session, the log kept and
// named.
func TestUpRunnerOutlivesTheWait(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	marker := filepath.Join(dir, "finished")
	fake := filepath.Join(dir, "agentbus")
	script := "#!/bin/sh\nsleep 0.3\necho still provisioning\necho late error >&2\ntouch " + marker + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The runner runs in a child that exits as soon as its wait runs out, as
	// busmon does: whatever the runner held (pipes, session) goes with it.
	cmd := exec.Command(os.Args[0], "-test.run=^TestUpRunnerHelper$")
	cmd.Env = append(os.Environ(), "BUSMON_UP_HELPER="+fake)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "left to finish") {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	logs, _ := filepath.Glob(filepath.Join(tmp, "agentbus-up-*.log"))
	if len(logs) != 1 || !strings.Contains(string(out), logs[0]) {
		t.Fatalf("the log %v is not named in %q", logs, out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("up did not finish after the picker stopped waiting")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if b, _ := os.ReadFile(logs[0]); !strings.Contains(string(b), "late error") {
		t.Fatalf("its stderr did not reach the log: %q", b)
	}
}

func TestUpRunnerHelper(t *testing.T) {
	fake := os.Getenv("BUSMON_UP_HELPER")
	if fake == "" {
		t.Skip("helper for TestUpRunnerOutlivesTheWait")
	}
	fmt.Println(upRunner(fake, 50*time.Millisecond)("demo"))
	os.Exit(0)
}

// A finished up leaves no log behind.
func TestUpRunnerRemovesItsLog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	fake := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := upRunner(fake, 5*time.Second)("demo"); err == nil || err.Error() != "boom" {
		t.Fatalf("err %v", err)
	}
	if logs, _ := filepath.Glob(filepath.Join(dir, "agentbus-up-*.log")); len(logs) != 0 {
		t.Fatalf("logs left: %v", logs)
	}
}
