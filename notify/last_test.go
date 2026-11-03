package notify

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)

func TestLastMissingIsEmpty(t *testing.T) {
	l, err := ReadLast(filepath.Join(t.TempDir(), "notify-last.json"))
	if err != nil || len(l.Targets) != 0 {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestLastKeepsTheLatest20AndTheHourApart(t *testing.T) {
	path := LastPath(t.TempDir())
	// Six counted sends, then twenty-five exempt ones (fail, stall) within the
	// hour: the recent list keeps 20, the hour still counts the six.
	for i := 0; i < 6; i++ {
		if err := UpdateLast(path, func(l *Last) error {
			l.Add("phone", Outcome{Project: "demo", At: t0.Add(time.Duration(i) * time.Minute), OK: true}, true)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 25; i++ {
		_ = UpdateLast(path, func(l *Last) error {
			l.Add("phone", Outcome{Project: "demo", At: t0.Add(10*time.Minute + time.Duration(i)*time.Second), OK: true}, false)
			return nil
		})
	}
	l, err := ReadLast(path)
	if err != nil {
		t.Fatal(err)
	}
	r := l.Targets["phone"]
	if len(r.Recent) != 20 || !r.Recent[19].At.Equal(t0.Add(10*time.Minute+24*time.Second)) {
		t.Fatalf("recent %d, last %v", len(r.Recent), r.Recent[len(r.Recent)-1].At)
	}
	if n := l.SentInHour("phone", t0.Add(30*time.Minute)); n != 6 {
		t.Fatalf("hour counts %d, want 6", n)
	}
	if n := l.SentInHour("phone", t0.Add(63*time.Minute)); n != 2 {
		t.Fatalf("an hour on, %d still counted, want 2 (14:04 and 14:05)", n)
	}
	if latest, ok := l.Latest("phone"); !ok || !latest.OK {
		t.Fatalf("latest %+v %v", latest, ok)
	}
}

func TestLastPrunesTheHourOnWrite(t *testing.T) {
	path := LastPath(t.TempDir())
	_ = UpdateLast(path, func(l *Last) error {
		l.Add("phone", Outcome{At: t0, OK: true}, true)
		return nil
	})
	_ = UpdateLast(path, func(l *Last) error {
		l.Add("phone", Outcome{At: t0.Add(2 * time.Hour), OK: true}, true)
		return nil
	})
	l, _ := ReadLast(path)
	if len(l.Targets["phone"].Hour) != 1 {
		t.Fatalf("hour %v", l.Targets["phone"].Hour)
	}
}

func TestLastFileIs0600(t *testing.T) {
	path := LastPath(t.TempDir())
	_ = UpdateLast(path, func(l *Last) error {
		l.Add("phone", Outcome{At: t0, Err: "phone: 401 unauthorized"}, true)
		return nil
	})
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", st, err)
	}
}

// Two services (two projects) writing at once: every outcome survives.
func TestLastConcurrentWritersLoseNothing(t *testing.T) {
	path := LastPath(t.TempDir())
	var wg sync.WaitGroup
	for _, target := range []string{"a", "b"} {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				if err := UpdateLast(path, func(l *Last) error {
					l.Add(target, Outcome{At: t0.Add(time.Duration(i) * time.Second), OK: true}, true)
					return nil
				}); err != nil {
					t.Error(err)
				}
			}
		}(target)
	}
	wg.Wait()
	l, _ := ReadLast(path)
	if len(l.Targets["a"].Recent) != 15 || len(l.Targets["b"].Recent) != 15 {
		t.Fatalf("a %d b %d", len(l.Targets["a"].Recent), len(l.Targets["b"].Recent))
	}
}

func TestLastLockWaitedAtMost(t *testing.T) {
	dir := t.TempDir()
	path := LastPath(dir)
	f, err := os.OpenFile(filepath.Join(dir, "notify.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	old := lockWait
	lockWait = 150 * time.Millisecond
	defer func() { lockWait = old }()
	start := time.Now()
	err = UpdateLast(path, func(*Last) error { t.Fatal("ran without the lock"); return nil })
	if !errors.Is(err, ErrLockBusy) || time.Since(start) > 2*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestLastFnErrorWritesNothing(t *testing.T) {
	path := LastPath(t.TempDir())
	boom := errors.New("boom")
	if err := UpdateLast(path, func(l *Last) error {
		l.Add("phone", Outcome{At: t0}, true)
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("written: %v", err)
	}
}

func TestLastSourceFailures(t *testing.T) {
	path := LastPath(t.TempDir())
	_ = UpdateLast(path, func(l *Last) error {
		l.SetSources("demo", map[string]time.Time{"asks": t0}, t0)
		return nil
	})
	// A later pass still failing keeps the first since; a source back drops.
	_ = UpdateLast(path, func(l *Last) error {
		l.SetSources("demo", map[string]time.Time{"asks": t0.Add(time.Minute), "plan": t0.Add(time.Minute)}, t0.Add(time.Minute))
		return nil
	})
	l, _ := ReadLast(path)
	if got := l.Sources["demo"]; !got["asks"].Equal(t0) || !got["plan"].Equal(t0.Add(time.Minute)) {
		t.Fatalf("sources %v", got)
	}
	_ = UpdateLast(path, func(l *Last) error {
		l.SetSources("demo", nil, t0.Add(2*time.Minute))
		return nil
	})
	l, _ = ReadLast(path)
	if _, ok := l.Sources["demo"]; ok {
		t.Fatalf("a project reading every source again keeps no failure: %v", l.Sources)
	}
}

func TestReserveAndUnreserve(t *testing.T) {
	var l Last
	l.Reserve("phone", t0)
	l.Reserve("phone", t0.Add(time.Second))
	if l.SentInHour("phone", t0.Add(time.Minute)) != 2 {
		t.Fatal("reserved")
	}
	l.Unreserve("phone", t0)
	l.Unreserve("other", t0)
	if l.SentInHour("phone", t0.Add(time.Minute)) != 1 {
		t.Fatal("unreserved")
	}
}
