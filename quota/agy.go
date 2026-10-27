package quota

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// AgyReader reads Antigravity's CLI logs (agy, Gemini) for a quota hit
// (kimi-quota): `RESOURCE_EXHAUSTED (code 429): Individual quota reached. …
// Resets in 66h41m18s.` agy states the reset, counted from the line's time,
// so the window is held at 100 % until then. agy logs no line that proves an
// answer (it logs each request's URL, answered or not), so nothing voids a hit
// earlier.
//
// Only <Dir>/cli-<YYYYMMDD>_<HHMMSS>.log is opened, for the agyMaxLogs newest
// modified within 8 days, their last 256 KiB. glog writes local time with
// no year: the year is the file name's.
type AgyReader struct {
	Dir string // ~/.gemini/antigravity-cli/log
	Now func() time.Time
	Loc *time.Location // the zone glog wrote in (nil = local)
}

// agyMaxLogs bounds the logs read per collection.
const agyMaxLogs = 8

var (
	agyFile  = regexp.MustCompile(`^cli-(\d{8})_\d{6}\.log$`)
	agyStamp = regexp.MustCompile(`^[IWEF](\d{2})(\d{2}) (\d{2}:\d{2}:\d{2}\.\d+) `)
	agyReset = regexp.MustCompile(`RESOURCE_EXHAUSTED \(code 429\).* Resets in ([0-9hms.]+)\.`)
)

func (AgyReader) Provider() string { return "google" }

func (a AgyReader) Read(ctx context.Context) (Reading, error) {
	now := a.Now()
	loc := a.Loc
	if loc == nil {
		loc = time.Local
	}
	var hitAt, resetAt time.Time
	for _, f := range agyLogs(a.Dir, now) {
		date := agyFile.FindStringSubmatch(filepath.Base(f))[1] // YYYYMMDD
		year, _ := strconv.Atoi(date[:4])
		fileMonth, _ := strconv.Atoi(date[4:6])
		for _, line := range tailLines(f, 256<<10) {
			if !bytes.Contains(line, []byte("RESOURCE_EXHAUSTED")) {
				continue
			}
			m := agyStamp.FindSubmatch(line)
			r := agyReset.FindSubmatch(line)
			if m == nil || r == nil {
				continue
			}
			month, _ := strconv.Atoi(string(m[1]))
			y := year
			if month < fileMonth {
				y++ // the session crossed New Year
			}
			at, err := time.ParseInLocation("2006 0102 15:04:05", strconv.Itoa(y)+" "+string(m[1])+string(m[2])+" "+string(m[3])[:8], loc)
			if err != nil || at.After(now) {
				continue // not a date, or stamped after now
			}
			d, err := time.ParseDuration(string(r[1]))
			if err != nil || d <= 0 {
				continue
			}
			if at.After(hitAt) {
				hitAt, resetAt = at, at.Add(d)
			}
		}
	}
	if hitAt.IsZero() || !resetAt.After(now) {
		return Unknown("google", "agy-log", "no current quota hit in recent agy logs"), nil
	}
	obs := hitAt.UnixMilli()
	return Reading{Provider: "google", Source: "agy-log", CollectedAt: now.UnixMilli(), ObservedAt: obs,
		Windows: []Window{{Name: "quota", UsedPct: 100, ResetsAt: resetAt.Unix(), ObservedAt: obs}}}, nil
}

// agyLogs is the newest cli-<date>_<time>.log files modified within 8 days
// (agy has stated resets of up to a week).
func agyLogs(dir string, now time.Time) []string {
	entries, _ := os.ReadDir(dir)
	type f struct {
		path string
		mod  time.Time
	}
	var recent []f
	for _, e := range entries {
		if !agyFile.MatchString(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() || now.Sub(st.ModTime()) > 8*24*time.Hour {
			continue
		}
		recent = append(recent, f{p, st.ModTime()})
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].mod.After(recent[j].mod) })
	if len(recent) > agyMaxLogs {
		recent = recent[:agyMaxLogs]
	}
	out := make([]string, len(recent))
	for i, r := range recent {
		out[i] = r.path
	}
	return out
}
