package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

func TestSnapLife(t *testing.T) {
	starts := map[int]uint64{10: 500}
	ps := func(pid int) (uint64, error) {
		if s, ok := starts[pid]; ok {
			return s, nil
		}
		if pid == 13 {
			return 0, errors.New("permission denied")
		}
		return 0, os.ErrNotExist
	}
	for _, c := range []struct {
		name string
		s    bus.AgentSnapshot
		has  bool
		ps   func(int) (uint64, error)
		want life
	}{
		{"no snapshot", bus.AgentSnapshot{}, false, ps, lifeUnknown},
		{"no pid", bus.AgentSnapshot{State: "idle"}, true, ps, lifeUnknown},
		{"runs with its start", bus.AgentSnapshot{PID: 10, PIDStart: 500}, true, ps, lifeAlive},
		{"pid reused", bus.AgentSnapshot{PID: 10, PIDStart: 499}, true, ps, lifeDead},
		{"pid gone", bus.AgentSnapshot{PID: 11, PIDStart: 500}, true, ps, lifeDead},
		{"unreadable", bus.AgentSnapshot{PID: 13, PIDStart: 500}, true, ps, lifeUnknown},
		{"not on Linux", bus.AgentSnapshot{PID: 10, PIDStart: 500}, true, nil, lifeUnknown},
	} {
		if got := snapLife(c.s, c.has, c.ps); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestDecideRelaunch(t *testing.T) {
	shell := paneFacts{Known: true, Shell: true}
	busy := paneFacts{Known: true}
	withAgent := paneFacts{Known: true, Agent: "claude"}
	for _, c := range []struct {
		name    string
		agent   bool
		snap    life
		hasPane bool
		pane    paneFacts
		create  bool
		want    relaunchVerdict
	}{
		{"alive by its snapshot", true, lifeAlive, true, shell, true, verdictAlive},
		{"alive elsewhere, no tab", true, lifeAlive, false, paneFacts{}, true, verdictAlive},
		{"dead in a bare shell", true, lifeDead, true, shell, true, verdictRelaunch},
		{"no snapshot, bare shell", true, lifeUnknown, true, shell, true, verdictRelaunch},
		{"no snapshot, herdr sees an agent", true, lifeUnknown, true, withAgent, true, verdictAlive},
		{"dead, another agent in its pane", true, lifeDead, true, withAgent, true, verdictBusy},
		{"dead, but something runs in its tab", true, lifeDead, true, busy, true, verdictBusy},
		{"no snapshot, something runs: no proof", true, lifeUnknown, true, busy, true, verdictBusy},
		{"pane unreadable", true, lifeDead, true, paneFacts{}, true, verdictUnknown},
		{"no tab: created", true, lifeDead, false, paneFacts{}, true, verdictCreate},
		{"no tab, not asked to create", true, lifeDead, false, paneFacts{}, false, verdictMissing},
		{"busmon still runs", false, lifeUnknown, true, busy, true, verdictRunning},
		{"busmon gone", false, lifeUnknown, true, shell, true, verdictRelaunch},
		{"an agent where busmon was", false, lifeUnknown, true, withAgent, true, verdictRunning},
	} {
		if got := decideRelaunch(c.agent, c.snap, c.hasPane, c.pane, c.create); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLaunchRole(t *testing.T) {
	for cmd, want := range map[string]string{
		"env ROLES_TOML=/r/roles.toml /r/scripts/agent-launch master demo": "master",
		"agent-launch coder demo --profile x":                              "coder",
		"agent-launch coder other":                                         "",
		"/r/busmon --project demo":                                         "",
		"agent-launch":                                                     "",
		"agent-launch BAD demo":                                            "",
	} {
		got, ok := launchRole(cmd, "demo")
		if got != want || ok != (want != "") {
			t.Errorf("%q: %q %v, want %q", cmd, got, ok, want)
		}
	}
}

func TestResumeCommand(t *testing.T) {
	cmd := "agent-launch master demo"
	id := "93ff25bd-34d8-4077-bf6c-65e6cb135bbf"
	if got := resumeCommand(cmd, bus.AgentSnapshot{Session: id, Client: "claude-code"}, true); got != cmd+" --resume "+id {
		t.Fatalf("claude: %q", got)
	}
	for name, s := range map[string]bus.AgentSnapshot{
		"no session":   {Client: "claude-code"},
		"other client": {Session: id, Client: "codex"},
		"odd id":       {Session: id + "; rm -rf ~", Client: "claude-code"},
	} {
		if got := resumeCommand(cmd, s, true); got != "" {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := resumeCommand(cmd, bus.AgentSnapshot{Session: id}, false); got != "" {
		t.Errorf("no snapshot: %q", got)
	}
}

// relaunchHerdr answers tab list, pane list and process-info for one open
// workspace w4 and records every call.
type relaunchHerdr struct {
	calls []string
	made  map[string]bool   // pane ids this fake returned from a split or a tab create
	tabs  string            // JSON tabs
	panes string            // JSON panes
	fg    map[string]string // pane -> foreground command line (split into argv)

	closeErr error // what pane close answers
}

func (f *relaunchHerdr) run(args ...string) ([]byte, error) {
	line := strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch {
	case strings.HasPrefix(line, "tab list --workspace w4"):
		return []byte(`{"result":{"tabs":[` + f.tabs + `]}}`), nil
	case strings.HasPrefix(line, "pane list --workspace w4"):
		return []byte(`{"result":{"panes":[` + f.panes + `]}}`), nil
	case strings.HasPrefix(line, "pane process-info --pane "):
		cmdline, ok := f.fg[args[3]]
		if !ok {
			return nil, errors.New("no such pane")
		}
		argv, _ := json.Marshal(strings.Fields(cmdline))
		return []byte(`{"result":{"process_info":{"foreground_processes":[{"argv":` + string(argv) + `}]}}}`), nil
	case strings.HasPrefix(line, "tab create --workspace w4"):
		f.made_("w4:p9")
		return []byte(`{"result":{"root_pane":{"pane_id":"w4:p9"}}}`), nil
	case strings.HasPrefix(line, "pane close "):
		return []byte(`{"result":{}}`), f.closeErr
	case strings.HasPrefix(line, "pane split "):
		f.made_(args[2] + "s")
		return []byte(`{"result":{"pane":{"pane_id":"` + args[2] + `s"}}}`), nil
	}
	return []byte(`{"result":{}}`), nil
}

func (f *relaunchHerdr) ran() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "pane run ") || strings.HasPrefix(c, "tab create ") || strings.HasPrefix(c, "pane split ") {
			out = append(out, c)
		}
	}
	return out
}

func relaunchTemplate() upTemplate {
	return upTemplate{Tabs: []upTab{
		{Name: "busmon", Command: "/r/busmon --project demo"},
		{Name: "coder", Command: "/r/scripts/agent-launch coder demo"},
		{Name: "master", Command: "/r/scripts/agent-launch master demo"},
		{Name: "foureyes", Command: "/r/scripts/agent-launch foureyes demo"},
		{Name: "sentinel", Command: "/r/scripts/agent-launch sentinel demo"},
	}}
}

func TestRelaunchOpenRelaunchesOnlyWhatIsGone(t *testing.T) {
	h := &relaunchHerdr{
		tabs: `{"label":"busmon","tab_id":"w4:t1"},{"label":"coder","tab_id":"w4:t2"},{"label":"master","tab_id":"w4:t3"},{"label":"foureyes","tab_id":"w4:t4"}`,
		panes: `{"pane_id":"w4:p1","tab_id":"w4:t1"},{"pane_id":"w4:p2","tab_id":"w4:t2"},{"pane_id":"w4:p3","tab_id":"w4:t3"},` +
			`{"pane_id":"w4:p4","tab_id":"w4:t4","agent":"codex"}`,
		fg: map[string]string{"w4:p1": "busmon", "w4:p2": "bash", "w4:p3": "-zsh"},
	}
	snaps := map[string]bus.AgentSnapshot{
		"coder":  {PID: 20, PIDStart: 1, Session: "93ff25bd-34d8-4077-bf6c-65e6cb135bbf", Client: "claude-code"}, // dead
		"master": {PID: 30, PIDStart: 7},                                                                         // dead, no session
	}
	ps := func(int) (uint64, error) { return 0, os.ErrNotExist }
	var out bytes.Buffer
	err := relaunchOpen(h.run, snaps, true, ps, "demo", "w4", relaunchTemplate(), relaunchOptions{Resume: true, Create: true}, &out)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pane split w4:p3 --direction right", // master first, in a new pane
		"pane run w4:p3s /r/scripts/agent-launch master demo",
		"pane split w4:p2 --direction right",
		"pane run w4:p2s /r/scripts/agent-launch coder demo --resume 93ff25bd-34d8-4077-bf6c-65e6cb135bbf",
		"tab create --workspace w4 --label sentinel --no-focus",
		"pane run w4:p9 /r/scripts/agent-launch sentinel demo",
	}
	if got := h.ran(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if bad := h.typedIntoExisting(); len(bad) != 0 {
		t.Fatalf("typed into an existing pane: %q", bad)
	}
	for _, w := range []string{"master: relaunched (new pane; the old one is left as it is) (fresh", "busmon: running", "coder: relaunched (new pane; the old one is left as it is) (resumed)", "foureyes: alive", "sentinel: relaunched (new tab)"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
}

func TestRelaunchOpenDryRunTypesNothing(t *testing.T) {
	h := &relaunchHerdr{
		tabs:  `{"label":"master","tab_id":"w4:t3"}`,
		panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"}`,
		fg:    map[string]string{"w4:p3": "bash"},
	}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", relaunchTemplate(), relaunchOptions{DryRun: true, Create: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 {
		t.Fatalf("a dry run typed or created: %q", got)
	}
	if !strings.Contains(out.String(), "master: would relaunch") || !strings.Contains(out.String(), "coder: would relaunch (new tab)") {
		t.Fatalf("out = %s", out.String())
	}
}

// A live agent is never typed into, even when herdr does not detect it.
func TestRelaunchOpenLeavesALiveAgent(t *testing.T) {
	h := &relaunchHerdr{
		tabs:  `{"label":"master","tab_id":"w4:t3"}`,
		panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"}`,
		fg:    map[string]string{"w4:p3": "bash"},
	}
	snaps := map[string]bus.AgentSnapshot{"master": {PID: 30, PIDStart: 7}}
	ps := func(int) (uint64, error) { return 7, nil }
	tpl := upTemplate{Tabs: []upTab{{Name: "master", Command: "agent-launch master demo"}}}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, snaps, true, ps, "demo", "w4", tpl, relaunchOptions{Create: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 || !strings.Contains(out.String(), "master: alive") {
		t.Fatalf("calls %q out %s", got, out.String())
	}
}

// Without the create option (the workspace hook), a missing tab is reported,
// never recreated: a retired role stays retired.
func TestRelaunchOpenWithoutCreateLeavesMissingTabs(t *testing.T) {
	h := &relaunchHerdr{}
	tpl := upTemplate{Tabs: []upTab{{Name: "coder", Command: "agent-launch coder demo"}}}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 || !strings.Contains(out.String(), "coder: missing") {
		t.Fatalf("calls %q out %s", got, out.String())
	}
}

func TestUpRelaunchesInAnOpenWorkspace(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	h := &relaunchHerdr{
		tabs:  `{"label":"busmon","tab_id":"w4:t1"},{"label":"master","tab_id":"w4:t3"}`,
		panes: `{"pane_id":"w4:p1","tab_id":"w4:t1"},{"pane_id":"w4:p3","tab_id":"w4:t3"}`,
		fg:    map[string]string{"w4:p1": "bash", "w4:p3": "bash"},
	}
	deps.Herdr = func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "workspace list" {
			return []byte(`{"result":{"workspaces":[{"label":"demo","workspace_id":"w4"}]}}`), nil
		}
		return h.run(args...)
	}
	locked := 0
	deps.Lock = func(string, bool) (func(), error) { locked++; return func() {}, nil }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--resume"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	got := strings.Join(h.ran(), "\n")
	if !strings.Contains(got, "pane split w4:p3 --direction right --cwd "+work) ||
		!strings.Contains(got, "pane run w4:p3s env ROLES_TOML=/r/roles.toml /r/scripts/agent-launch master demo") ||
		!strings.Contains(got, "pane run w4:p1s /r/busmon --project demo") || locked != 1 {
		t.Fatalf("calls:\n%s\nlocked %d out %s", got, locked, out.String())
	}
}

func TestUpDryRunProvisionsNothing(t *testing.T) {
	_, templates, _, boot, deps := upFixture(t)
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"demo\"\n[[tabs]]\nname = \"master\"\ncommand = \"agent-launch master demo\"\n"
	if err := os.WriteFile(templates+"/demo.toml", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &relaunchHerdr{tabs: `{"label":"master","tab_id":"w4:t3"}`, panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"}`, fg: map[string]string{"w4:p3": "bash"}}
	deps.Herdr = func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "workspace list" {
			return []byte(`{"result":{"workspaces":[{"label":"demo","workspace_id":"w4"}]}}`), nil
		}
		return h.run(args...)
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", "--dry-run"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if len(*boot) != 0 || len(h.ran()) != 0 || strings.Contains(strings.Join(h.calls, " "), "workspace focus") {
		t.Fatalf("boot %q calls %q", *boot, h.calls)
	}
	if !strings.Contains(out.String(), "master: would relaunch") {
		t.Fatalf("out = %s", out.String())
	}
}

func TestUpLockExcludesASecondUp(t *testing.T) {
	lock := upLock(t.TempDir())
	unlock, err := lock("demo", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock("demo", false); !errors.Is(err, errUpBusy) {
		t.Fatalf("a second up during an up: %v", err)
	}
	if u, err := lock("other", false); err != nil {
		t.Fatalf("another project is locked: %v", err)
	} else {
		u()
	}
	unlock()
	u2, err := lock("demo", false)
	if err != nil {
		t.Fatalf("an up after an up: %v", err)
	}
	u2()
}

// No lock, no pane: an unwritable cache refuses up.
func TestUpLockFailsClosed(t *testing.T) {
	blocker := t.TempDir() + "/file"
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lock := upLock(blocker + "/cache") // a file where the directory should be
	for _, wait := range []bool{true, false} {
		if _, err := lock("demo", wait); err == nil || errors.Is(err, errUpBusy) {
			t.Errorf("wait=%v: %v", wait, err)
		}
	}
	work, _, h, _, deps := upFixture(t)
	deps.Lock = lock
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 1 || len(h.calls) != 0 {
		t.Fatalf("up without its lock: code %d calls %q", code, h.calls)
	}
}

// The workspace hook starts a stopped service as installed (no Install, so a
// unit is never rewritten), leaves a running one, and touches no pane.
func TestUpHookStartsStoppedServicesOnly(t *testing.T) {
	_, _, _, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	u.m.specs["demo/dispatch"] = service.Spec{Project: "demo", Kind: "dispatch"}
	u.m.specs["demo/missions"] = service.Spec{Project: "demo", Kind: "missions"}
	u.m.active["demo/missions"] = true
	h := &relaunchHerdr{}
	deps.Herdr = h.run
	var out, errw bytes.Buffer
	if code := upHook(deps, "demo", "w4", &out, &errw); code != 0 {
		t.Fatalf("hook = %d: %s", code, errw.String())
	}
	if got := strings.Join(u.m.calls, ","); got != "start demo dispatch" {
		t.Fatalf("manager calls = %q", got)
	}
	if len(h.calls) != 0 || len(*boot) != 0 || !strings.Contains(out.String(), "dispatch started") {
		t.Fatalf("herdr %q boot %q out %s", h.calls, *boot, out.String())
	}
}

func TestIdleShell(t *testing.T) {
	for argv, want := range map[string]bool{
		"bash": true, "-bash": true, "/bin/zsh -l": true, "fish --login": true, "bash -i": true,
		"bash -c read": false, "bash script.sh": false, "sh -c x": false, "bash --rcfile x": false,
		"vim": false, "": false, "python3": false,
	} {
		if got := idleShell(strings.Fields(argv)); got != want {
			t.Errorf("%q: %v, want %v", argv, got, want)
		}
	}
}

// A shell running a command reads its input: never type a launch line into it.
func TestRelaunchOpenNeverTypesIntoAShellRunningACommand(t *testing.T) {
	h := &relaunchHerdr{
		tabs:  `{"label":"master","tab_id":"w4:t3"}`,
		panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"}`,
		fg:    map[string]string{"w4:p3": "bash -c read"},
	}
	tpl := upTemplate{Tabs: []upTab{{Name: "master", Command: "agent-launch master demo"}}}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{Create: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 || !strings.Contains(out.String(), "master: skipped (busy)") {
		t.Fatalf("calls %q out %s", got, out.String())
	}
}

// Two tabs of one label: which holds the role is unknown, so neither is typed into.
func TestRelaunchOpenLeavesDuplicateLabels(t *testing.T) {
	h := &relaunchHerdr{
		tabs:  `{"label":"master","tab_id":"w4:t3"},{"label":"master","tab_id":"w4:t5"}`,
		panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"},{"pane_id":"w4:p5","tab_id":"w4:t5","agent":"claude"}`,
		fg:    map[string]string{"w4:p3": "bash"},
	}
	tpl := upTemplate{Tabs: []upTab{{Name: "master", Command: "agent-launch master demo"}}}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{Create: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 || !strings.Contains(out.String(), "master: unknown (several tabs") {
		t.Fatalf("calls %q out %s", got, out.String())
	}
}

func (f *relaunchHerdr) made_(id string) {
	if f.made == nil {
		f.made = map[string]bool{}
	}
	f.made[id] = true
}

// The invariant of every relaunch: text goes only into a pane this fake has
// just returned from a split or a tab create, never into one that existed.
func (f *relaunchHerdr) typedIntoExisting() []string {
	var bad []string
	for _, c := range f.calls {
		if p, ok := strings.CutPrefix(c, "pane run "); ok {
			pane, _, _ := strings.Cut(p, " ")
			if !f.made[pane] {
				bad = append(bad, c)
			}
		}
	}
	return bad
}

// Codex's rounds 2 to 4: the old pane of a dead agent never receives text.
// A plain shell there means its command is gone: the agent comes back in a new
// pane beside it. Anything else running there (a builtin read, an editor, an
// agent herdr does not detect) makes the tab busy: nothing is done.
func TestRelaunchOpenLeavesTheOldPaneOfADeadAgentAlone(t *testing.T) {
	for fg, want := range map[string]string{
		"bash":         "pane split w4:p3 --direction right,pane run w4:p3s agent-launch master demo",
		"/bin/bash -i": "pane split w4:p3 --direction right,pane run w4:p3s agent-launch master demo",
		"bash -c read": "",
		"vim notes":    "",
	} {
		h := &relaunchHerdr{
			tabs:  `{"label":"master","tab_id":"w4:t3"}`,
			panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"}`,
			fg:    map[string]string{"w4:p3": fg},
		}
		snaps := map[string]bus.AgentSnapshot{"master": {PID: 30, PIDStart: 7}}
		ps := func(int) (uint64, error) { return 0, os.ErrNotExist }
		tpl := upTemplate{Tabs: []upTab{{Name: "master", Command: "agent-launch master demo"}}}
		var out bytes.Buffer
		if err := relaunchOpen(h.run, snaps, true, ps, "demo", "w4", tpl, relaunchOptions{Create: true}, &out); err != nil {
			t.Fatal(err)
		}
		if bad := h.typedIntoExisting(); len(bad) != 0 {
			t.Fatalf("%q: typed into a pane up did not create: %q", fg, bad)
		}
		if got := strings.Join(h.ran(), ","); got != want {
			t.Fatalf("%q: calls %q, want %q", fg, got, want)
		}
	}
}

// Codex's round 4: a tab split by an earlier relaunch holds the old shell and
// the new pane. With no snapshot and an agent herdr does not detect in the
// second pane, a second up must not start another copy. Two ups in a row.
func TestRelaunchOpenNeverDuplicatesAcrossSplitPanes(t *testing.T) {
	h := &relaunchHerdr{
		tabs:  `{"label":"master","tab_id":"w4:t3"},{"label":"busmon","tab_id":"w4:t1"}`,
		panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"},{"pane_id":"w4:p1","tab_id":"w4:t1"}`,
		fg:    map[string]string{"w4:p3": "bash", "w4:p1": "bash"},
	}
	tpl := upTemplate{Tabs: []upTab{{Name: "master", Command: "agent-launch master demo"}, {Name: "busmon", Command: "busmon --project demo"}}}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{Create: true}, &out); err != nil {
		t.Fatal(err)
	}
	if n := len(h.ran()); n != 4 { // split + run for master and for busmon
		t.Fatalf("first up: %q", h.ran())
	}
	// The layout the first up left: each tab holds its old shell first, then
	// the new pane running the command (herdr detects neither).
	h.panes = `{"pane_id":"w4:p3","tab_id":"w4:t3"},{"pane_id":"w4:p3s","tab_id":"w4:t3"},` +
		`{"pane_id":"w4:p1","tab_id":"w4:t1"},{"pane_id":"w4:p1s","tab_id":"w4:t1"}`
	h.fg["w4:p3s"], h.fg["w4:p1s"] = "python3 worker.py", "busmon --project demo"
	h.calls = nil
	out.Reset()
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{Create: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 {
		t.Fatalf("second up started another copy: %q\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "master: skipped (busy)") || !strings.Contains(out.String(), "busmon: running") {
		t.Fatalf("out = %s", out.String())
	}
}

// closes are the panes this fake was asked to close.
func (f *relaunchHerdr) closes() []string {
	var out []string
	for _, c := range f.calls {
		if p, ok := strings.CutPrefix(c, "pane close "); ok {
			out = append(out, p)
		}
	}
	return out
}

// deadMaster is a master tab whose agent died in pane w4:p3, a plain shell.
func deadMaster() (*relaunchHerdr, map[string]bus.AgentSnapshot, upTemplate) {
	h := &relaunchHerdr{
		tabs:  `{"label":"master","tab_id":"w4:t3"},{"label":"busmon","tab_id":"w4:t1"}`,
		panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"},{"pane_id":"w4:p1","tab_id":"w4:t1"}`,
		fg:    map[string]string{"w4:p3": "bash", "w4:p1": "bash"},
	}
	snaps := map[string]bus.AgentSnapshot{"master": {PID: 30, PIDStart: 7, Pane: "w4:p3"}}
	tpl := upTemplate{Tabs: []upTab{{Name: "master", Command: "agent-launch master demo"}, {Name: "busmon", Command: "busmon --project demo"}}}
	return h, snaps, tpl
}

// procs: pid 30 is gone, pid 31 runs since 9.
func procs(pid int) (uint64, error) {
	if pid == 31 {
		return 9, nil
	}
	return 0, os.ErrNotExist
}

// proofAfter answers the old snapshot n times, then the relaunched one.
func proofAfter(n int, next bus.AgentSnapshot, sleeps *int) *relaunchProof {
	reads := 0
	return &relaunchProof{
		Agents: func() (map[string]bus.AgentSnapshot, error) {
			reads++
			if reads <= n {
				return map[string]bus.AgentSnapshot{"master": {PID: 30, PIDStart: 7, Pane: "w4:p3"}}, nil
			}
			return map[string]bus.AgentSnapshot{"master": next}, nil
		},
		Sleep: func(time.Duration) { *sleeps++ },
		Tries: 5,
	}
}

// Bernard's decision (2026-10-27): once the relaunched agent is proven in its
// new pane, the old pane it replaced is closed, if it is still a plain shell
// when re-read. Nothing is typed into it, and no other pane is closed.
func TestRelaunchOpenClosesTheOldPaneOnceTheNewOneIsProven(t *testing.T) {
	h, snaps, tpl := deadMaster()
	sleeps := 0
	o := relaunchOptions{Create: true, Prove: proofAfter(2, bus.AgentSnapshot{PID: 31, PIDStart: 9, Pane: "w4:p3s"}, &sleeps)}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, snaps, true, procs, "demo", "w4", tpl, o, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.closes(); len(got) != 1 || got[0] != "w4:p3" {
		t.Fatalf("closed %q, want only w4:p3\n%s", got, out.String())
	}
	if bad := h.typedIntoExisting(); len(bad) != 0 {
		t.Fatalf("typed into an existing pane: %q", bad)
	}
	// The old pane is re-read after the launch, right before the close.
	run, recheck, closed := -1, -1, -1
	for i, c := range h.calls {
		switch {
		case strings.HasPrefix(c, "pane run w4:p3s"):
			run = i
		case c == "pane process-info --pane w4:p3":
			recheck = i
		case c == "pane close w4:p3":
			closed = i
		}
	}
	if !(run < recheck && recheck < closed) {
		t.Fatalf("run %d, recheck %d, close %d:\n%s", run, recheck, closed, strings.Join(h.calls, "\n"))
	}
	if sleeps != 2 {
		t.Errorf("slept %d times, want 2 (proof on the third read)", sleeps)
	}
	for _, w := range []string{"master: relaunched (new pane; the old one w4:p3 closes once the new one is proven)", "master: old pane w4:p3 closed", "busmon: relaunched (new pane; the old one is left as it is)"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
}

// Anything short of a proven agent and a plain shell re-read before the close
// leaves the old pane as it is, and says why.
func TestRelaunchOpenLeavesTheOldPaneWithoutProof(t *testing.T) {
	proven := bus.AgentSnapshot{PID: 31, PIDStart: 9, Pane: "w4:p3s"}
	for name, c := range map[string]struct {
		next  bus.AgentSnapshot
		snap  bus.AgentSnapshot
		later string // the old pane's foreground once the agent is proven ("" = gone)
		want  string
	}{
		"no proof":            {next: bus.AgentSnapshot{PID: 30, PIDStart: 7, Pane: "w4:p3"}, later: "bash", want: "master: old pane w4:p3 left as it is (no proof of the new agent in pane w4:p3s after 5 s)"},
		"bound elsewhere":     {next: bus.AgentSnapshot{PID: 31, PIDStart: 9, Pane: "w4:p8"}, later: "bash", want: "no proof of the new agent"},
		"new pid not alive":   {next: bus.AgentSnapshot{PID: 32, PIDStart: 9, Pane: "w4:p3s"}, later: "bash", want: "no proof of the new agent"},
		"busy at the recheck": {next: proven, later: "vim notes", want: "master: old pane w4:p3 left as it is (no longer a plain shell)"},
		"gone at the recheck": {next: proven, later: "", want: "master: old pane w4:p3 left as it is (unreadable:"},
		"not its pane":        {next: proven, snap: bus.AgentSnapshot{PID: 30, PIDStart: 7, Pane: "w9:p1"}, later: "bash", want: "master: relaunched (new pane; the old one is left as it is)"},
	} {
		t.Run(name, func(t *testing.T) {
			h, snaps, tpl := deadMaster()
			if c.snap.Pane != "" {
				snaps["master"] = c.snap
			}
			sleeps := 0
			p := proofAfter(0, c.next, &sleeps)
			read := p.Agents
			p.Agents = func() (map[string]bus.AgentSnapshot, error) {
				if c.later == "" {
					delete(h.fg, "w4:p3")
				} else {
					h.fg["w4:p3"] = c.later
				}
				return read()
			}
			var out bytes.Buffer
			if err := relaunchOpen(h.run, snaps, true, procs, "demo", "w4", tpl, relaunchOptions{Create: true, Prove: p}, &out); err != nil {
				t.Fatal(err)
			}
			if got := h.closes(); len(got) != 0 {
				t.Fatalf("closed %q", got)
			}
			if !strings.Contains(out.String(), c.want) {
				t.Fatalf("output lacks %q:\n%s", c.want, out.String())
			}
		})
	}
}

func TestRelaunchOpenDryRunSaysWhatItWouldClose(t *testing.T) {
	h, snaps, tpl := deadMaster()
	sleeps := 0
	o := relaunchOptions{DryRun: true, Create: true, Prove: proofAfter(0, bus.AgentSnapshot{}, &sleeps)}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, snaps, true, procs, "demo", "w4", tpl, o, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 || len(h.closes()) != 0 {
		t.Fatalf("a dry run acted: %q", h.calls)
	}
	if want := "master: would relaunch (new pane; would close the old one w4:p3 once the new one is proven)"; !strings.Contains(out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, out.String())
	}
}

func TestRelaunchOpenReportsAFailedClose(t *testing.T) {
	h, snaps, tpl := deadMaster()
	h.closeErr = errors.New("pane busy")
	sleeps := 0
	o := relaunchOptions{Create: true, Prove: proofAfter(0, bus.AgentSnapshot{PID: 31, PIDStart: 9, Pane: "w4:p3s"}, &sleeps)}
	var out bytes.Buffer
	err := relaunchOpen(h.run, snaps, true, procs, "demo", "w4", tpl, o, &out)
	if err == nil || !strings.Contains(out.String(), "master: old pane w4:p3 not closed: ") {
		t.Fatalf("err %v out %s", err, out.String())
	}
}

// A tab of two plain shells: only the one the agent's snapshot recorded is
// closed, never the tab's first pane by default, never the other one.
func TestRelaunchOpenClosesOnlyTheRecordedPane(t *testing.T) {
	h, snaps, tpl := deadMaster()
	h.panes = `{"pane_id":"w4:p3","tab_id":"w4:t3"},{"pane_id":"w4:p7","tab_id":"w4:t3"},{"pane_id":"w4:p1","tab_id":"w4:t1"}`
	h.fg["w4:p7"] = "zsh"
	snaps["master"] = bus.AgentSnapshot{PID: 30, PIDStart: 7, Pane: "w4:p7"}
	sleeps := 0
	o := relaunchOptions{Create: true, Prove: proofAfter(1, bus.AgentSnapshot{PID: 31, PIDStart: 9, Pane: "w4:p3s"}, &sleeps)}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, snaps, true, procs, "demo", "w4", tpl, o, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.closes(); len(got) != 1 || got[0] != "w4:p7" {
		t.Fatalf("closed %q, want only w4:p7\n%s", got, out.String())
	}
}

// up wires the proof: its own snapshot reads prove the relaunched master
// before the old pane is closed.
func TestUpClosesTheOldPaneOfAProvenRelaunch(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	h, _, _ := deadMaster()
	deps.Herdr = func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "workspace list" {
			return []byte(`{"result":{"workspaces":[{"label":"demo","workspace_id":"w4"}]}}`), nil
		}
		return h.run(args...)
	}
	reads := 0
	deps.Agents = func(string) (map[string]bus.AgentSnapshot, error) {
		reads++
		if reads == 1 {
			return map[string]bus.AgentSnapshot{"master": {PID: 30, PIDStart: 7, Pane: "w4:p3"}}, nil
		}
		return map[string]bus.AgentSnapshot{"master": {PID: 31, PIDStart: 9, Pane: "w4:p3s"}}, nil
	}
	deps.ProcStart = procs
	deps.Sleep = func(time.Duration) {}
	deps.Lock = func(string, bool) (func(), error) { return func() {}, nil }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--resume"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if got := h.closes(); len(got) != 1 || got[0] != "w4:p3" || !strings.Contains(out.String(), "master: old pane w4:p3 closed") {
		t.Fatalf("closed %q\n%s", got, out.String())
	}
	if bad := h.typedIntoExisting(); len(bad) != 0 {
		t.Fatalf("typed into an existing pane: %q", bad)
	}
}

// The proof needs a new process: the dead agent's own identity, even read
// alive again in the new pane (a pid reused with the same start, or a stale
// read), proves nothing, and the old pane stays.
func TestRelaunchOpenNeedsANewProcessAsProof(t *testing.T) {
	h, snaps, tpl := deadMaster()
	calls := 0
	ps := func(pid int) (uint64, error) {
		calls++
		if calls == 1 { // the relaunch's own judgement: dead
			return 0, os.ErrNotExist
		}
		return 7, nil
	}
	sleeps := 0
	o := relaunchOptions{Create: true, Prove: proofAfter(0, bus.AgentSnapshot{PID: 30, PIDStart: 7, Pane: "w4:p3s"}, &sleeps)}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, snaps, true, ps, "demo", "w4", tpl, o, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.closes(); len(got) != 0 || !strings.Contains(out.String(), "no proof of the new agent") {
		t.Fatalf("closed %q\n%s", got, out.String())
	}
}
