package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// superviseWorld installs a holder build without a manager (holder.Supervised)
// in temp directories, its env file setting the run directory the holder
// would use.
type superviseWorld struct {
	m              holder.Supervised
	key, env, work string
	run            string // the env file's XDG_RUNTIME_DIR
}

func newSuperviseWorld(t *testing.T) *superviseWorld {
	t.Helper()
	tmp := t.TempDir()
	mk := func(name string) string {
		d, err := os.MkdirTemp("", name) // short: a run directory
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(d) })
		if err := os.Chmod(d, 0o700); err != nil {
			t.Fatal(err)
		}
		return d
	}
	w := &superviseWorld{env: filepath.Join(tmp, "demo.env"), work: tmp, run: mk("sv")}
	paths := service.Paths{Home: tmp, ConfigHome: filepath.Join(tmp, "c"), StateHome: filepath.Join(tmp, "s"), DataHome: filepath.Join(tmp, "d")}
	w.m = holder.Supervised{Paths: paths, Getenv: envMap(map[string]string{"XDG_RUNTIME_DIR": mk("svm")})}
	if _, err := service.WriteEnvFile(w.env, map[string]string{"XDG_RUNTIME_DIR": w.run}); err != nil {
		t.Fatal(err)
	}
	raw := "v9.9.9"
	w.key = service.BuildKey(raw)
	writeExe(t, service.HolderBin(paths, w.key), "#!/bin/sh\nexit 0\n")
	if _, err := w.m.InstallHolder(service.HolderSpec{Project: "demo", Raw: raw, EnvFile: w.env, WorkDir: w.work, Paths: paths}); err != nil {
		t.Fatal(err)
	}
	return w
}

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// superviseRun captures what the command would supervise, without running it.
type superviseRun struct {
	sup    service.Supervisor
	killed []string // project/key/cutoff of each kill
}

func (w *superviseWorld) deps(r *superviseRun, gaveUp bool) superviseDeps {
	return superviseDeps{
		Manager: w.m,
		Run: func(_ context.Context, s service.Supervisor) bool {
			r.sup = s
			return gaveUp
		},
		Kill: func(getenv func(string) string, project, key string, cutoff uint64) (int, error) {
			r.killed = append(r.killed, project+"/"+key+"/"+getenv("XDG_RUNTIME_DIR")+"/"+strconv.FormatUint(cutoff, 10))
			return 0, nil
		},
		Now: func() uint64 { return 4242 },
	}
}

