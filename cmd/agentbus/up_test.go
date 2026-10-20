package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// fakeHerdr records the calls and answers like herdr's CLI.
type fakeHerdr struct {
	calls      []string
	workspaces string // JSON list of {"label","workspace_id"} entries
	fail       string // a call prefix that fails
}

func (f *fakeHerdr) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if f.fail != "" && strings.HasPrefix(strings.Join(args, " "), f.fail) {
		return nil, errors.New("herdr failed")
	}
	switch strings.Join(args[:2], " ") {
	case "workspace list":
		return []byte(`{"result":{"workspaces":[` + f.workspaces + `]}}`), nil
	case "workspace create":
		return []byte(`{"result":{"workspace":{"workspace_id":"w9"},"tab":{"tab_id":"w9:t1"},"root_pane":{"pane_id":"w9:p1"}}}`), nil
	case "tab create":
		n := len(f.calls)
		return []byte(`{"result":{"tab":{"tab_id":"w9:t` + string(rune('0'+n)) + `"},"root_pane":{"pane_id":"w9:p` + string(rune('0'+n)) + `"}}}`), nil
	}
	return []byte(`{"result":{}}`), nil
}

func upFixture(t *testing.T) (string, string, *fakeHerdr, *[]string, upDeps) {
	t.Helper()
	dir := t.TempDir()
	templates := filepath.Join(dir, "projects")
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	h := &fakeHerdr{}
	var boot []string
	env := map[string]string{"HERDR_ENV": "1", "AGENTBUS_PROJECTS_DIR": templates}
	deps := upDeps{
		Herdr: h.run,
		Bootstrap: func(args []string) error {
			boot = append(boot, strings.Join(args, " "))
			if err := os.MkdirAll(templates, 0o755); err != nil {
				return err
			}
			// Like scripts/bootstrap: an existing template is rewritten only by
			// "new"; --no-dispatch / --no-missions omit those tabs.
			path := filepath.Join(templates, "demo.toml")
			if _, err := os.Stat(path); err == nil && args[0] != "new" {
				return nil
			}
			has := func(f string) bool { return slices.Contains(args, f) }
			body := "name = \"demo\"\nworking_dir = \"" + work + "\"\n\n[[tabs]]\nname = \"busmon\"\ncommand = \"/r/busmon --project demo\"\n\n[[tabs]]\nname = \"master\"\ncommand = \"env ROLES_TOML=/r/roles.toml /r/scripts/agent-launch master demo\"\n"
			if !has("--no-dispatch") {
				body += "\n[[tabs]]\nname = \"dispatch\"\ncommand = \"/r/agentbus --project demo dispatch\"\n"
			}
			if !has("--no-missions") {
				body += "\n[[tabs]]\nname = \"missions\"\ncommand = \"/r/agentbus --project demo missions --repo " + work + "\"\n"
			}
			return os.WriteFile(path, []byte(body), 0o644)
		},
		Env: func(k string) string { return env[k] },
	}
	return work, templates, h, &boot, deps
}

func TestUpProvisionsAndOpensBusmonFirst(t *testing.T) {
	work, _, h, boot, deps := upFixture(t)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--as", "bernard"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if len(*boot) != 1 || (*boot)[0] != "demo "+work+" --as bernard" {
		t.Fatalf("bootstrap calls = %q", *boot)
	}
	want := []string{
		"workspace list",
		"workspace create --cwd " + work + " --label demo --focus",
		"tab rename w9:t1 busmon",
		"tab create --workspace w9 --label master --no-focus",
		"tab create --workspace w9 --label dispatch --no-focus",
		"pane run w9:p1 /r/busmon --project demo",
	}
	got := strings.Join(h.calls, "\n")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("missing herdr call %q in:\n%s", w, got)
		}
	}
	if !strings.Contains(out.String(), "opened demo") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestUpFocusesAnOpenWorkspace(t *testing.T) {
	work, _, h, _, deps := upFixture(t)
	h.workspaces = `{"label":"demo","workspace_id":"w4"}`
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	got := strings.Join(h.calls, "\n")
	if !strings.Contains(got, "workspace focus w4") || strings.Contains(got, "workspace create") {
		t.Fatalf("a re-run must focus, not open a second workspace:\n%s", got)
	}
}

func TestUpOutsideHerdrProvisionsAndExplains(t *testing.T) {
	work, templates, h, boot, deps := upFixture(t)
	deps.Env = func(k string) string { // outside herdr: no HERDR_ENV
		if k == "AGENTBUS_PROJECTS_DIR" {
			return templates
		}
		return ""
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d", code)
	}
	if len(*boot) != 1 || len(h.calls) != 0 {
		t.Fatalf("outside herdr: bootstrap %q, herdr %q", *boot, h.calls)
	}
	if !strings.Contains(out.String(), "start herdr") {
		t.Fatalf("no instructions: %q", out.String())
	}
}

