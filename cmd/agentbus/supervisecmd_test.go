package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// Only the holder is supervised yet (dispatch and missions are 6c3); the
// project must be the one given; a build not installed is refused.
func TestSuperviseRefuses(t *testing.T) {
	w := newSuperviseWorld(t)
	for name, args := range map[string][]string{
		"kind":          {"dispatch", "demo", w.key},
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
