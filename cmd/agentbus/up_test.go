package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// fixtureManifest is upFixture's roles.toml: beside its templates.
func fixtureManifest(templates string) string {
	return filepath.Join(filepath.Dir(templates), "roles.toml")
}

// agentTab is a template tab launching role in demo, as bootstrap writes it.
func agentTab(templates, role string) string {
	return "env ROLES_TOML=" + fixtureManifest(templates) + " /r/scripts/agent-launch " + role + " demo"
}

// upFixture: a project "demo" whose bootstrap writes a template (busmon,
// master, and the service tabs unless --no-dispatch / --no-missions), a
// roles.toml it names, a fake service manager and the fake terminal host
// the team starts on (herdr exit §4.7).
func upFixture(t *testing.T) (string, string, *recHost, *[]string, upDeps) {
	t.Helper()
	dir := t.TempDir()
	templates := filepath.Join(dir, "projects")
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fixtureManifest(templates)
	if err := os.WriteFile(manifest, []byte("schema_version = 2\n[defaults]\nprofile = \"claude\"\n"+
		"[profiles.claude]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n"+
		"[roles.master]\ntier = \"boot\"\nskills = []\n[roles.coder]\ntier = \"boot\"\nskills = []\n"+
		"[roles.foureyes]\ntier = \"boot\"\nskills = []\n[roles.sentinel]\ntier = \"boot\"\nskills = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	team := &recHost{Host: fake.New()}
	var boot []string
	env := map[string]string{"AGENTBUS_PROJECTS_DIR": templates}
	deps := upDeps{
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
			body := "name = \"demo\"\nworking_dir = \"" + work + "\"\n\n[[tabs]]\nname = \"busmon\"\ncommand = \"/r/busmon --project demo\"\n\n[[tabs]]\nname = \"master\"\ncommand = \"env ROLES_TOML=" + manifest + " /r/scripts/agent-launch master demo\"\n"
			if !has("--no-dispatch") {
				body += "\n[[tabs]]\nname = \"dispatch\"\ncommand = \"/r/agentbus --project demo dispatch\"\n"
			}
			if !has("--no-missions") {
				body += "\n[[tabs]]\nname = \"missions\"\ncommand = \"/r/agentbus --project demo missions --repo " + work + "\"\n"
			}
			return os.WriteFile(path, []byte(body), 0o644)
		},
		Env:  func(k string) string { return env[k] },
		Team: func(string) (teamHost, error) { return team, nil },
	}
	withServices(t, &deps)
	return work, templates, team, &boot, deps
}

func TestUpProvisionsAndStartsTheTeam(t *testing.T) {
	work, _, h, boot, deps := upFixture(t)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--as", "charlie"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if len(*boot) != 1 || (*boot)[0] != "demo "+work+" --as charlie --no-dispatch --no-missions" {
		t.Fatalf("bootstrap calls = %q", *boot)
	}
	if strings.Join(h.started(), ",") != "master" {
		t.Fatalf("started %v, want the agents only (busmon is the person's window)", h.started())
	}
	if !strings.Contains(out.String(), "master: started") || !strings.Contains(out.String(), "busmon: run it in your own terminal: busmon --project demo") {
		t.Fatalf("out = %q", out.String())
	}
}

// A second up starts nothing that runs.
func TestUpStartsNothingRunning(t *testing.T) {
	work, _, h, _, deps := upFixture(t)
	var out, errw bytes.Buffer
	for i := 0; i < 2; i++ {
		if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
			t.Fatalf("up = %d: %s", code, errw.String())
		}
	}
	if strings.Join(h.started(), ",") != "master" || !strings.Contains(out.String(), "master: running") {
		t.Fatalf("started %v: %q", h.started(), out.String())
	}
}

