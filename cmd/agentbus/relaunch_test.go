package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

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
		{"dead, the shell runs something", true, lifeDead, true, busy, true, verdictBusy},
		{"pane unreadable", true, lifeDead, true, paneFacts{}, true, verdictUnknown},
		{"no tab: created", true, lifeDead, false, paneFacts{}, true, verdictCreate},
		{"no tab, not asked to create", true, lifeDead, false, paneFacts{}, false, verdictMissing},
		{"busmon still runs", false, lifeUnknown, true, busy, true, verdictRunning},
		{"busmon gone", false, lifeUnknown, true, shell, true, verdictRelaunch},
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
	tabs  string            // JSON tabs
	panes string            // JSON panes
	fg    map[string]string // pane -> foreground command line (split into argv)
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
		// "bash" waits at its prompt; "bash @busy" is the same argv running a
		// builtin (its terminal canonical). The pid encodes which.
		pid := 100
		if strings.HasSuffix(cmdline, " @busy") {
			cmdline, pid = strings.TrimSuffix(cmdline, " @busy"), 200
		}
		argv, _ := json.Marshal(strings.Fields(cmdline))
		return []byte(`{"result":{"process_info":{"foreground_processes":[{"pid":` + strconv.Itoa(pid) + `,"argv":` + string(argv) + `}]}}}`), nil
	case strings.HasPrefix(line, "tab create --workspace w4"):
		return []byte(`{"result":{"root_pane":{"pane_id":"w4:p9"}}}`), nil
	}
	return []byte(`{"result":{}}`), nil
}