func TestUpRefusesBadInput(t *testing.T) {
	_, _, _, _, deps := upFixture(t)
	var out, errw bytes.Buffer
	if code := runUp([]string{"Bad Name"}, deps, &out, &errw); code == 0 {
		t.Fatal("an invalid project name was accepted")
	}
	deps.Bootstrap = func([]string) error { return errors.New("broker down") }
	if code := runUp([]string{"demo"}, deps, &out, &errw); code == 0 || !strings.Contains(errw.String(), "broker down") {
		t.Fatalf("a bootstrap failure must stop up: %q", errw.String())
	}
}

// A layout that fails halfway must not leave a labelled workspace behind: the
// next "agentbus up" would focus it and report success with no command running.
func TestUpClosesAWorkspaceItCouldNotFinish(t *testing.T) {
	for _, fail := range []string{"tab create --workspace w9 --label dispatch", "pane run w9:p1", "tab rename"} {
		work, _, h, _, deps := upFixture(t)
		h.fail = fail
		var out, errw bytes.Buffer
		if code := runUp([]string{"demo", work}, deps, &out, &errw); code == 0 {
			t.Fatalf("%s: up succeeded", fail)
		}
		if got := strings.Join(h.calls, "\n"); !strings.Contains(got, "workspace close w9") {
			t.Fatalf("%s: the half-made workspace was not closed:\n%s", fail, got)
		}
		if !strings.Contains(errw.String(), "closed") {
			t.Fatalf("%s: stderr = %q", fail, errw.String())
		}
	}
}

// servicesFixture: upFixture plus a fake service manager, a marker recorder
// and a service environment.
type upServices struct {
	m      *fakeManager
	marks  []bus.ServicesInstall
	paths  service.Paths
	exe    string
	probed int
}

func withServices(t *testing.T, deps *upDeps) *upServices {
	root := t.TempDir()
	u := &upServices{m: newFakeManager(), exe: "/opt/ab/agentbus",
		paths: service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}}
	deps.Services = func() (service.Manager, string) { u.probed++; return u.m, "" }
	deps.Paths = u.paths
	deps.Exe = func() (string, error) { return u.exe, nil }
	deps.Environ = []string{"PATH=/bin", "REDIS_PASSWORD=s3cret", "HERDR_SESSION=s", "HERDR_PANE_ID=w1:p1"}
	deps.Mark = func(project string, inst bus.ServicesInstall) error { u.marks = append(u.marks, inst); return nil }
	deps.Host = "h1"
	return u
}

func TestUpStartsTheServicesAndOpensNoServiceTab(t *testing.T) {
	work, _, h, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if !strings.Contains((*boot)[0], "--no-dispatch") || !strings.Contains((*boot)[0], "--no-missions") {
		t.Fatalf("the template must not hold service tabs: bootstrap %q", *boot)
	}
	if len(u.marks) != 1 || strings.Join(u.marks[0].Kinds, ",") != "dispatch,missions" || u.marks[0].Manager != "fake" {
		t.Fatalf("marks = %+v", u.marks)
	}
	for _, c := range []string{"install demo dispatch", "start demo dispatch", "install demo missions", "start demo missions"} {
		if !u.m.saw(c) {
			t.Fatalf("missing %q in %v", c, u.m.calls)
		}
	}
	ms := u.m.specs["demo/missions"]
	if ms.Exe != "/opt/ab/agentbus" || strings.Join(ms.Args, " ") != "--repo "+work || ms.WorkDir != work || ms.EnvFile != u.paths.EnvFile("demo") {
		t.Fatalf("missions spec = %+v", ms)
	}
	env, err := service.LoadEnvFile(u.paths.EnvFile("demo"))
	if err != nil || env["REDIS_PASSWORD"] != "s3cret" || env["HERDR_SESSION"] != "s" || env["HERDR_PANE_ID"] != "" {
		t.Fatalf("env = %v, %v", env, err)
	}
	got := strings.Join(h.calls, "\n")
	if strings.Contains(got, "--label dispatch") || strings.Contains(got, "--label missions") || strings.Contains(got, " dispatch\n") {
		t.Fatalf("a service tab was opened:\n%s", got)
	}
	if !strings.Contains(out.String(), "dispatch: started") || !strings.Contains(out.String(), "service logs") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestUpFiltersServiceTabsOfAnOlderTemplate(t *testing.T) {
	work, templates, h, _, deps := upFixture(t)
	withServices(t, &deps)
	old := "name = \"demo\"\nworking_dir = \"" + work + "\"\n\n[[tabs]]\nname = \"busmon\"\ncommand = \"/r/busmon --project demo\"\n\n[[tabs]]\nname = \"dispatch\"\ncommand = \"/r/agentbus --project demo dispatch\"\n\n[[tabs]]\nname = \"missions\"\ncommand = \"/r/agentbus --project demo missions --repo " + work + "\"\n"
	os.MkdirAll(templates, 0o755)
	os.WriteFile(filepath.Join(templates, "demo.toml"), []byte(old), 0o644)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if got := strings.Join(h.calls, "\n"); strings.Contains(got, "dispatch") || strings.Contains(got, "missions") {
		t.Fatalf("an older template's service tabs were opened:\n%s", got)
	}
}

func TestUpRespectsNoDispatch(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--no-dispatch"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if u.m.saw("install demo dispatch") || u.m.saw("start demo dispatch") || !u.m.saw("start demo missions") {
		t.Fatalf("calls = %v", u.m.calls)
	}
	if strings.Join(u.marks[0].Kinds, ",") != "missions" {
		t.Fatalf("marks = %+v", u.marks)
	}
}