// herdr exit §4.4b: no tab mode, and no team without a service manager.
func TestUpRefusesTabsAndNoManager(t *testing.T) {
	work, _, h, _, deps := upFixture(t)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--tabs"}, deps, &out, &errw); code != 1 || !strings.Contains(errw.String(), "--tabs is gone") {
		t.Fatalf("--tabs: %d %q", code, errw.String())
	}
	deps.Services = func() (service.Manager, string) { return nil, "systemd --user is not available" }
	errw.Reset()
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 1 || !strings.Contains(errw.String(), "services.manager = none") {
		t.Fatalf("no manager: %d %q", code, errw.String())
	}
	if len(h.specs) != 0 {
		t.Fatalf("started %v", h.started())
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

// A start the holder refuses fails up, and says which.
func TestUpSaysAStartTheHolderRefused(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	deps.Team = func(string) (teamHost, error) { return refusingHost{}, nil }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 1 || !strings.Contains(out.String(), "master: not started: no holder") {
		t.Fatalf("up = %d: %q %q", code, out.String(), errw.String())
	}
}

// refusingHost lists nothing and refuses every start.
type refusingHost struct{}

func (refusingHost) List(context.Context, string) ([]termhost.Term, error) { return nil, nil }
func (refusingHost) Start(context.Context, termhost.Spec) (termhost.Term, error) {
	return termhost.Term{}, errors.New("no holder")
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
	deps.Environ = []string{"PATH=/bin", "REDIS_PASSWORD=s3cret", "AGENTBUS_TERM_ID=w1:p1"}
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
		if !u.m.Saw(c) {
			t.Fatalf("missing %q in %v", c, u.m.Calls)
		}
	}
	ms := u.m.Specs["demo/missions"]
	if ms.Exe != "/opt/ab/agentbus" || strings.Join(ms.Args, " ") != "--repo "+work || ms.WorkDir != work || ms.EnvFile != u.paths.EnvFile("demo") {
		t.Fatalf("missions spec = %+v", ms)
	}
	env, err := service.LoadEnvFile(u.paths.EnvFile("demo"))
	if err != nil || env["REDIS_PASSWORD"] != "s3cret" || env["AGENTBUS_TERM_ID"] != "" {
		t.Fatalf("env = %v, %v", env, err)
	}
	if strings.Join(h.started(), ",") != "master" {
		t.Fatalf("started %v: a service is no terminal", h.started())
	}
	if !strings.Contains(out.String(), "dispatch: started") || !strings.Contains(out.String(), "service logs") {
		t.Fatalf("out = %q", out.String())
	}
}

