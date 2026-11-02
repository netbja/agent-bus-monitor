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
		GOOS:    "linux",
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
		"docker context inspect --format {{.Endpoints.docker.Host}}": "unix:///var/run/docker.sock\n", "docker info -f {{.DockerRootDir}}|{{.OperatingSystem}}": "/var/lib/docker|Fedora Linux 44\n", "go env GOCACHE": "/home/u/.cache/go-build\n"})
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

// Never a silent omission, never a host path for a VM (reviewer-codex): go on
// PATH whose GOCACHE cannot be read is a path not measured; Docker Desktop
// (a VM on macOS, or its own VM on Linux) keeps its root out of the host's
// reach; GOCACHE=off is no cache, rightly no path.
func TestDiskPathsUnmeasured(t *testing.T) {
	g := uint64(1) << 30
	disks := map[string][3]uint64{"/var/lib/docker": {1 * g, 2 * g, 1}, "/home/u": {50 * g, 500 * g, 2}, "/data/p": {50 * g, 500 * g, 2}, "/tmp": {50 * g, 500 * g, 2}}
	note := func(d diskDeps, label string) (string, bool) {
		for _, p := range diskPaths(context.Background(), d) {
			if p.Label == label {
				return p.Note, true
			}
		}
		return "", false
	}
	tools := map[string]bool{"docker": true, "go": true}
	d := fakeDisks(disks, tools, map[string]string{"docker context inspect --format {{.Endpoints.docker.Host}}": "unix:///var/run/docker.sock\n", "docker info -f {{.DockerRootDir}}|{{.OperatingSystem}}": "/var/lib/docker|Docker Desktop\n"})
	if n, ok := note(d, "go cache"); !ok || n == "" {
		t.Fatalf("go env failed: go cache %q %v", n, ok)
	}
	if n, ok := note(d, "broker data"); !ok || !strings.Contains(n, "Docker Desktop") {
		t.Fatalf("Docker Desktop on Linux: broker data %q %v", n, ok)
	}
	d = fakeDisks(disks, tools, map[string]string{"docker context inspect --format {{.Endpoints.docker.Host}}": "unix:///var/run/docker.sock\n", "docker info -f {{.DockerRootDir}}|{{.OperatingSystem}}": "/var/lib/docker|Ubuntu\n", "go env GOCACHE": "\n"})
	d.GOOS = "darwin"
	if n, _ := note(d, "broker data"); !strings.Contains(n, "VM") {
		t.Fatalf("macOS: broker data %q", n)
	}
	if n, ok := note(d, "go cache"); !ok || n == "" {
		t.Fatalf("empty GOCACHE: go cache %q %v", n, ok)
	}
	d = fakeDisks(disks, tools, map[string]string{"docker context inspect --format {{.Endpoints.docker.Host}}": "unix:///var/run/docker.sock\n", "docker info -f {{.DockerRootDir}}|{{.OperatingSystem}}": "/var/lib/docker|Ubuntu\n", "go env GOCACHE": "off\n"})
	if _, ok := note(d, "go cache"); ok {
		t.Fatal("GOCACHE=off is no cache, yet a path")
	}
}

// A daemon that is not on this host keeps its root out of the host's reach
// (reviewer-codex): the endpoint Docker itself reports for the current
// context (it resolves DOCKER_CONTEXT and DOCKER_HOST, so the check reads no
// variable: master) must be a local unix socket; anything else, or none read,
// is not measured, the note naming the scheme only, never the host.
func TestDiskPathsRemoteDocker(t *testing.T) {
	g := uint64(1) << 30
	disks := map[string][3]uint64{"/var/lib/docker": {1 * g, 2 * g, 1}, "/home/u": {50 * g, 500 * g, 2}, "/data/p": {50 * g, 500 * g, 2}, "/tmp": {50 * g, 500 * g, 2}}
	info := "docker info -f {{.DockerRootDir}}|{{.OperatingSystem}}"
	inspect := "docker context inspect --format {{.Endpoints.docker.Host}}"
	for name, c := range map[string]struct {
		endpoint string // what docker reports; "" = the inspect fails
		measured bool
		note     string
	}{
		"local socket":          {"unix:///var/run/docker.sock", true, ""},
		"a rootless socket":     {"unix:///run/user/1000/docker.sock", true, ""},
		"tcp":                   {"tcp://build.example:2376", false, "remote (tcp)"},
		"ssh":                   {"ssh://u@far", false, "remote (ssh)"},
		"endpoint not readable": {"", false, "endpoint unknown"},
	} {
		out := map[string]string{info: "/var/lib/docker|Fedora Linux 44\n"}
		if c.endpoint != "" {
			out[inspect] = c.endpoint + "\n"
		}
		d := fakeDisks(disks, map[string]bool{"docker": true}, out)
		// The variables are docker's to resolve: set to a conflicting pair, they
		// change nothing in what the check asks.
		d.Getenv = func(k string) string {
			return map[string]string{"HOME": "/home/u", "DOCKER_HOST": "unix:///var/run/docker.sock", "DOCKER_CONTEXT": "far"}[k]
		}
		var p health.DiskPath
		for _, x := range diskPaths(context.Background(), d) {
			if x.Label == "broker data" {
				p = x
			}
		}
		if p.Known != c.measured || !strings.Contains(p.Note, c.note) || strings.Contains(p.Note, "far") || strings.Contains(p.Note, "build.example") {
			t.Errorf("%s: known %v note %q", name, p.Known, p.Note)
		}
	}
}
