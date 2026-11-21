package dispatchsvc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/brokerrt"
)

// diskDeps is what the disk check reads, injected: a test never touches a
// real disk.
type diskDeps struct {
	Statfs   func(path string) (free, total, dev uint64, err error)
	LookPath func(string) (string, error)
	Output   func(ctx context.Context, name string, args ...string) (string, error)
	Getenv   func(string) string
	Getwd    func() (string, error)
	TempDir  func() string
	GOOS     string // runtime.GOOS: on macOS docker's root is inside its VM
}

func realDiskDeps() diskDeps {
	return diskDeps{Statfs: realStatfs, LookPath: exec.LookPath, Getenv: os.Getenv, Getwd: os.Getwd, TempDir: os.TempDir, GOOS: runtime.GOOS,
		Output: func(ctx context.Context, name string, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, name, args...).Output()
			return string(out), err
		}}
}

// realStatfs is path's filesystem: the bytes an unprivileged writer may use,
// its size, and its device.
func realStatfs(path string) (uint64, uint64, uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, 0, err
	}
	var s unix.Stat_t
	if err := unix.Stat(path, &s); err != nil {
		return 0, 0, 0, err
	}
	bs := uint64(st.Bsize)
	return uint64(st.Bavail) * bs, uint64(st.Blocks) * bs, uint64(s.Dev), nil
}

// diskPaths are the filesystems agent-bus depends on (disk-check): the
// broker's data (its runtime's root when that runtime runs on this host, else not measured),
// the home, the Go build cache (when go is on PATH), the repository (the
// services' working directory) and its worktrees, and the temp dir.
func diskPaths(ctx context.Context, d diskDeps) []health.DiskPath {
	var out []health.DiskPath
	measure := func(label, path string, optional bool) {
		p := health.DiskPath{Label: label, Path: path}
		free, total, dev, err := d.Statfs(path)
		switch {
		case err != nil && optional && errors.Is(err, fs.ErrNotExist):
			return // no worktrees: nothing to measure
		case err != nil:
			p.Note = "statfs failed"
		default:
			p.Free, p.Total, p.Dev, p.Known = free, total, dev, true
			p.DevName = fmt.Sprintf("%d:%d", unix.Major(dev), unix.Minor(dev))
		}
		out = append(out, p)
	}
	out = append(out, brokerData(ctx, d, measure)...)
	if home := d.Getenv("HOME"); home != "" {
		measure("home", home, false)
	} else {
		out = append(out, health.DiskPath{Label: "home", Note: "HOME unset"})
	}
	if _, err := d.LookPath("go"); err == nil { // go on PATH: its cache is measured, or said not measured
		switch c, err := d.Output(ctx, "go", "env", "GOCACHE"); {
		case err != nil:
			out = append(out, health.DiskPath{Label: "go cache", Note: "go env GOCACHE failed"})
		case strings.TrimSpace(c) == "":
			out = append(out, health.DiskPath{Label: "go cache", Note: "go env GOCACHE empty"})
		case strings.TrimSpace(c) == "off": // no build cache: nothing to measure
		default:
			measure("go cache", strings.TrimSpace(c), true)
		}
	}
	if wd, err := d.Getwd(); err == nil {
		measure("repository", wd, false)
		measure("worktrees", filepath.Join(wd, ".worktrees"), true)
	} else {
		out = append(out, health.DiskPath{Label: "repository", Note: "working directory unavailable"})
	}
	measure("temp", d.TempDir(), false)
	return out
}

// brokerData is the broker's data path, measured, or said why not: the
// root of the runtime that runs it (broker.runtime, broker-hardening step
// 3), from the host only when that runtime runs on it. Docker Desktop, and a
// podman machine, keep it in their own VM (on macOS, and on Linux when they
// say so): not measured, never a host path of that name (reviewer-codex).
func brokerData(ctx context.Context, d diskDeps, measure func(label, path string, optional bool)) []health.DiskPath {
	note := func(n string) []health.DiskPath { return []health.DiskPath{{Label: "broker data", Note: n}} }
	rt := brokerrt.Of(d.Getenv)
	bin := rt.Bin()
	if _, err := d.LookPath(bin); err != nil {
		return note(bin + " not on PATH")
	}
	if rt == brokerrt.Podman { // each program a literal at its call: the guard reads it
		info, err := d.Output(ctx, "podman", "info", "-f", "{{.Host.ServiceIsRemote}}|{{.Store.GraphRoot}}")
		remote, root, _ := strings.Cut(strings.TrimSpace(info), "|")
		switch {
		case err != nil || root == "":
			return note("podman info failed")
		case remote != "false": // a podman machine or a remote connection: not this host's disk
			return note("podman runs remotely (a podman machine or a connection): not measured from this host")
		case d.GOOS != "linux":
			return note("podman's root is inside its VM on " + d.GOOS + ": not measured from the host")
		}
		measure("broker data", root, false)
		return nil
	}
	if why := dockerNotLocal(ctx, d); why != "" {
		return note(why)
	}
	info, err := d.Output(ctx, "docker", "info", "-f", "{{.DockerRootDir}}|{{.OperatingSystem}}")
	root, osName, _ := strings.Cut(strings.TrimSpace(info), "|")
	switch {
	case err != nil || root == "":
		return note("docker info failed")
	case d.GOOS != "linux":
		return note("docker's root is inside its VM on " + d.GOOS + ": not measured from the host")
	case strings.Contains(osName, "Docker Desktop"):
		return note("Docker Desktop keeps its root inside its VM: not measured from the host")
	}
	measure("broker data", root, false)
	return nil
}

// dockerNotLocal says why the selected daemon is not on this host (""
// when it is). Docker itself names the endpoint it uses: 'docker context
// inspect' with no name resolves the current one after DOCKER_CONTEXT and
// DOCKER_HOST, in its own order (checked on Docker 29; master: never
// reimplement that precedence). Only a local unix socket is this host's; any
// other endpoint, or none read, means its root is not measured from here
// (reviewer-codex). The note names the scheme only, never the host.
func dockerNotLocal(ctx context.Context, d diskDeps) string {
	out, err := d.Output(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	endpoint := strings.TrimSpace(out)
	if err != nil {
		endpoint = ""
	}
	if endpoint == "" {
		return "docker endpoint unknown: not measured from this host"
	}
	if strings.HasPrefix(endpoint, "unix://") {
		return ""
	}
	scheme, _, ok := strings.Cut(endpoint, "://")
	if !ok || scheme == "" {
		scheme = "unknown scheme"
	}
	return "docker daemon is remote (" + scheme + "): not measured from this host"
}

// diskThresholds are the disk check's bounds from the registry (env, then
// config.toml).
func diskThresholds(getenv func(string) string) health.DiskThresholds {
	s := config.Machine(getenv)
	return health.DiskThresholds{
		WarnPct: s.Get("health.disk_warn_pct").Value.(float64), FailPct: s.Get("health.disk_fail_pct").Value.(float64),
		WarnGiB: float64(s.Get("health.disk_warn_gib").Value.(int)), FailGiB: float64(s.Get("health.disk_fail_gib").Value.(int)),
	}
}

// diskCheck is the health check: the paths, judged.
func diskCheck(d diskDeps) func(context.Context) (health.Outcome, error) {
	return func(ctx context.Context) (health.Outcome, error) {
		return health.EvaluateDisk(diskPaths(ctx, d), diskThresholds(d.Getenv)), nil
	}
}
