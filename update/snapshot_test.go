package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func binDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, n := range []string{"agentbus", "busmon"} {
		p := filepath.Join(d, n)
		if err := os.WriteFile(p, []byte("old "+n), 0o751); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, old, old)
	}
	return d
}

// TestSnapshotKeepsBinaries: both binaries copied with their mode and times
// (an arm64 signature is inside the file: the copy keeps it, §6), the
// manifest last; a failure leaves no snapshot.
func TestSnapshotKeepsBinaries(t *testing.T) {
	bin := binDir(t)
	dir := filepath.Join(t.TempDir(), "updates", "id1")
	if err := WriteSnapshot(dir, Manifest{From: "v0.1.0", To: "v0.2.0", BinDir: bin}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"agentbus", "busmon"} {
		st, err := os.Stat(filepath.Join(dir, "bin", n))
		b, _ := os.ReadFile(filepath.Join(dir, "bin", n))
		if err != nil || st.Mode().Perm() != 0o751 || !st.ModTime().Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) || string(b) != "old "+n {
			t.Fatalf("%s: %v %v %v %q", n, err, st.Mode(), st.ModTime(), b)
		}
	}
	m, err := ReadManifest(dir)
	if err != nil || m.From != "v0.1.0" || m.Format != manifestFormat {
		t.Fatalf("%+v %v", m, err)
	}
	_ = os.Remove(filepath.Join(bin, "busmon"))
	bad := filepath.Join(filepath.Dir(dir), "id2")
	if err := WriteSnapshot(bad, Manifest{BinDir: bin}); err == nil {
		t.Fatal("a missing binary wrote a snapshot")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("a failed snapshot left %s", bad)
	}
}

// TestSnapshotsInterruptedPrune: an incomplete snapshot is never listed; the
// last one with no history line is an interrupted update; Prune keeps 3.
func TestSnapshotsInterruptedPrune(t *testing.T) {
	updates := t.TempDir()
	bin := binDir(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := WriteSnapshot(filepath.Join(updates, id), Manifest{BinDir: bin}); err != nil {
			t.Fatal(err)
		}
		if id != "d" {
			_ = AppendHistory(updates, HistoryLine{Kind: "update", OK: true, Snapshot: id})
		}
	}
	_ = os.MkdirAll(filepath.Join(updates, "e", "bin"), 0o700) // no manifest
	if got := Snapshots(updates); len(got) != 4 || got[3] != "d" {
		t.Fatalf("snapshots %v", got)
	}
	if Interrupted(updates) != "d" {
		t.Fatalf("interrupted %q", Interrupted(updates))
	}
	_ = AppendHistory(updates, HistoryLine{Kind: "update", OK: false, Snapshot: "d"})
	if Interrupted(updates) != "" {
		t.Fatal("a snapshot with its history line is not interrupted")
	}
	if h := History(updates); len(h) != 4 || h[3].OK || h[0].Snapshot != "a" {
		t.Fatalf("history %+v", h)
	}
	if err := Prune(updates); err != nil {
		t.Fatal(err)
	}
	if got := Snapshots(updates); len(got) != Keep || got[0] != "b" {
		t.Fatalf("after prune %v", got)
	}
}

// TestLockOneAtATime: a second update refuses while the first holds the lock.
func TestLockOneAtATime(t *testing.T) {
	updates := t.TempDir()
	unlock, err := Lock(updates)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(updates); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	unlock()
	again, err := Lock(updates)
	if err != nil {
		t.Fatal(err)
	}
	again()
}
