package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 15, 21, 40, 0, 0, time.UTC)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// §2.2: every row of the judgement.
func TestJudge(t *testing.T) {
	for _, c := range []struct {
		name string
		prev *Record
		err  error
		boot string
		want Verdict
	}{
		{"missing", nil, os.ErrNotExist, "B", None},
		{"unreadable", nil, ErrMalformed, "B", None},
		{"this boot unknown", &Record{Boot: "A", State: Running}, nil, "", None},
		{"its boot unknown", &Record{Boot: "", State: Running}, nil, "B", None},
		{"same boot", &Record{Boot: "B", State: Running}, nil, "B", None},
		{"same boot, stopped", &Record{Boot: "B", State: Stopped, Cause: CauseSignal}, nil, "B", None},
		{"down", &Record{Boot: "A", State: Stopped, Cause: CauseDown}, nil, "B", None},
		{"suspend", &Record{Boot: "A", State: Stopped, Cause: CauseSuspend}, nil, "B", Suspended},
		{"signal", &Record{Boot: "A", State: Stopped, Cause: CauseSignal}, nil, "B", Intentional},
		{"running", &Record{Boot: "A", State: Running}, nil, "B", Unplanned},
		{"stopped, unknown cause", &Record{Boot: "A", State: Stopped, Cause: "x"}, nil, "B", None},
		{"unknown state", &Record{Boot: "A", State: "x"}, nil, "B", None},
	} {
		if got := Judge(c.prev, c.err, c.boot); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// The dispatch service at start: the first start creates the counter as []
// before run.json; a later start never recreates a lost counter (§3.2).
func TestStartedBootstrap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agentbus", "p")
	if err := Started(dir, "B", t0); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, CounterFile)); got != "[]\n" {
		t.Fatalf("counter %q", got)
	}
	if got := readFile(t, filepath.Join(dir, RunFile)); got != `{"boot":"B","state":"running","cause":"","at":"2026-11-15T21:40:00Z"}`+"\n" {
		t.Fatalf("run %q", got)
	}
	for _, f := range []string{RunFile, CounterFile} {
		if m := mode(t, filepath.Join(dir, f)); m != 0o600 {
			t.Fatalf("%s mode %v", f, m)
		}
	}
	if err := os.Remove(filepath.Join(dir, CounterFile)); err != nil {
		t.Fatal(err)
	}
	if err := Started(dir, "C", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, CounterFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a lost counter recreated by the service: %v", err)
	}
	if r, err := ReadRun(dir); err != nil || r.Boot != "C" || r.State != Running {
		t.Fatalf("%+v %v", r, err)
	}
}

// The counter is created before run.json is renamed into place: a failed
// counter write leaves no run.json.
func TestStartedCounterFirst(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, CounterFile), 0o700); err != nil { // the counter cannot be written
		t.Fatal(err)
	}
	if err := Started(dir, "B", t0); err == nil {
		t.Fatal("no error")
	}
	if _, err := os.Stat(filepath.Join(dir, RunFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run.json written without its counter: %v", err)
	}
}

// up, the person's start: creates or recreates the counter as [] when it is
// missing, never resets one that exists.
func TestUpRecreatesOnlyAMissingCounter(t *testing.T) {
	dir := t.TempDir()
	if err := Started(dir, "A", t0); err != nil {
		t.Fatal(err)
	}
	if err := WriteCounter(dir, []Entry{{Boot: "A", At: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := Up(dir, "B", t0); err != nil {
		t.Fatal(err)
	}
	if es, err := ReadCounter(dir); err != nil || len(es) != 1 {
		t.Fatalf("up reset the counter: %+v %v", es, err)
	}
	os.Remove(filepath.Join(dir, CounterFile))
	if err := Up(dir, "B", t0); err != nil {
		t.Fatal(err)
	}
	if es, err := ReadCounter(dir); err != nil || len(es) != 0 {
		t.Fatalf("up did not recreate: %+v %v", es, err)
	}
	if r, _ := ReadRun(dir); r.State != Running || r.Boot != "B" {
		t.Fatalf("%+v", r)
	}
}

// SIGTERM writes stopped only over running of this boot; down and suspend
// write theirs whatever was there.
func TestSignalledOnlyOverRunningOfThisBoot(t *testing.T) {
	dir := t.TempDir()
	if err := Started(dir, "B", t0); err != nil {
		t.Fatal(err)
	}
	if err := Signalled(dir, "B", t0); err != nil {
		t.Fatal(err)
	}
	if r, _ := ReadRun(dir); r.State != Stopped || r.Cause != CauseSignal {
		t.Fatalf("%+v", r)
	}
	for _, cause := range []string{CauseDown, CauseSuspend} {
		if err := Stopping(dir, "B", cause, t0); err != nil {
			t.Fatal(err)
		}
		if err := Signalled(dir, "B", t0); err != nil {
			t.Fatal(err)
		}
		if r, _ := ReadRun(dir); r.Cause != cause {
			t.Fatalf("signal over %s: %+v", cause, r)
		}
	}
	if err := Started(dir, "A", t0); err != nil { // another boot's running
		t.Fatal(err)
	}
	if err := Signalled(dir, "B", t0); err != nil {
		t.Fatal(err)
	}
	if r, _ := ReadRun(dir); r.State != Running || r.Boot != "A" {
		t.Fatalf("signal over another boot's running: %+v", r)
	}
	if err := Signalled(t.TempDir(), "B", t0); err != nil { // no file: nothing written
		t.Fatal(err)
	}
}

// Unreadable or malformed files are errors, never an empty value.
func TestReadMalformed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, RunFile), []byte("{"), 0o600)
	os.WriteFile(filepath.Join(dir, CounterFile), []byte(`{"a":1}`), 0o600)
	if _, err := ReadRun(dir); !errors.Is(err, ErrMalformed) {
		t.Fatalf("run: %v", err)
	}
	if _, err := ReadCounter(dir); !errors.Is(err, ErrMalformed) {
		t.Fatalf("counter: %v", err)
	}
	empty := t.TempDir()
	if _, err := ReadCounter(empty); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing counter: %v", err)
	}
}

// Count adds this boot once and keeps only the window (§3.2).
func TestCount(t *testing.T) {
	now := int64(100 * 3600 * 1000)
	win := 24 * time.Hour
	in := []Entry{{Boot: "old", At: now - 25*3600*1000}, {Boot: "A", At: now - 3600*1000}}
	out := Count(in, "B", now, win)
	if len(out) != 2 || out[0].Boot != "A" || out[1].Boot != "B" || out[1].At != now {
		t.Fatalf("%+v", out)
	}
	if again := Count(out, "B", now+1, win); len(again) != 2 || again[1].At != now {
		t.Fatalf("the same boot counted twice: %+v", again)
	}
	if edge := Count([]Entry{{Boot: "E", At: now - win.Milliseconds()}}, "B", now, win); len(edge) != 1 {
		t.Fatalf("an entry exactly a window old is outside: %+v", edge)
	}
}

// The counter round trip, 0600, atomic replace; ResetCounter empties it.
func TestCounterFile(t *testing.T) {
	dir := t.TempDir()
	es := []Entry{{Boot: "A", At: 5}, {Boot: "B", At: 6}}
	if err := WriteCounter(dir, es); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, CounterFile)); got != `[{"boot":"A","at":5},{"boot":"B","at":6}]`+"\n" {
		t.Fatalf("%q", got)
	}
	if mode(t, filepath.Join(dir, CounterFile)) != 0o600 {
		t.Fatal("mode")
	}
	if err := ResetCounter(dir); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCounter(dir); err != nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*tmp*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}
