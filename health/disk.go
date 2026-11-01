package health

import (
	"fmt"
	"sort"
	"strings"
)

// The disk check (disk-check): free space on the filesystems agent-bus
// depends on (the broker's data, the home, the Go cache, the repository and
// its worktrees, the temp dir), each device once.

// DiskPath is one path's filesystem as the reader measured it: Known false
// when it could not (Note says why, a fixed phrase).
type DiskPath struct {
	Label, Path string
	Dev         uint64 // st_dev: paths on one device are judged once
	DevName     string // major:minor, for the detail
	Free, Total uint64 // bytes available to an unprivileged writer, and in all
	Known       bool
	Note        string
}

// DiskThresholds: a device warns under either warn bound and fails under
// either fail bound.
type DiskThresholds struct {
	WarnPct, FailPct float64
	WarnGiB, FailGiB float64
}

const bytesPerGiB = 1 << 30

// DiskCleanChoice ends a warn or fail's detail: the cleanup is the person's
// choice once they have looked (the command only looks, du).
const DiskCleanChoice = ". Your choice once you have looked: go clean -cache empties the Go build cache, which rebuilds itself"

// EvaluateDisk judges each device once by the worse of its free percentage
// and its free GiB; the check is the worst device's status. A path not
// measured is named in the detail, never read as ok; only when nothing was
// measured is the check unknown. The worst device comes first.
func EvaluateDisk(paths []DiskPath, th DiskThresholds) Outcome {
	type device struct {
		labels []string
		p      DiskPath
		status Status
	}
	var devs []*device
	byDev := map[uint64]*device{}
	var unmeasured []string
	for _, p := range paths {
		if p.Known && p.Total == 0 { // some FUSE or autofs report no size: never a fail at 0 GiB (foureyes)
			p.Known, p.Note = false, "no size reported"
		}
		if !p.Known {
			unmeasured = append(unmeasured, fmt.Sprintf("%s: not measured (%s)", p.Label, p.Note))
			continue
		}
		if d, ok := byDev[p.Dev]; ok {
			d.labels = append(d.labels, p.Label)
			continue
		}
		d := &device{labels: []string{p.Label}, p: p, status: diskStatus(p, th)}
		byDev[p.Dev] = d
		devs = append(devs, d)
	}
	if len(devs) == 0 {
		return Outcome{Status: Unknown, Detail: truncateDetail(strings.Join(unmeasured, "; "))}
	}
	sort.SliceStable(devs, func(i, j int) bool { return severity(devs[i].status) > severity(devs[j].status) })
	var parts []string
	for _, d := range devs {
		name := ""
		if d.p.DevName != "" {
			name = " (" + d.p.DevName + ")"
		}
		pct := 0.0
		if d.p.Total > 0 {
			pct = float64(d.p.Free) * 100 / float64(d.p.Total)
		}
		parts = append(parts, fmt.Sprintf("%s %s%s: %.1f GiB free of %.1f GiB (%.0f%%)", strings.Join(d.labels, ", "), d.p.Path, name,
			float64(d.p.Free)/bytesPerGiB, float64(d.p.Total)/bytesPerGiB, pct))
	}
	detail := strings.Join(append(parts, unmeasured...), "; ")
	if devs[0].status == OK {
		return Outcome{Status: OK, Detail: truncateDetail(detail)}
	}
	// The person's choice, named here, never in the copyable command; the
	// paths are cut to leave room for it within the detail's bound (foureyes).
	room := detailMax - len([]rune(DiskCleanChoice))
	if r := []rune(detail); len(r) > room {
		detail = string(r[:room-3]) + "..."
	}
	return Outcome{Status: devs[0].status, Detail: detail + DiskCleanChoice}
}

func diskStatus(p DiskPath, th DiskThresholds) Status {
	pct := 100.0
	if p.Total > 0 {
		pct = float64(p.Free) * 100 / float64(p.Total)
	}
	gib := float64(p.Free) / bytesPerGiB
	switch {
	case pct < th.FailPct || gib < th.FailGiB:
		return Fail
	case pct < th.WarnPct || gib < th.WarnGiB:
		return Warn
	}
	return OK
}

// severity orders the statuses a measured device can have: fail first.
func severity(s Status) int {
	switch s {
	case Fail:
		return 2
	case Warn:
		return 1
	}
	return 0
}
