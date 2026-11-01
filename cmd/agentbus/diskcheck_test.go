package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/netbja/agent-bus-monitor/health"
)

// fakeDisks answers statfs from a table: never a real disk in a test.
func fakeDisks(fs map[string][3]uint64, path map[string]bool, out map[string]string) diskDeps {
	return diskDeps{
		Statfs: func(p string) (uint64, uint64, uint64, error) {
			v, ok := fs[p]
			if !ok {
				return 0, 0, 0, errNotExist
			}
			return v[0], v[1], v[2], nil
		},
		LookPath: func(name string) (string, error) {
			if path[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Output: func(_ context.Context, name string, args ...string) (string, error) {
			v, ok := out[name+" "+strings.Join(args, " ")]
			if !ok {
				return "", errors.New("failed")
			}
			return v, nil
		},
		Getenv:  func(k string) string { return map[string]string{"HOME": "/home/u"}[k] },
		Getwd:   func() (string, error) { return "/data/p", nil },
		TempDir: func() string { return "/tmp" },
	}
}

var errNotExist = fs.ErrNotExist

// The paths agent-bus depends on, each with its label; a missing tool is a
// path not measured (docker) or no path at all (go); a repository without
// worktrees has no worktrees path.
func TestDiskPaths(t *testing.T) {
	g := uint64(1) << 30
	disks := map[string][3]uint64{
		"/var/lib/docker": {100 * g, 1000 * g, 2049}, "/home/u": {50 * g, 500 * g, 2050}, "/home/u/.cache/go-build": {50 * g, 500 * g, 2050},
		"/data/p": {300 * g, 900 * g, 2051}, "/data/p/.worktrees": {300 * g, 900 * g, 2051}, "/tmp": {7 * g, 8 * g, 40},
	}
	d := fakeDisks(disks, map[string]bool{"docker": true, "go": true}, map[string]string{
		"docker info -f {{.DockerRootDir}}": "/var/lib/docker\n", "go env GOCACHE": "/home/u/.cache/go-build\n"})
	var got []string
	for _, p := range diskPaths(context.Background(), d) {
		got = append(got, p.Label+"="+p.Path)
		if !p.Known {
			t.Errorf("%s not measured: %s", p.Label, p.Note)
		}
	}
	want := "broker data=/var/lib/docker home=/home/u go cache=/home/u/.cache/go-build repository=/data/p worktrees=/data/p/.worktrees temp=/tmp"
	if strings.Join(got, " ") != want {
		t.Fatalf("paths %q", strings.Join(got, " "))
	}
	if out := health.EvaluateDisk(diskPaths(context.Background(), d), health.DiskThresholds{WarnPct: 10, WarnGiB: 5, FailPct: 2, FailGiB: 1}); out.Status != health.OK ||
		!strings.Contains(out.Detail, fmt.Sprintf("home, go cache /home/u (%d:%d)", unix.Major(2050), unix.Minor(2050))) {
		t.Fatalf("evaluated: %s %q", out.Status, out.Detail)
	}

	delete(disks, "/data/p/.worktrees")
	d = fakeDisks(disks, map[string]bool{}, nil)
	got = nil
	for _, p := range diskPaths(context.Background(), d) {
		got = append(got, p.Label+"="+p.Path+"|"+p.Note)
	}
	if strings.Join(got, " ") != "broker data=|docker not on PATH home=/home/u| repository=/data/p| temp=/tmp|" {
		t.Fatalf("no docker, no go, no worktrees: %q", strings.Join(got, " "))
	}
}

// The thresholds come from the registry (env, then config.toml).
func TestDiskThresholds(t *testing.T) {
	th := diskThresholds(func(k string) string {
		return map[string]string{"AGENT_BUS_DISK_WARN_PCT": "15", "AGENT_BUS_DISK_FAIL_GIB": "3"}[k]
	})
	if th.WarnPct != 15 || th.WarnGiB != 5 || th.FailPct != 2 || th.FailGiB != 3 {
		t.Fatalf("thresholds %+v", th)
	}
}
