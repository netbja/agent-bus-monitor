package health

import (
	"strings"
	"testing"
)

const gib = uint64(1) << 30

func diskTh() DiskThresholds { return DiskThresholds{WarnPct: 10, WarnGiB: 5, FailPct: 2, FailGiB: 1} }

// The disk check (disk-check): each device once, judged by the worse of its
// free percentage and its free GiB; a path not measured is said, never ok,
// and only no measurement at all makes the check unknown.
func TestEvaluateDisk(t *testing.T) {
	ok := DiskPath{Label: "home", Path: "/home/u", Dev: 1, DevName: "8:1", Free: 200 * gib, Total: 500 * gib, Known: true}
	for _, c := range []struct {
		name   string
		paths  []DiskPath
		want   Status
		detail []string
	}{
		{"all fine", []DiskPath{ok}, OK, []string{"home /home/u (8:1): 200.0 GiB free of 500.0 GiB (40%)"}},
		{"one device once", []DiskPath{ok, {Label: "repo", Path: "/home/u/p", Dev: 1, DevName: "8:1", Free: 200 * gib, Total: 500 * gib, Known: true}}, OK,
			[]string{"home, repo /home/u (8:1)"}},
		{"under 10 percent warns", []DiskPath{{Label: "tmp", Path: "/tmp", Dev: 2, DevName: "0:30", Free: 9 * gib, Total: 100 * gib, Known: true}}, Warn, []string{"tmp /tmp (0:30): 9.0 GiB free"}},
		{"under 5 GiB warns", []DiskPath{{Label: "tmp", Path: "/tmp", Dev: 2, Free: 4 * gib, Total: 20 * gib, Known: true}}, Warn, []string{"4.0 GiB free"}},
		{"under 2 percent fails", []DiskPath{{Label: "docker root", Path: "/var/lib/docker", Dev: 3, Free: 15 * gib, Total: 1000 * gib, Known: true}}, Fail, []string{"docker root"}},
		{"under 1 GiB fails", []DiskPath{ok, {Label: "gocache", Path: "/c", Dev: 4, Free: gib / 2, Total: 4 * gib, Known: true}}, Fail, []string{"gocache /c", "0.5 GiB free"}},
		{"a path not measured is said", []DiskPath{ok, {Label: "docker root", Note: "docker not on PATH"}}, OK, []string{"docker root: not measured (docker not on PATH)"}},
		{"nothing measured", []DiskPath{{Label: "home", Note: "statfs failed"}}, Unknown, []string{"home: not measured (statfs failed)"}},
		// A filesystem that reports no size (some FUSE, autofs) is not measured,
		// never a fail at 0 GiB (foureyes).
		{"no size reported", []DiskPath{ok, {Label: "worktrees", Path: "/w", Dev: 9, Free: 0, Total: 0, Known: true}}, OK, []string{"worktrees: not measured (no size reported)"}},
		// A warn or fail names the person's choice, never in the command.
		{"the choice in the detail", []DiskPath{{Label: "tmp", Path: "/tmp", Dev: 2, Free: 4 * gib, Total: 20 * gib, Known: true}}, Warn, []string{"go clean -cache"}},
	} {
		out := EvaluateDisk(c.paths, diskTh())
		if out.Status != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, out.Status, out.Detail, c.want)
		}
		for _, d := range c.detail {
			if !strings.Contains(out.Detail, d) {
				t.Errorf("%s: detail %q lacks %q", c.name, out.Detail, d)
			}
		}
	}
	// The worst device comes first in the detail.
	out := EvaluateDisk([]DiskPath{ok, {Label: "tmp", Path: "/tmp", Dev: 2, Free: gib / 2, Total: 4 * gib, Known: true}}, diskTh())
	if !strings.HasPrefix(out.Detail, "tmp /tmp") {
		t.Fatalf("worst first: %q", out.Detail)
	}
}

// The For you command only looks: go clean is never in the copyable line.
func TestDiskCommandOnlyLooks(t *testing.T) {
	d, _ := ByID("disk")
	if !strings.HasPrefix(d.Command, "du -sh") || strings.Contains(d.Command, "clean") {
		t.Fatalf("command %q", d.Command)
	}
	if out := EvaluateDisk([]DiskPath{{Label: "home", Path: "/h", Dev: 1, Free: 200 * gib, Total: 500 * gib, Known: true}}, diskTh()); strings.Contains(out.Detail, "go clean") {
		t.Fatalf("an ok detail names a cleanup: %q", out.Detail)
	}
}
