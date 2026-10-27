package quota

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agy's glog lines (local time, no year), as its cli-<date>_<time>.log holds
// them; everything but the limit's words is dropped.
const agyHit = `errorreport.go:224] agent executor error: generating and executing: RESOURCE_EXHAUSTED (code 429): Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 66h41m18s.`

func agyLine(level string, at time.Time, msg string) string {
	return level + at.Format("0102 15:04:05.000000") + "     252 " + msg
}

func agyLog(t *testing.T, dir string, started time.Time, mtime time.Time, lines ...string) {
	t.Helper()
	f := filepath.Join(dir, "cli-"+started.Format("20060102_150405")+".log")
	if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(f, mtime, mtime)
}

func readAgy(t *testing.T, dir string, now time.Time) Reading {
	t.Helper()
	r, err := AgyReader{Dir: dir, Now: func() time.Time { return now }, Loc: time.UTC}.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// agy states its reset: "Resets in 66h41m18s." from the line's time. The
// window is held at 100 % until then; agy logs no line that proves an
// answer, so nothing voids it earlier.
func TestAgyQuotaHit(t *testing.T) {
	dir := t.TempDir()
	hit := t0.Add(-20 * time.Minute)
	agyLog(t, dir, t0.Add(-time.Hour), t0,
		agyLine("I", t0.Add(-time.Hour), "http_helpers.go:315] URL: https://example.invalid/v1internal:streamGenerateContent"),
		agyLine("E", hit.Add(-time.Second), agyHit),
		agyLine("E", hit, strings.Replace(agyHit, "66h41m18s", "66h41m17s", 1)))
	r := readAgy(t, dir, t0)
	if r.Unknown || r.Provider != "google" || len(r.Windows) != 1 {
		t.Fatalf("reading %+v", r)
	}
	w := r.Windows[0]
	want := hit.Add(66*time.Hour + 41*time.Minute + 17*time.Second)
	if w.Name != "quota" || w.UsedPct != 100 || w.ResetsAt != want.Unix() || w.ObservedAt != hit.UnixMilli() || r.AvailableAt != 0 {
		t.Fatalf("window %+v", w)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("the policy must accept it: %v", err)
	}
}

// glog writes no year: it is the file's (cli-<YYYYMMDD>_…), plus one when the
// line's month is earlier than the file's (a session across New Year).
func TestAgyYearFromTheFileName(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 12, 31, 23, 50, 0, 0, time.UTC)
	hit := time.Date(2027, 1, 1, 0, 10, 0, 0, time.UTC)
	agyLog(t, dir, started, hit, agyLine("E", hit, agyHit))
	r := readAgy(t, dir, hit.Add(time.Minute))
	if len(r.Windows) != 1 || r.Windows[0].ObservedAt != hit.UnixMilli() {
		t.Fatalf("reading %+v", r)
	}
}

// A reset already passed, a missing or garbled "Resets in", other lines, no
// log and a file not named cli-<date>_<time>.log read unknown.
func TestAgyUnknowns(t *testing.T) {
	dir := t.TempDir()
	if r := readAgy(t, filepath.Join(dir, "none"), t0); !r.Unknown {
		t.Fatalf("no dir: %+v", r)
	}
	agyLog(t, dir, t0.Add(-80*time.Hour), t0, agyLine("E", t0.Add(-70*time.Hour), agyHit)) // reset 3 h ago
	agyLog(t, dir, t0.Add(-2*time.Hour), t0,
		agyLine("E", t0.Add(-time.Hour), strings.Replace(agyHit, " Resets in 66h41m18s.", "", 1)),
		agyLine("E", t0.Add(-time.Hour), strings.Replace(agyHit, "66h41m18s", "soon", 1)),
		"E1399 99:99:99.000000 252 "+agyHit, "garbage")
	os.WriteFile(filepath.Join(dir, "notes.log"), []byte(agyLine("E", t0.Add(-time.Minute), agyHit)+"\n"), 0o600)
	if r := readAgy(t, dir, t0); !r.Unknown || len(r.Windows) != 0 {
		t.Fatalf("reading %+v", r)
	}
}

// Only the newest logs, modified within the longest stated reset, are read.
func TestAgyReadsOnlyRecentLogs(t *testing.T) {
	dir := t.TempDir()
	agyLog(t, dir, t0.Add(-9*24*time.Hour), t0.Add(-9*24*time.Hour), agyLine("E", t0.Add(-9*24*time.Hour), strings.Replace(agyHit, "66h41m18s", "300h", 1)))
	if r := readAgy(t, dir, t0); !r.Unknown {
		t.Fatalf("read a log older than 8 days: %+v", r)
	}
	for i := 0; i < agyMaxLogs; i++ {
		agyLog(t, dir, t0.Add(-time.Duration(i+1)*time.Minute), t0.Add(-time.Duration(i)*time.Minute), agyLine("I", t0.Add(-time.Hour), "manager.go:88] nothing"))
	}
	agyLog(t, dir, t0.Add(-2*time.Hour), t0.Add(-time.Hour), agyLine("E", t0.Add(-90*time.Minute), agyHit))
	if r := readAgy(t, dir, t0); !r.Unknown {
		t.Fatalf("read past the newest %d logs: %+v", agyMaxLogs, r)
	}
}

func TestAgyBuiltin(t *testing.T) {
	found := func(n string) (string, error) {
		if n == "agy" {
			return "/x/agy", nil
		}
		return "", os.ErrNotExist
	}
	if p, ok := Builtins(found)["google"]; !ok || p.Kind != "agy" {
		t.Fatalf("builtin %+v %v", p, ok)
	}
	if rs := Readers(map[string]ProviderConfig{"google": {Kind: "agy"}}, fixed); len(rs) != 1 || rs[0].Provider() != "google" {
		t.Fatalf("readers %v", rs)
	}
}
