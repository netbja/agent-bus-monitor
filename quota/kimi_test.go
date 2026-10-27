package quota

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Kimi Code's log lines, as kimi-code.log writes them (the upgrade URL
// dropped; no other field is read).
const (
	kimi5h      = `WARN  llm request failed  model=kimi-code/k3 errorName=APIProviderQuotaExhaustedError errorMessage="429 You've reached your 5-hour usage limit. Your quota will reset when the current 5-hour window ends. To continue now, purchase extra usage or upgrade your plan: " statusCode=429`
	kimi5h403   = `WARN  llm request failed  model=kimi-code/k3 errorName=APIStatusError errorMessage="403 You've reached your 5-hour usage limit. Your quota will reset when the current 5-hour window ends. To continue now, purchase extra usage or upgrade your plan: " statusCode=403`
	kimiWeekly  = `WARN  llm request failed  model=kimi-code/k3 errorName=APIStatusError errorMessage="403 You've reached your weekly (7-day) usage limit. Your quota will reset when the current 7-day window ends. To continue now, purchase extra usage or upgrade your plan: " statusCode=403`
	kimiBilling = `WARN  llm request failed  attempt=1/10 model=k3 errorName=APIStatusError errorMessage="403 You've reached your usage limit for this billing cycle. Your quota will be refreshed in the next cycle. To continue now, purchase extra usage or upgrade your plan: " statusCode=403`
	kimiServed  = `INFO  llm response  turnStep=0.1 ttftMs=2561 streamDurationMs=15044 outputTokens=617`
	kimiRequest = `INFO  llm request  turnStep=0.1`
)

// kimiAt is a log line at t0 minus ago.
func kimiAt(ago time.Duration, line string) string {
	return t0.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") + " " + line
}

// kimiSession writes one session's kimi-code.log, modified at mtime.
func kimiSession(t *testing.T, root, wd, session string, mtime time.Time, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, wd, session, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "kimi-code.log")
	if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(f, mtime, mtime)
	return f
}