// On a managed build, up installs and starts the updater's unit when it is
// missing (quiet-update-auto §4.1): one per host, its env file from up's
// shell; a running one is left as it is, a stopped one started, and a
// developer build writes none.
func TestUpInstallsTheUpdater(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 || u.m.Saw("install updater") {
		t.Fatalf("a developer build: up = %d, calls %v", code, u.m.Calls)
	}
	deps.Managed = true
	out.Reset()
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if u.m.Updater == nil || *u.m.Updater != (service.UpdaterSpec{Exe: "/opt/ab/agentbus", WorkDir: u.paths.Home, Paths: u.paths}) || !u.m.UpdaterOn {
		t.Fatalf("updater %+v on %v, calls %v", u.m.Updater, u.m.UpdaterOn, u.m.Calls)
	}
	env, err := service.LoadEnvFile(u.paths.UpdaterEnvFile())
	if err != nil || env["PATH"] != "/bin" || env["AGENTBUS_TERM_ID"] != "" {
		t.Fatalf("updater env %v %v", env, err)
	}
	if !strings.Contains(out.String(), "updater: installed and started as a fake service") {
		t.Fatalf("out %q", out.String())
	}
	u.m.Calls = nil
	runUp([]string{"demo", work}, deps, &out, &errw)
	if u.m.Saw("install updater") || u.m.Saw("start updater") || u.m.Saw("stop updater") {
		t.Fatalf("a running updater was touched: %v", u.m.Calls)
	}
	// A stopped updater, its env file unreadable (it exits on "invalid
	// configuration"): the next up rewrites the file and starts it.
	u.m.UpdaterOn = false
	if err := os.WriteFile(u.paths.UpdaterEnvFile(), []byte("not an env line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadEnvFile(u.paths.UpdaterEnvFile()); err == nil {
		t.Fatal("the fixture's env file reads")
	}
	runUp([]string{"demo", work}, deps, &out, &errw)
	if u.m.Saw("install updater") || !u.m.Saw("start updater") || !u.m.UpdaterOn {
		t.Fatalf("a stopped updater: %v", u.m.Calls)
	}
	if env, err := service.LoadEnvFile(u.paths.UpdaterEnvFile()); err != nil || env["PATH"] != "/bin" {
		t.Fatalf("the env file not repaired: %v %v", env, err)
	}
}

// A manager with no updater unit (services.manager = none, until step 4f)
// says so and fails nothing.
func TestUpUpdaterWithoutUnit(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.Managed = true
	deps.Services = func() (service.Manager, string) { return struct{ service.Manager }{u.m}, "" }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 || u.m.Saw("install updater") ||
		!strings.Contains(out.String(), "updater: none under the fake manager yet") {
		t.Fatalf("up = %d, calls %v, out %q", code, u.m.Calls, out.String())
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
	if len(h.specs) != 0 {
		t.Fatalf("an older template's service tabs were started: %v", h.started())
	}
}

func TestUpRespectsNoDispatch(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--no-dispatch"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if u.m.Saw("install demo dispatch") || u.m.Saw("start demo dispatch") || !u.m.Saw("start demo missions") {
		t.Fatalf("calls = %v", u.m.Calls)
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
	u.m.Calls, out = nil, bytes.Buffer{}
	runUp([]string{"demo", work}, deps, &out, &errw)
	if u.m.Saw("stop demo dispatch") || u.m.Saw("start demo dispatch") || !strings.Contains(out.String(), "dispatch: already running") {
		t.Fatalf("an unchanged running service was touched: %v\n%s", u.m.Calls, out.String())
	}
	u.exe = "/opt/ab2/agentbus"
	u.m.Calls, out = nil, bytes.Buffer{}
	runUp([]string{"demo", work}, deps, &out, &errw)
	if !u.m.Saw("stop demo dispatch") || !u.m.Saw("start demo dispatch") || !strings.Contains(out.String(), "restarted") {
		t.Fatalf("a changed unit was not restarted: %v\n%s", u.m.Calls, out.String())
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
	u.m.Calls, out = nil, bytes.Buffer{}
	runUp([]string{"demo", work}, deps, &out, &errw)
	if u.m.Saw("stop demo missions") || !u.m.Saw("stop demo dispatch") {
		t.Fatalf("calls = %v", u.m.Calls)
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
	u.m.Calls = nil
	runUp([]string{"demo", work}, deps, &out, &errw)
	stop, start := -1, -1
	for i, c := range u.m.Calls {
		if c == "stop demo dispatch" && stop < 0 {
			stop = i
		}
		if c == "start demo dispatch" {
			start = i
		}
	}
	if stop < 0 || start < stop {
		t.Fatalf("a changed stopped unit was not reloaded before its start: %v", u.m.Calls)
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
	if len(u.m.Calls) != 0 || !strings.Contains(errw.String(), "installed agentbus") {
		t.Fatalf("calls %v stderr %q", u.m.Calls, errw.String())
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

// --reload (lifecycle part C) is recorded like --escalate-to: kept by a plain
// re-run, replaced by a new value, removed by on; anything else is refused.
func TestUpRecordsTheReloadPolicy(t *testing.T) {
	work, _, _, boot, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.Environ = []string{"PATH=/bin"}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--reload", "propose"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up: %d %s", code, errw.String())
	}
	if slices.ContainsFunc(*boot, func(c string) bool { return strings.Contains(c, "reload") }) {
		t.Fatalf("bootstrap was given --reload: %q", *boot)
	}
	read := func() string { env, _ := service.LoadEnvFile(u.paths.EnvFile("demo")); return env[service.ReloadEnv] }
	if got := read(); got != "propose" {
		t.Fatalf("policy %q", got)
	}
	runUp([]string{"demo"}, deps, &out, &errw)
	if got := read(); got != "propose" {
		t.Fatalf("a plain re-run reset it: %q", got)
	}
	runUp([]string{"demo", "--reload", "off"}, deps, &out, &errw)
	if got := read(); got != "off" {
		t.Fatalf("not replaced: %q", got)
	}
	runUp([]string{"demo", "--reload", "on"}, deps, &out, &errw)
	if got := read(); got != "" {
		t.Fatalf("on kept %q", got)
	}
	*boot = nil
	if code := runUp([]string{"demo", "--reload", "sometimes"}, deps, &out, &errw); code != 1 || len(*boot) != 0 {
		t.Fatalf("invalid policy: code %d, bootstrap %q", code, *boot)
	}
}

func TestUpNamesTheExpectedTemplatePath(t *testing.T) {
	work, templates, _, _, deps := upFixture(t)
	deps.Bootstrap = func([]string) error { return nil } // writes no template
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if want := filepath.Join(templates, "demo.toml"); !strings.Contains(errw.String(), want) {
		t.Fatalf("stderr %q does not name %s", errw.String(), want)
	}
}

// Whichever agentbus runs up (the repo's, a worktree's), the units
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
	if got := u.m.Specs["demo/dispatch"].Exe; got != bin {
		t.Fatalf("unit runs %q, want %q", got, bin)
	}
	// Gone (uninstalled): the running binary, as before.
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if got := u.m.Specs["demo/dispatch"].Exe; got != u.exe {
		t.Fatalf("unit runs %q, want %q", got, u.exe)
	}
}

// KeepEnv (a suspended project's resume): the project's env file is kept as
// it is, so a resume never changes a project's
// configuration; what the caller's environment would have changed is named,
// never its value (master, suspend 3a).
func TestUpKeepEnvKeepsTheProjectsSettings(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.Environ = []string{"PATH=/bin", "AGENT_BUS_AUTO_CLEAR=0", "AGENT_BUS_QUOTA_LONG_HOLD=3h"}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up: %d %s", code, errw.String())
	}
	deps.KeepEnv = true
	deps.Environ = []string{"PATH=/bin", "AGENT_BUS_QUOTA_LONG_HOLD=1h", "AGENT_BUS_TOKENS=0"}
	out.Reset()
	if code := runUp([]string{"demo"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up KeepEnv: %d %s", code, errw.String())
	}
	env, err := service.LoadEnvFile(u.paths.EnvFile("demo"))
	if err != nil {
		t.Fatal(err)
	}
	if env["AGENT_BUS_AUTO_CLEAR"] != "0" || env["AGENT_BUS_QUOTA_LONG_HOLD"] != "3h" {
		t.Fatalf("env after a KeepEnv up = %v", env)
	}
	if _, set := env["AGENT_BUS_TOKENS"]; set {
		t.Fatalf("the caller's new key was taken: %v", env)
	}
	// Named: a value the caller sets differently, a key it adds, a key it lacks; never a value.
	if s := out.String(); !strings.Contains(s, "AGENT_BUS_QUOTA_LONG_HOLD") || !strings.Contains(s, "AGENT_BUS_TOKENS") ||
		!strings.Contains(s, "AGENT_BUS_AUTO_CLEAR") || strings.Contains(s, "1h") || strings.Contains(s, "3h") {
		t.Fatalf("the note on the caller's keys = %q", s)
	}
	// Without KeepEnv, up takes the caller's environment, as it always did.
	deps.KeepEnv = false
	if code := runUp([]string{"demo"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up: %d %s", code, errw.String())
	}
	if env, _ = service.LoadEnvFile(u.paths.EnvFile("demo")); env["AGENT_BUS_AUTO_CLEAR"] != "" || env["AGENT_BUS_QUOTA_LONG_HOLD"] != "1h" {
		t.Fatalf("a plain up = %v", env)
	}
}

// KeepEnv with an env file it cannot read installs nothing: it never
// replaces a configuration it could not read.
func TestUpKeepEnvRefusesAnUnreadableEnvFile(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.Environ = []string{"PATH=/bin", "AGENT_BUS_AUTO_CLEAR=0"}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up: %d %s", code, errw.String())
	}
	if err := os.Chmod(u.paths.EnvFile("demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps.KeepEnv = true
	errw.Reset()
	if code := runUp([]string{"demo"}, deps, &out, &errw); code != 1 || !strings.Contains(errw.String(), "keep") {
		t.Fatalf("up KeepEnv on an unreadable file = %d %q", code, errw.String())
	}
	if err := os.Chmod(u.paths.EnvFile("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if env, _ := service.LoadEnvFile(u.paths.EnvFile("demo")); env["AGENT_BUS_AUTO_CLEAR"] != "0" {
		t.Fatalf("the file was replaced: %v", env)
	}
}