func (f *relaunchHerdr) ran() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "pane run ") || strings.HasPrefix(c, "tab create ") {
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
	err := relaunchOpen(h.run, snaps, true, ps, "demo", "w4", relaunchTemplate(), relaunchOptions{Resume: true, Create: true, AtPrompt: fakePrompt}, &out)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pane run w4:p3 /r/scripts/agent-launch master demo", // master first
		"pane run w4:p2 /r/scripts/agent-launch coder demo --resume 93ff25bd-34d8-4077-bf6c-65e6cb135bbf",
		"tab create --workspace w4 --label sentinel --no-focus",
		"pane run w4:p9 /r/scripts/agent-launch sentinel demo",
	}
	if got := h.ran(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, w := range []string{"master: relaunched (fresh", "busmon: running", "coder: relaunched (resumed)", "foureyes: alive", "sentinel: relaunched (new tab)"} {
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
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", relaunchTemplate(), relaunchOptions{DryRun: true, Create: true, AtPrompt: fakePrompt}, &out); err != nil {
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
	if err := relaunchOpen(h.run, snaps, true, ps, "demo", "w4", tpl, relaunchOptions{Create: true, AtPrompt: fakePrompt}, &out); err != nil {
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
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{AtPrompt: fakePrompt}, &out); err != nil {
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
	if !strings.Contains(got, "pane run w4:p3 env ROLES_TOML=/r/roles.toml /r/scripts/agent-launch master demo") ||
		!strings.Contains(got, "pane run w4:p1 /r/busmon --project demo") || locked != 1 {
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
	dir := t.TempDir()
	lock := upLock(dir)
	unlock, err := lock("demo", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock("demo", false); !errors.Is(err, errUpBusy) {
		t.Fatalf("a hook during an up: %v", err)
	}
	if u, err := lock("other", false); err != nil {
		t.Fatalf("another project is locked: %v", err)
	} else {
		u()
	}
	unlock()
	if _, err := lock("demo", false); !errors.Is(err, errUpFresh) {
		t.Fatalf("the hook right after an up ended: %v", err)
	}
	u2, err := lock("demo", true)
	if err != nil {
		t.Fatalf("an up after an up: %v", err)
	}
	u2()
}

// First use (an empty lock file, whoever created it) is not a recent up: only
// an unlock writes the end time, and always under the lock.
func TestUpLockFirstUseIsNotFresh(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/up-demo.lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	u, err := upLock(dir)("demo", false)
	if err != nil {
		t.Fatalf("first use: %v", err)
	}
	u()
}

// No lock, no pane: an unwritable cache refuses both up and the hook.
func TestUpLockFailsClosed(t *testing.T) {
	dir := t.TempDir()
	blocker := dir + "/file"
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lock := upLock(blocker + "/cache") // a file where the directory should be
	for _, wait := range []bool{true, false} {
		if _, err := lock("demo", wait); err == nil || errors.Is(err, errUpBusy) || errors.Is(err, errUpFresh) {
			t.Errorf("wait=%v: %v", wait, err)
		}
	}
	work, _, h, _, deps := upFixture(t)
	deps.Lock = lock
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 1 || len(h.calls) != 0 {
		t.Fatalf("up without its lock: code %d calls %q", code, h.calls)
	}
	if code := upHook(deps, "demo", "w4", &out, &errw); code != 1 || len(h.calls) != 0 {
		t.Fatalf("hook without its lock: code %d calls %q", code, h.calls)
	}
}

// The workspace hook starts a stopped service as installed (no Install, so a
// unit is never rewritten), leaves a running one, and relaunches without
// creating tabs.
func TestUpHookStartsStoppedServicesAndRelaunches(t *testing.T) {
	_, templates, _, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	u.m.specs["demo/dispatch"] = service.Spec{Project: "demo", Kind: "dispatch"}
	u.m.specs["demo/missions"] = service.Spec{Project: "demo", Kind: "missions"}
	u.m.active["demo/missions"] = true
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"demo\"\n[[tabs]]\nname = \"master\"\ncommand = \"agent-launch master demo\"\n" +
		"[[tabs]]\nname = \"coder\"\ncommand = \"agent-launch coder demo\"\n" +
		"[[tabs]]\nname = \"dispatch\"\ncommand = \"/r/agentbus --project demo dispatch\"\n"
	if err := os.WriteFile(templates+"/demo.toml", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &relaunchHerdr{tabs: `{"label":"master","tab_id":"w4:t3"}`, panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"}`, fg: map[string]string{"w4:p3": "bash"}}
	deps.Herdr = h.run
	var out, errw bytes.Buffer
	if code := upHook(deps, "demo", "w4", &out, &errw); code != 0 {
		t.Fatalf("hook = %d: %s", code, errw.String())
	}
	if got := strings.Join(u.m.calls, ","); got != "start demo dispatch" {
		t.Fatalf("manager calls = %q", got)
	}
	if got := strings.Join(h.ran(), ","); got != "pane run w4:p3 agent-launch master demo" {
		t.Fatalf("herdr = %q", got)
	}
	if len(*boot) != 0 || !strings.Contains(out.String(), "coder: missing") || !strings.Contains(out.String(), "dispatch: started") {
		t.Fatalf("boot %q out %s", *boot, out.String())
	}
}

func TestUpHookDoesNothingWhileAnUpRuns(t *testing.T) {
	_, _, _, _, deps := upFixture(t)
	h := &relaunchHerdr{}
	deps.Herdr = h.run
	deps.Lock = func(string, bool) (func(), error) { return nil, errUpBusy }
	var out, errw bytes.Buffer
	if code := upHook(deps, "demo", "w4", &out, &errw); code != 0 || len(h.calls) != 0 {
		t.Fatalf("code %d calls %q", code, h.calls)
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
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{Create: true, AtPrompt: fakePrompt}, &out); err != nil {
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
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{Create: true, AtPrompt: fakePrompt}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 || !strings.Contains(out.String(), "master: unknown (several tabs") {
		t.Fatalf("calls %q out %s", got, out.String())
	}
}

// fakePrompt is the kernel's answer in the fakes: pid 100 waits at its prompt.
func fakePrompt(pid int, _ string) (bool, error) { return pid == 100, nil }

// Codex's round-2 case: an interactive shell running a builtin (read) keeps
// its argv; only the kernel's terminal mode tells it from a prompt.
func TestRelaunchOpenNeverTypesIntoAShellRunningABuiltin(t *testing.T) {
	h := &relaunchHerdr{
		tabs:  `{"label":"master","tab_id":"w4:t3"}`,
		panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"}`,
		fg:    map[string]string{"w4:p3": "sh -i @busy"},
	}
	tpl := upTemplate{Tabs: []upTab{{Name: "master", Command: "agent-launch master demo"}}}
	var out bytes.Buffer
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{Create: true, AtPrompt: fakePrompt}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 || !strings.Contains(out.String(), "master: skipped (busy)") {
		t.Fatalf("calls %q out %s", got, out.String())
	}
}

// The prompt check failing (no /proc, not Linux) is unknown, never a relaunch.
func TestRelaunchOpenUnknownWhenThePromptCannotBeRead(t *testing.T) {
	h := &relaunchHerdr{
		tabs:  `{"label":"master","tab_id":"w4:t3"}`,
		panes: `{"pane_id":"w4:p3","tab_id":"w4:t3"}`,
		fg:    map[string]string{"w4:p3": "bash"},
	}
	tpl := upTemplate{Tabs: []upTab{{Name: "master", Command: "agent-launch master demo"}}}
	fail := func(int, string) (bool, error) { return false, errors.New("no /proc") }
	var out bytes.Buffer
	if err := relaunchOpen(h.run, nil, false, nil, "demo", "w4", tpl, relaunchOptions{Create: true, AtPrompt: fail}, &out); err != nil {
		t.Fatal(err)
	}
	if got := h.ran(); len(got) != 0 || !strings.Contains(out.String(), "master: unknown") {
		t.Fatalf("calls %q out %s", got, out.String())
	}
}