// `service supervise holder <p> <key>` runs the build's binary copy as the
// holder, recording its groups, in the recorded working directory; after each
// exit it kills the groups the holder recorded, with the cutoff taken at the
// exit and the run directory the holder used (its env file's).
func TestSuperviseRunsTheBuildsHolder(t *testing.T) {
	w := newSuperviseWorld(t)
	var r superviseRun
	var errw strings.Builder
	if code := runSupervise("demo", []string{"holder", "demo", w.key}, w.deps(&r, false), &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	c := r.sup.Command()
	want := []string{service.HolderBin(w.m.Paths, w.key), "--project", "demo", "holder", "--env", w.env, "--record-groups"}
	if strings.Join(c.Args, " ") != strings.Join(want, " ") || c.Path != want[0] || c.Dir != w.work {
		t.Fatalf("command %v in %q, want %v in %q", c.Args, c.Dir, want, w.work)
	}
	r.sup.AfterExit(1)
	if len(r.killed) != 1 || r.killed[0] != "demo/"+w.key+"/"+w.run+"/4242" {
		t.Fatalf("kills %v", r.killed)
	}
}

// A supervisor that gave up exits 1, so the failure shows in its log and
// status; a stopped or finished one exits 0.
func TestSuperviseGaveUpExitsOne(t *testing.T) {
	w := newSuperviseWorld(t)
	var r superviseRun
	var errw strings.Builder
	if code := runSupervise("demo", []string{"holder", "demo", w.key}, w.deps(&r, true), &errw); code != 1 {
		t.Fatalf("exit %d", code)
	}
}

// The kind must be one supervised (a holder with its build key, dispatch or
// missions without one); the project must be the one given; a build or a
// service not installed is refused.
func TestSuperviseRefuses(t *testing.T) {
	w := newSuperviseWorld(t)
	for name, args := range map[string][]string{
		"kind":          {"nope", "demo"},
		"service key":   {"dispatch", "demo", w.key},
		"service only":  {"dispatch", "demo"},
		"svc project":   {"missions", "other"},
		"project":       {"holder", "other", w.key},
		"arity":         {"holder", "demo"},
		"not installed": {"holder", "demo", service.BuildKey("v0.0.1")},
		"bad key":       {"holder", "demo", "../x"},
	} {
		var r superviseRun
		var errw strings.Builder
		if code := runSupervise("demo", args, w.deps(&r, false), &errw); code == 0 || r.sup.Command != nil {
			t.Errorf("%s: exit %d, ran %v", name, code, r.sup.Command != nil)
		}
	}
}

// --record-groups makes the holder record its terminals' groups where its
// supervisor reads them (holder.PgidsPath of its own build).
func TestHolderConfigRecordGroups(t *testing.T) {
	w := newHolderWorld(t)
	w.writeEnv(t, map[string]string{"PATH": "/usr/bin:/bin", service.HolderRepoEnv: w.repo, "XDG_RUNTIME_DIR": w.run})
	cfg, getenv, err := holderConfig("demo", []string{"--env", w.envFile, "--record-groups"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := holder.PgidsPath(getenv, "demo", service.BuildKey(cfg.Build))
	if err != nil || cfg.Pgids != want || !strings.HasPrefix(want, w.run) {
		t.Fatalf("pgids %q, want %q under %s (%v)", cfg.Pgids, want, w.run, err)
	}
	if cfg, _, err := holderConfig("demo", []string{"--env", w.envFile}); err != nil || cfg.Pgids != "" {
		t.Fatalf("without the flag: %q %v", cfg.Pgids, err)
	}
}

// installService installs kind for demo under the world's manager, exe the
// unit's binary.
func (w *superviseWorld) installService(t *testing.T, kind, exe string, args ...string) {
	t.Helper()
	if _, err := w.m.Install(service.Spec{Project: "demo", Kind: kind, Exe: exe, WorkDir: w.work, EnvFile: w.env, Paths: w.m.Paths, Args: args}); err != nil {
		t.Fatal(err)
	}
}

// herdr exit 6c3: `service supervise dispatch|missions <p>` runs the unit's
// own command (service run, its env file, its args) in its working
// directory, and kills no recorded group after an exit: only the holder
// records any.
func TestSuperviseRunsAProjectService(t *testing.T) {
	w := newSuperviseWorld(t)
	exe := filepath.Join(w.work, "bin", "agentbus")
	writeExe(t, exe, "#!/bin/sh\nexit 0\n")
	w.installService(t, service.Missions, exe, "--repo", w.work)
	var r superviseRun
	var errw strings.Builder
	if code := runSupervise("demo", []string{service.Missions, "demo"}, w.deps(&r, false), &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	c := r.sup.Command()
	want := []string{exe, "--project", "demo", "service", "run", "missions", "--env", w.env, "--repo", w.work}
	if strings.Join(c.Args, " ") != strings.Join(want, " ") || c.Path != exe || c.Dir != w.work {
		t.Fatalf("command %v in %q, want %v in %q", c.Args, c.Dir, want, w.work)
	}
	if r.sup.AfterExit != nil {
		r.sup.AfterExit(1)
	}
	if len(r.killed) != 0 {
		t.Fatalf("a service's exit killed groups: %v", r.killed)
	}
	if code := runSupervise("demo", []string{service.Dispatch, "demo"}, w.deps(&r, false), &errw); code != 2 {
		t.Fatalf("dispatch not installed: exit %d", code)
	}
}

// TestSuperviseHelperProcess is `service supervise dispatch demo` in a
// process of its own (TestSuperviseUnderNohup): it runs the unit's command
// once and writes how it ended.
func TestSuperviseHelperProcess(t *testing.T) {
	if os.Getenv("AGENTBUS_SUPERVISE_HELPER") != "1" {
		t.Skip("helper process")
	}
	m := holder.Supervised{Getenv: envMap(map[string]string{"XDG_RUNTIME_DIR": os.Getenv("SV_RUN")})}
	d := superviseDeps{Manager: m, Now: func() uint64 { return 0 },
		Run: func(_ context.Context, s service.Supervisor) bool {
			c := s.Command()
			_ = c.Run()
			ended := "exit " + strconv.Itoa(c.ProcessState.ExitCode())
			if ws, ok := c.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				ended = "signal " + ws.Signal().String()
			}
			_ = os.WriteFile(os.Getenv("SV_ENDED"), []byte(ended), 0o600)
			return false
		}}
	os.Exit(runSupervise("demo", []string{service.Dispatch, "demo"}, d, os.Stderr))
}

// Spec 4.4b, revision 26: a supervisor started under nohup (SIGHUP ignored)
// never hands the ignored hangup down. The service it runs reports its own
// SIGHUP disposition (bit 0 of SigIgn, signal 1: clear) and dies by the
// SIGHUP it is then sent.
func TestSuperviseUnderNohup(t *testing.T) {
	if _, err := exec.LookPath("nohup"); err != nil {
		t.Skip("no nohup")
	}
	if _, err := os.Stat("/proc/self/status"); err != nil {
		t.Skip("no /proc")
	}
	w := newSuperviseWorld(t)
	sigign, ended := filepath.Join(w.work, "sigign"), filepath.Join(w.work, "ended")
	exe := filepath.Join(w.work, "bin", "service")
	writeExe(t, exe, "#!/bin/sh\ngrep '^SigIgn:' /proc/$$/status > '"+sigign+"'\nkill -HUP $$\nsleep 5\n")
	w.installService(t, service.Dispatch, exe)
	cmd := exec.Command("nohup", os.Args[0], "-test.run=^TestSuperviseHelperProcess$")
	cmd.Env = append(os.Environ(), "AGENTBUS_SUPERVISE_HELPER=1", "SV_RUN="+w.m.Getenv("XDG_RUNTIME_DIR"), "SV_ENDED="+ended)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("supervise: %v: %s", err, out)
	}
	b, err := os.ReadFile(sigign)
	if err != nil {
		t.Fatal(err)
	}
	mask, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(string(b), "SigIgn:")), 16, 64)
	if err != nil || mask&1 != 0 {
		t.Fatalf("the service ignores SIGHUP: %q %v", b, err)
	}
	if got, _ := os.ReadFile(ended); string(got) != "signal hangup" {
		t.Fatalf("the service ended %q, want signal hangup", got)
	}
}