func TestUpRestartsOnlyAServiceWhoseConfigurationChanged(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	u.m.calls, out = nil, bytes.Buffer{}
	runUp([]string{"demo", work}, deps, &out, &errw)
	if u.m.saw("stop demo dispatch") || u.m.saw("start demo dispatch") || !strings.Contains(out.String(), "dispatch: already running") {
		t.Fatalf("an unchanged running service was touched: %v\n%s", u.m.calls, out.String())
	}
	u.exe = "/opt/ab2/agentbus"
	u.m.calls, out = nil, bytes.Buffer{}
	runUp([]string{"demo", work}, deps, &out, &errw)
	if !u.m.saw("stop demo dispatch") || !u.m.saw("start demo dispatch") || !strings.Contains(out.String(), "restarted") {
		t.Fatalf("a changed unit was not restarted: %v\n%s", u.m.calls, out.String())
	}
}

// A running conductor is never restarted by up for a new configuration: its
// published state can be seconds old, and a mission may have just started.
func TestUpNeverRestartsARunningConductor(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	deps.Environ = append(deps.Environ, "LANG=fr_FR.UTF-8") // another shell
	u.m.calls, out = nil, bytes.Buffer{}
	runUp([]string{"demo", work}, deps, &out, &errw)
	if u.m.saw("stop demo missions") || !u.m.saw("stop demo dispatch") {
		t.Fatalf("calls = %v", u.m.calls)
	}
	if !strings.Contains(out.String(), "missions: running, left as is") || !strings.Contains(out.String(), "service restart missions") {
		t.Fatalf("out = %q", out.String())
	}
}

// A changed unit of a stopped job is reloaded (launchd keeps a loaded
// definition until bootout), not kicked with the old one.
func TestUpReloadsAChangedUnitThatIsNotRunning(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	u.m.Stop("demo", service.Dispatch)
	u.exe = "/opt/ab2/agentbus"
	u.m.calls = nil
	runUp([]string{"demo", work}, deps, &out, &errw)
	stop, start := -1, -1
	for i, c := range u.m.calls {
		if c == "stop demo dispatch" && stop < 0 {
			stop = i
		}
		if c == "start demo dispatch" {
			start = i
		}
	}
	if stop < 0 || start < stop {
		t.Fatalf("a changed stopped unit was not reloaded before its start: %v", u.m.calls)
	}
}

// The herdr session the project opens in reaches the services: --session,
// else the caller's, else the project name, as bootstrap resolves it. A
// socket of another session is not carried over.
func TestUpGivesTheServicesTheProjectsHerdrSession(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.Environ = []string{"PATH=/bin", "HERDR_SESSION=mine", "HERDR_SOCKET_PATH=/run/herdr/mine.sock"}
	var out, errw bytes.Buffer
	runUp([]string{"demo", work, "--session", "team"}, deps, &out, &errw)
	env, _ := service.LoadEnvFile(u.paths.EnvFile("demo"))
	if env["HERDR_SESSION"] != "team" || env["HERDR_SOCKET_PATH"] != "" {
		t.Fatalf("--session: env = %v", env)
	}
	runUp([]string{"demo", work}, deps, &out, &errw)
	env, _ = service.LoadEnvFile(u.paths.EnvFile("demo"))
	if env["HERDR_SESSION"] != "mine" || env["HERDR_SOCKET_PATH"] != "/run/herdr/mine.sock" {
		t.Fatalf("caller's session: env = %v", env)
	}
	deps.Environ = []string{"PATH=/bin"}
	runUp([]string{"demo", work}, deps, &out, &errw)
	env, _ = service.LoadEnvFile(u.paths.EnvFile("demo"))
	if env["HERDR_SESSION"] != "demo" {
		t.Fatalf("outside herdr: env = %v", env)
	}
}