func readKimi(t *testing.T, root string) Reading {
	t.Helper()
	r, err := KimiReader{Root: root, Now: fixed}.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A usage-limit hit is that window at 100 %. Kimi gives no reset time, so
// the reset is the honest upper bound: the window exhausted at the first hit
// of the episode ends at most its length after it. The newest hit is when the
// limit was last seen to hold.
func TestKimiLimitHit(t *testing.T) {
	root := t.TempDir()
	kimiSession(t, root, "wd_a_1", "session_1", t0, kimiAt(time.Hour, kimiServed),
		kimiAt(40*time.Minute, kimi5h403), kimiAt(10*time.Minute, kimiRequest), kimiAt(10*time.Minute, kimi5h),
		kimiAt(-time.Minute, kimiServed)) // a line stamped after now (a skewed clock) is not evidence
	r := readKimi(t, root)
	if r.Unknown || r.Provider != "moonshot" || len(r.Windows) != 1 {
		t.Fatalf("reading %+v", r)
	}
	w := r.Windows[0]
	if w.Name != "5h" || w.UsedPct != 100 || w.Minutes != 300 ||
		w.ResetsAt != t0.Add(-40*time.Minute+5*time.Hour).Unix() || w.ObservedAt != t0.Add(-10*time.Minute).UnixMilli() {
		t.Fatalf("window %+v", w)
	}
	if r.AvailableAt != t0.Add(-time.Hour).UnixMilli() || r.ObservedAt != w.ObservedAt {
		t.Fatalf("available %d observed %d", r.AvailableAt, r.ObservedAt)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("the policy must accept it: %v", err)
	}
}

// The account is shared: a weekly hit in another project's session counts.
func TestKimiWeeklyHitInAnotherProject(t *testing.T) {
	root := t.TempDir()
	kimiSession(t, root, "wd_a_1", "session_1", t0, kimiAt(4*time.Hour, kimiServed))
	kimiSession(t, root, "wd_b_2", "session_2", t0, kimiAt(3*time.Hour, kimiWeekly))
	r := readKimi(t, root)
	if len(r.Windows) != 1 || r.Windows[0].Name != "7d" || r.Windows[0].Minutes != 10080 ||
		r.Windows[0].ResetsAt != t0.Add(-3*time.Hour+7*24*time.Hour).Unix() {
		t.Fatalf("reading %+v", r)
	}
}

// An answer after the hit voids it: the account was served since, so no
// limit stops it, whatever window it was; AvailableAt carries the answer.
func TestKimiHitVoidedByALaterAnswer(t *testing.T) {
	root := t.TempDir()
	kimiSession(t, root, "wd_a_1", "session_1", t0, kimiAt(30*time.Minute, kimi5h))
	kimiSession(t, root, "wd_b_2", "session_2", t0, kimiAt(5*time.Minute, kimiServed))
	r := readKimi(t, root)
	if !r.Unknown || len(r.Windows) != 0 || r.AvailableAt != t0.Add(-5*time.Minute).UnixMilli() {
		t.Fatalf("reading %+v", r)
	}
	// A new hit after that answer opens a new episode: its bound starts there.
	kimiSession(t, root, "wd_a_1", "session_3", t0, kimiAt(2*time.Minute, kimi5h))
	r = readKimi(t, root)
	if len(r.Windows) != 1 || r.Windows[0].ResetsAt != t0.Add(-2*time.Minute+5*time.Hour).Unix() {
		t.Fatalf("new episode %+v", r)
	}
}

// A hit whose bound has passed is over; a billing-cycle limit has no
// length to bound it, so it never becomes a window (unknown, with a note);
// no files, no hit or garbage lines read unknown.
func TestKimiAgedBillingAndEmpty(t *testing.T) {
	root := t.TempDir()
	if r := readKimi(t, filepath.Join(root, "none")); !r.Unknown {
		t.Fatalf("no sessions: %+v", r)
	}
	kimiSession(t, root, "wd_a_1", "session_1", t0, kimiAt(6*time.Hour, kimi5h))
	if r := readKimi(t, root); !r.Unknown || len(r.Windows) != 0 {
		t.Fatalf("a hit past its bound: %+v", r)
	}
	kimiSession(t, root, "wd_a_1", "session_2", t0, kimiAt(time.Minute, kimiBilling))
	r := readKimi(t, root)
	if !r.Unknown || len(r.Windows) != 0 || !strings.Contains(r.Note, "billing cycle") {
		t.Fatalf("billing cycle: %+v", r)
	}
	kimiSession(t, root, "wd_a_1", "session_2", t0, "not a log line", `{"json":true}`,
		"2026-13-45T99:00:00.000Z "+kimi5h, kimiAt(time.Minute, "WARN  llm request failed  errorMessage=\"500 internal\" statusCode=500"))
	if r := readKimi(t, root); !r.Unknown || len(r.Windows) != 0 {
		t.Fatalf("malformed: %+v", r)
	}
}

// Only kimi-code.log is read, only in the newest sessions and only within the
// longest window: a hit anywhere else in the session (its wire log, state)
// or in a log untouched for longer than a week is not read.
func TestKimiReadsOnlyRecentSessionLogs(t *testing.T) {
	root := t.TempDir()
	f := kimiSession(t, root, "wd_a_1", "session_1", t0, kimiAt(time.Hour, kimiRequest))
	sess := filepath.Dir(filepath.Dir(f))
	os.MkdirAll(filepath.Join(sess, "agents", "main"), 0o700)
	os.WriteFile(filepath.Join(sess, "agents", "main", "wire.jsonl"), []byte(kimiAt(time.Minute, kimi5h)+"\n"), 0o600)
	os.WriteFile(filepath.Join(sess, "state.json"), []byte(kimiAt(time.Minute, kimi5h)+"\n"), 0o600)
	os.WriteFile(filepath.Join(sess, "logs", "other.log"), []byte(kimiAt(time.Minute, kimi5h)+"\n"), 0o600)
	os.WriteFile(filepath.Join(root, "config.toml"), []byte(kimiAt(time.Minute, kimi5h)+"\n"), 0o600)
	kimiSession(t, root, "wd_b_2", "session_old", t0.Add(-8*24*time.Hour), kimiAt(time.Minute, kimiWeekly))
	if r := readKimi(t, root); !r.Unknown || len(r.Windows) != 0 {
		t.Fatalf("read a file it must not: %+v", r)
	}
	for i := 0; i < kimiMaxLogs+2; i++ { // the newest logs win; the oldest, with the hit, is skipped
		kimiSession(t, root, "wd_c_3", "session_n"+itoa(int64(i)), t0.Add(-time.Duration(i)*time.Minute), kimiAt(time.Hour, kimiRequest))
	}
	kimiSession(t, root, "wd_c_3", "session_oldest", t0.Add(-time.Hour), kimiAt(time.Minute, kimi5h))
	if r := readKimi(t, root); !r.Unknown {
		t.Fatalf("read past the newest %d logs: %+v", kimiMaxLogs, r)
	}
}

func TestKimiBuiltin(t *testing.T) {
	found := func(n string) (string, error) {
		if n == "kimi" {
			return "/x/kimi", nil
		}
		return "", os.ErrNotExist
	}
	if p, ok := Builtins(found)["moonshot"]; !ok || p.Kind != "kimi" {
		t.Fatalf("builtin %+v %v", p, ok)
	}
	rs := Readers(map[string]ProviderConfig{"moonshot": {Kind: "kimi"}}, fixed)
	if len(rs) != 1 || rs[0].Provider() != "moonshot" {
		t.Fatalf("readers %v", rs)
	}
}
