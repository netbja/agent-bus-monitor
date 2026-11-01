package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
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
}

func realDiskDeps() diskDeps {
	return diskDeps{Statfs: realStatfs, LookPath: exec.LookPath, Getenv: os.Getenv, Getwd: os.Getwd, TempDir: os.TempDir,
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
// broker's data (docker's root when docker is on PATH, else not measured),
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
	if _, err := d.LookPath("docker"); err != nil {
		out = append(out, health.DiskPath{Label: "broker data", Note: "docker not on PATH"})
	} else if root, err := d.Output(ctx, "docker", "info", "-f", "{{.DockerRootDir}}"); err != nil || strings.TrimSpace(root) == "" {
		out = append(out, health.DiskPath{Label: "broker data", Note: "docker info failed"})
	} else {
		measure("broker data", strings.TrimSpace(root), false)
	}
	if home := d.Getenv("HOME"); home != "" {
		measure("home", home, false)
	} else {
		out = append(out, health.DiskPath{Label: "home", Note: "HOME unset"})
	}
	if _, err := d.LookPath("go"); err == nil {
		if c, err := d.Output(ctx, "go", "env", "GOCACHE"); err == nil && strings.TrimSpace(c) != "" && strings.TrimSpace(c) != "off" {
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