// The tab fallback rewrites the template but keeps the project's directory,
// even when up runs elsewhere without a path.
func TestUpTabsFallbackKeepsTheProjectDirectory(t *testing.T) {
	work, _, _, boot, deps := upFixture(t)
	withServices(t, &deps)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	*boot = nil
	runUp([]string{"demo", "--tabs"}, deps, &out, &errw)
	if len(*boot) != 2 || (*boot)[1] != "new demo "+work {
		t.Fatalf("bootstrap calls = %q, want the rewrite to keep %s", *boot, work)
	}
}

func TestUpFallsBackToTabsExplicitly(t *testing.T) {
	work, _, h, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.Services = func() (service.Manager, string) {
		return nil, "systemd --user is not available (Failed to connect to bus)"
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "systemd --user is not available") || !strings.Contains(out.String(), "herdr tabs") {
		t.Fatalf("the fallback is silent: %q", out.String())
	}
	if strings.Contains((*boot)[0], "--no-dispatch") || len(u.marks) != 0 || len(u.m.calls) != 0 {
		t.Fatalf("services touched in tab mode: boot %q marks %v calls %v", *boot, u.marks, u.m.calls)
	}
	if got := strings.Join(h.calls, "\n"); !strings.Contains(got, "--label dispatch") || !strings.Contains(got, "--label missions") {
		t.Fatalf("no service tabs in tab mode:\n%s", got)
	}
}

func TestUpTabsFlagForcesTabsAndRewritesAServiceTemplate(t *testing.T) {
	work, _, h, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw) // services: the template has no service tab
	h.calls, *boot, u.probed = nil, nil, 0
	if code := runUp([]string{"demo", work, "--tabs"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up --tabs = %d: %s", code, errw.String())
	}
	if u.probed != 0 {
		t.Fatal("--tabs still probed the service manager")
	}
	if len(*boot) != 2 || !strings.HasPrefix((*boot)[1], "new demo") || strings.Contains(strings.Join(*boot, " "), "--tabs") {
		t.Fatalf("bootstrap calls = %q (want a rewrite with \"new\", never --tabs)", *boot)
	}
	if got := strings.Join(h.calls, "\n"); !strings.Contains(got, "--label dispatch") {
		t.Fatalf("no dispatch tab:\n%s", got)
	}
}

func TestUpRefusesAGoRunBinaryForServices(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	u.exe = "/tmp/go-build123/b001/exe/agentbus"
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code == 0 {
		t.Fatal("up accepted a temporary binary for its units")
	}
	if len(u.m.calls) != 0 || !strings.Contains(errw.String(), "installed agentbus") {
		t.Fatalf("calls %v stderr %q", u.m.calls, errw.String())
	}
}

// --escalate-to is recorded for the services and survives a plain re-run;
// a new value replaces it and an invalid one is refused before anything runs.
func TestUpRecordsTheEscalationTarget(t *testing.T) {
	work, _, _, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.Environ = []string{"PATH=/bin"}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--escalate-to", "claude-hl-master"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up: %d %s", code, errw.String())
	}
	if slices.ContainsFunc(*boot, func(c string) bool { return strings.Contains(c, "escalate") }) {
		t.Fatalf("bootstrap was given --escalate-to: %q", *boot)
	}
	env, _ := service.LoadEnvFile(u.paths.EnvFile("demo"))
	if env[service.EscalateEnv] != "claude-hl-master" {
		t.Fatalf("env = %v", env)
	}
	runUp([]string{"demo"}, deps, &out, &errw)
	if env, _ = service.LoadEnvFile(u.paths.EnvFile("demo")); env[service.EscalateEnv] != "claude-hl-master" {
		t.Fatalf("a plain re-run reset the target: %v", env)
	}
	runUp([]string{"demo", "--escalate-to", "master"}, deps, &out, &errw)
	if env, _ = service.LoadEnvFile(u.paths.EnvFile("demo")); env[service.EscalateEnv] != "master" {
		t.Fatalf("a new target was not recorded: %v", env)
	}
	*boot = nil
	errw.Reset()
	if code := runUp([]string{"demo", "--escalate-to", "Bad Name"}, deps, &out, &errw); code != 1 || len(*boot) != 0 {
		t.Fatalf("invalid target: code %d, bootstrap %q, %s", code, *boot, errw.String())
	}
}

