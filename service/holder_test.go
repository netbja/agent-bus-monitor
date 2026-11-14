package service

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func holderPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	return Paths{Home: root, ConfigHome: filepath.Join(root, "cfg"), StateHome: filepath.Join(root, "state"), DataHome: filepath.Join(root, "data")}
}

// fakeExe is a binary to copy.
func fakeExe(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// One unit per (project, build), its own binary copy, the restart policy
// and the recorded values (spec 4.4a).
func TestSystemdHolderUnit(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{}
	m := Systemd{Run: f.run, Paths: p}
	raw := "v1.2.3+abc+dirty"
	key := BuildKey(raw)
	bin, err := InstallHolderBinary(p, raw, fakeExe(t, "A"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(p.DataHome, "agentbus", "holder", key, "agentbus"); bin != want {
		t.Fatalf("binary at %s, want %s", bin, want)
	}
	for _, path := range []string{bin, filepath.Dir(bin)} {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", path, st.Mode(), err)
		}
	}
	if _, err := m.InstallHolder(HolderSpec{Project: "demo", Raw: raw, Paths: p}); err != nil {
		t.Fatal(err)
	}
	name := HolderUnitName("demo", key)
	if name != "agentbus-holder.demo."+key+".service" {
		t.Fatalf("unit name %s", name)
	}
	b, err := os.ReadFile(filepath.Join(p.ConfigHome, "systemd", "user", name))
	if err != nil {
		t.Fatal(err)
	}
	u := string(b)
	for _, want := range []string{
		"X-Agentbus-Project=demo\n", "X-Agentbus-Build=" + key + "\n", "X-Agentbus-Build-Raw=" + raw + "\n",
		"StartLimitIntervalSec=300\n", "StartLimitBurst=5\n",
		`ExecStart="` + bin + `" "--project" "demo" "holder"` + "\n",
		"Restart=on-failure\n", "RestartSec=2\n", "KillMode=control-group\n",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q:\n%s", want, u)
		}
	}
	if !f.saw("--user daemon-reload") {
		t.Fatal("no daemon-reload")
	}
	units, err := m.HolderUnits("demo")
	if err != nil || len(units) != 1 || units[0] != (HolderUnit{Project: "demo", Key: key, Raw: raw}) {
		t.Fatalf("units %+v %v", units, err)
	}
	if units, _ := m.List(); len(units) != 0 {
		t.Fatalf("a holder unit listed as a service kind: %+v", units)
	}
}

// launchd: one job per build, label com.agentbus.holder.<project>.<key>,
// KeepAlive on failure only, ThrottleInterval 10, the three values recorded.
func TestLaunchdHolderJob(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{}
	m := Launchd{Run: f.run, Paths: p, UID: 501}
	raw := "v1.2.3+abc"
	key := BuildKey(raw)
	bin, err := InstallHolderBinary(p, raw, fakeExe(t, "A"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallHolder(HolderSpec{Project: "demo", Raw: raw, Paths: p}); err != nil {
		t.Fatal(err)
	}
	label := HolderLabel("demo", key)
	b, err := os.ReadFile(filepath.Join(p.DataHome, "agentbus", "launchd", label+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	pl := string(b)
	for _, want := range []string{
		"<key>Label</key><string>com.agentbus.holder.demo." + key + "</string>",
		"<string>" + bin + "</string><string>--project</string><string>demo</string><string>holder</string>",
		"<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>",
		"<key>ThrottleInterval</key><integer>10</integer>",
		"<key>XAgentbusProject</key><string>demo</string>",
		"<key>XAgentbusBuild</key><string>" + key + "</string>",
		"<key>XAgentbusBuildRaw</key><string>" + raw + "</string>",
	} {
		if !strings.Contains(pl, want) {
			t.Errorf("plist lacks %q:\n%s", want, pl)
		}
	}
	units, err := m.HolderUnits("demo")
	if err != nil || len(units) != 1 || units[0] != (HolderUnit{Project: "demo", Key: key, Raw: raw}) {
		t.Fatalf("units %+v %v", units, err)
	}
}

// Security S7: an install whose key is recorded with another raw id is
// refused (unit and binary alike).
func TestHolderInstallRefusesAnotherRaw(t *testing.T) {
	p := holderPaths(t)
	m := Systemd{Run: (&fakeRun{}).run, Paths: p}
	raw := "v1+abc"
	key := BuildKey(raw)
	if _, err := InstallHolderBinary(p, raw, fakeExe(t, "A")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallHolder(HolderSpec{Project: "demo", Raw: raw, Paths: p}); err != nil {
		t.Fatal(err)
	}
	// A raw id that reduces alike but differs: forge one with the same key
	// by rewriting what is recorded (a real collision needs the hash's).
	unit := filepath.Join(p.ConfigHome, "systemd", "user", HolderUnitName("demo", key))
	b, _ := os.ReadFile(unit)
	os.WriteFile(unit, []byte(strings.Replace(string(b), "X-Agentbus-Build-Raw="+raw, "X-Agentbus-Build-Raw=v1+ABC", 1)), 0o644)
	if _, err := m.InstallHolder(HolderSpec{Project: "demo", Raw: raw, Paths: p}); err == nil || !strings.Contains(err.Error(), "recorded") {
		t.Fatalf("unit: %v", err)
	}
	os.WriteFile(filepath.Join(HolderBinDir(p, key), "build-raw"), []byte("v1+ABC\n"), 0o600)
	if _, err := InstallHolderBinary(p, raw, fakeExe(t, "A")); err == nil || !strings.Contains(err.Error(), "recorded") {
		t.Fatalf("binary: %v", err)
	}
}

// fakeSystemd answers is-active per unit from active.
func fakeSystemd(active map[string]bool) *fakeRun {
	f := &fakeRun{answers: map[string]string{}}
	for unit, on := range active {
		if on {
			f.answers["--user is-active "+unit] = "active"
		} else {
			f.answers["--user is-active "+unit] = "inactive"
		}
	}
	return f
}

func install(t *testing.T, m HolderManager, p Paths, project, raw string) string {
	t.Helper()
	if _, err := InstallHolderBinary(p, raw, fakeExe(t, raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallHolder(HolderSpec{Project: project, Raw: raw, Paths: p}); err != nil {
		t.Fatal(err)
	}
	return BuildKey(raw)
}

func keysOf(t *testing.T, m HolderManager, project string) []string {
	t.Helper()
	units, err := m.HolderUnits(project)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range units {
		out = append(out, u.Key)
	}
	slices.Sort(out)
	return out
}

func binExists(p Paths, key string) bool {
	_, err := os.Stat(filepath.Join(HolderBinDir(p, key), "agentbus"))
	return err == nil
}

// Spec 4.4a's gc test: install A, upgrade to B, upgrade to C. With B still
// active, B's unit and binary are kept; once A's and B's holders exited,
// only C's remain. A gc never stops a unit, never touches the current build.
func TestHolderGC(t *testing.T) {
	p := holderPaths(t)
	active := map[string]bool{}
	f := fakeSystemd(active)
	m := Systemd{Run: f.run, Paths: p}
	a := install(t, m, p, "demo", "v1+aaa")
	b := install(t, m, p, "demo", "v2+bbb")
	c := install(t, m, p, "demo", "v3+ccc")
	f.answers["--user is-active "+HolderUnitName("demo", a)] = "inactive"
	f.answers["--user is-active "+HolderUnitName("demo", b)] = "active"
	f.answers["--user is-active "+HolderUnitName("demo", c)] = "active"
	none := func(string) bool { return false }
	removed, err := GCHolders(m, p, "demo", c, none)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{a}) || !slices.Equal(keysOf(t, m, "demo"), sorted(b, c)) || binExists(p, a) || !binExists(p, b) || !binExists(p, c) {
		t.Fatalf("B active: removed %v, units %v", removed, keysOf(t, m, "demo"))
	}
	if !f.saw("--user disable " + HolderUnitName("demo", a)) {
		t.Fatal("A's unit not disabled before its file went")
	}
	// B's holder exits: the next gc takes it.
	f.answers["--user is-active "+HolderUnitName("demo", b)] = "inactive"
	if removed, err = GCHolders(m, p, "demo", c, none); err != nil || !slices.Equal(removed, []string{b}) {
		t.Fatalf("B exited: removed %v %v", removed, err)
	}
	if !slices.Equal(keysOf(t, m, "demo"), []string{c}) || binExists(p, b) || !binExists(p, c) {
		t.Fatalf("after: units %v", keysOf(t, m, "demo"))
	}
	for _, call := range f.calls {
		if strings.Contains(call, " stop ") || strings.Contains(call, " restart ") || strings.HasPrefix(call, "--user stop") {
			t.Fatalf("a gc stopped a unit: %s", call)
		}
	}
}

// An inactive build whose holder socket still answers is kept; an
// unreachable manager keeps everything (an error is never "inactive").
func TestHolderGCKeepsWhatMayRun(t *testing.T) {
	p := holderPaths(t)
	f := fakeSystemd(nil)
	m := Systemd{Run: f.run, Paths: p}
	a := install(t, m, p, "demo", "v1+aaa")
	c := install(t, m, p, "demo", "v3+ccc")
	f.answers["--user is-active "+HolderUnitName("demo", a)] = "inactive"
	if removed, err := GCHolders(m, p, "demo", c, func(k string) bool { return k == a }); err != nil || len(removed) != 0 || !binExists(p, a) {
		t.Fatalf("an answering socket: removed %v %v", removed, err)
	}
	f.answers["--user is-active "+HolderUnitName("demo", a)] = "Failed to connect to bus: No medium found"
	if removed, _ := GCHolders(m, p, "demo", c, func(string) bool { return false }); len(removed) != 0 || !binExists(p, a) {
		t.Fatalf("an unreachable manager: removed %v", removed)
	}
}

// Recorded values, never a name prefix (spec 4.4a, S7): projects a and a-b;
// gc a leaves every a-b unit, and a binary another project's unit records
// stays.
func TestHolderGCMatchesTheRecordedProject(t *testing.T) {
	p := holderPaths(t)
	f := fakeSystemd(nil)
	m := Systemd{Run: f.run, Paths: p}
	old := install(t, m, p, "a", "v1+aaa")
	install(t, m, p, "a-b", "v1+aaa") // the same build in another project
	cur := install(t, m, p, "a", "v2+bbb")
	for _, proj := range []string{"a", "a-b"} {
		f.answers["--user is-active "+HolderUnitName(proj, old)] = "inactive"
	}
	removed, err := GCHolders(m, p, "a", cur, func(string) bool { return false })
	if err != nil || !slices.Equal(removed, []string{old}) {
		t.Fatalf("removed %v %v", removed, err)
	}
	if !slices.Equal(keysOf(t, m, "a-b"), []string{old}) {
		t.Fatalf("a-b's units %v", keysOf(t, m, "a-b"))
	}
	if !binExists(p, old) {
		t.Fatal("a binary a-b's unit records was removed")
	}
}

// launchd's gc: bootout, then the plist; a loaded job is kept.
func TestLaunchdHolderGC(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{answers: map[string]string{}, fails: map[string]bool{}}
	m := Launchd{Run: f.run, Paths: p, UID: 501}
	a := install(t, m, p, "demo", "v1+aaa")
	b := install(t, m, p, "demo", "v2+bbb")
	c := install(t, m, p, "demo", "v3+ccc")
	f.out = map[string]fakeOut{"print gui/501/" + HolderLabel("demo", a): {"Could not find service", true}} // not loaded
	f.answers["print gui/501/"+HolderLabel("demo", b)] = "state = running"
	removed, err := GCHolders(m, p, "demo", c, func(string) bool { return false })
	if err != nil || !slices.Equal(removed, []string{a}) || !slices.Equal(keysOf(t, m, "demo"), sorted(b, c)) || binExists(p, a) {
		t.Fatalf("removed %v %v, units %v", removed, err, keysOf(t, m, "demo"))
	}
}

func sorted(s ...string) []string { slices.Sort(s); return s }

// The current build is never removed, even inactive and silent (spec 4.4a:
// a gc never touches the current build).
func TestHolderGCKeepsTheCurrentBuild(t *testing.T) {
	p := holderPaths(t)
	f := fakeSystemd(nil)
	m := Systemd{Run: f.run, Paths: p}
	c := install(t, m, p, "demo", "v3+ccc")
	f.answers["--user is-active "+HolderUnitName("demo", c)] = "inactive"
	if removed, err := GCHolders(m, p, "demo", c, func(string) bool { return false }); err != nil || len(removed) != 0 || !binExists(p, c) {
		t.Fatalf("the current build: removed %v %v", removed, err)
	}
}

// launchd's RemoveHolder never removes a loaded job (a gc never stops one).
func TestLaunchdRemoveHolderKeepsALoadedJob(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{answers: map[string]string{}}
	m := Launchd{Run: f.run, Paths: p, UID: 501}
	a := install(t, m, p, "demo", "v1+aaa")
	f.answers["print gui/501/"+HolderLabel("demo", a)] = "state = running"
	if err := m.RemoveHolder("demo", a); err == nil || !slices.Equal(keysOf(t, m, "demo"), []string{a}) || f.saw("bootout") {
		t.Fatalf("a loaded job: %v, units %v", err, keysOf(t, m, "demo"))
	}
}

// StartHolder starts its build's unit and nothing else: systemd start (never
// restart, never reset-failed of another), launchd bootstrap only when the
// job is not loaded (a loaded one is left running).
func TestStartHolder(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{}
	sd := Systemd{Run: f.run, Paths: p}
	if err := sd.StartHolder("demo", "v1.aaa.0123456789"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.calls, []string{"--user start " + HolderUnitName("demo", "v1.aaa.0123456789")}) {
		t.Fatalf("systemd calls %v", f.calls)
	}
	g := &fakeRun{answers: map[string]string{}, fails: map[string]bool{}}
	ld := Launchd{Run: g.run, Paths: p, UID: 501}
	g.answers["print gui/501/"+HolderLabel("demo", "k1")] = "state = running"
	if err := ld.StartHolder("demo", "k1"); err != nil || g.saw("bootstrap") || g.saw("kickstart") {
		t.Fatalf("a loaded job: %v %v", err, g.calls)
	}
	g.out = map[string]fakeOut{"print gui/501/" + HolderLabel("demo", "k2"): {"Could not find service", true}}
	if err := ld.StartHolder("demo", "k2"); err != nil || !g.saw("bootstrap gui/501 ") {
		t.Fatalf("not loaded: %v %v", err, g.calls)
	}
}

// launchd's "not loaded" is its own answer (Could not find service); any
// other print failure is a manager that cannot say: gc keeps the job and its
// binary, and StartHolder bootstraps nothing (reviewer-codex).
func TestLaunchdHolderUncertainManager(t *testing.T) {
	p := holderPaths(t)
	f := &fakeRun{answers: map[string]string{}}
	m := Launchd{Run: f.run, Paths: p, UID: 501}
	a := install(t, m, p, "demo", "v1+aaa")
	c := install(t, m, p, "demo", "v3+ccc")
	f.out = map[string]fakeOut{"print gui/501/" + HolderLabel("demo", a): {"Unhandled error: permission denied", true}}
	if removed, err := GCHolders(m, p, "demo", c, func(string) bool { return false }); err == nil || len(removed) != 0 || !binExists(p, a) {
		t.Fatalf("an uncertain manager: removed %v, err %v", removed, err)
	}
	if err := m.StartHolder("demo", a); err == nil || f.saw("bootstrap") {
		t.Fatalf("start on an uncertain manager: %v %v", err, f.calls)
	}
	f.out["print gui/501/"+HolderLabel("demo", a)] = fakeOut{`Could not find service "` + HolderLabel("demo", a) + `" in domain for port`, true}
	if removed, err := GCHolders(m, p, "demo", c, func(string) bool { return false }); err != nil || !slices.Equal(removed, []string{a}) {
		t.Fatalf("not loaded: removed %v %v", removed, err)
	}
}