func TestUpNamesTheExpectedTemplatePath(t *testing.T) {
	work, templates, _, _, deps := upFixture(t)
	deps.Bootstrap = func([]string) error { return nil } // writes no template
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--tabs"}, deps, &out, &errw); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if want := filepath.Join(templates, "demo.toml"); !strings.Contains(errw.String(), want) {
		t.Fatalf("stderr %q does not name %s", errw.String(), want)
	}
}

func TestHerdrSessionFallsBackToTheSocket(t *testing.T) {
	sock := "HERDR_SOCKET_PATH=/h/.config/herdr/sessions/agent-bus/herdr.sock"
	cases := []struct {
		args, environ []string
		want          string
	}{
		{[]string{"demo"}, []string{sock}, "agent-bus"},
		{[]string{"demo", "--session", "team"}, []string{sock}, "team"},
		{[]string{"demo"}, []string{"HERDR_SESSION=mine", sock}, "mine"},
		{[]string{"demo"}, []string{"PATH=/bin"}, "demo"},
		{[]string{"demo"}, []string{"HERDR_SOCKET_PATH=/h/.config/herdr/herdr.sock"}, "default"},
		{[]string{"demo"}, []string{"HERDR_SOCKET_PATH=/tmp/odd.sock"}, ""},
	}
	for _, c := range cases {
		if got := herdrSession(c.args, c.environ); got != c.want {
			t.Errorf("herdrSession(%v, %v) = %q, want %q", c.args, c.environ, got, c.want)
		}
	}
}

func TestUpRefusesServicesOnAnUnreadableSocket(t *testing.T) {
	work, _, _, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.Environ = []string{"PATH=/bin", "HERDR_SOCKET_PATH=/tmp/odd.sock"}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	for _, w := range []string{"HERDR_SOCKET_PATH", "--session"} {
		if !strings.Contains(errw.String(), w) {
			t.Fatalf("stderr %q does not name %s", errw.String(), w)
		}
	}
	if _, err := os.Stat(u.paths.EnvFile("demo")); err == nil {
		t.Fatal("an env file was written")
	}
	if len(*boot) != 0 {
		t.Fatalf("a refused up must not bootstrap: %q", *boot)
	}
}

func TestUpOutsideHerdrHintNamesASession(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	deps.Env = func(k string) string {
		if k == "AGENTBUS_PROJECTS_DIR" {
			return filepath.Join(filepath.Dir(work), "projects")
		}
		return ""
	}
	deps.Environ = []string{"PATH=/bin", "HERDR_SOCKET_PATH=/tmp/odd.sock"}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--tabs"}, deps, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "herdr --session demo)") {
		t.Fatalf("the hint must name a session: %q", out.String())
	}
}

func TestHerdrBinOrder(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"HERDR_BIN_PATH": "/p/herdr", "HERDR_BIN": "/b/herdr"}, "/p/herdr"},
		{map[string]string{"HERDR_BIN": "/b/herdr"}, "/b/herdr"},
		{map[string]string{}, "herdr"},
	}
	for _, c := range cases {
		if got := herdrBin(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("herdrBin(%v) = %q, want %q", c.env, got, c.want)
		}
	}
}

func TestUpKeepsTheSocketThatNamedTheSession(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	sock := "/x/sessions/agent-bus/herdr.sock"
	deps.Environ = []string{"PATH=/bin", "HERDR_SOCKET_PATH=" + sock}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	env, _ := service.LoadEnvFile(u.paths.EnvFile("demo"))
	if env["HERDR_SESSION"] != "agent-bus" || env["HERDR_SOCKET_PATH"] != sock {
		t.Fatalf("env = %v", env)
	}
}

// Whichever agentbus runs up (the plugin's ./agentbus, the repo's), the units
// run the one setup.sh installed, so a machine's services name one binary.
func TestUpUnitsRunTheInstalledBinary(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	bin := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	deps.Installed = bin
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if got := u.m.specs["demo/dispatch"].Exe; got != bin {
		t.Fatalf("unit runs %q, want %q", got, bin)
	}
	// Gone (uninstalled): the running binary, as before.
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if got := u.m.specs["demo/dispatch"].Exe; got != u.exe {
		t.Fatalf("unit runs %q, want %q", got, u.exe)
	}
}
