package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/brokerrt"
)

// fakeSnap is a broker whose BGSAVE finishes after polls polls (never when
// polls < 0), with status, its data file dir/file.
type fakeSnap struct {
	now      time.Time
	saves    int
	polls    int
	status   string
	bgErr    error
	dir      string
	file     string
	asked    bool
	noCounts bool // an older broker without rdb_saves
	lag      int  // polls after BGSAVE that show no save running yet (it starts late)
}

func (f *fakeSnap) Time(context.Context) (time.Time, error) { return f.now, nil }
func (f *fakeSnap) BgSave(context.Context) error {
	if f.bgErr != nil {
		return f.bgErr
	}
	f.asked = true
	return nil
}
func (f *fakeSnap) Persistence(context.Context) (map[string]string, error) {
	m := map[string]string{"rdb_bgsave_in_progress": "0", "rdb_last_bgsave_status": "ok", "rdb_last_save_time": strconv.FormatInt(f.now.Unix()-100, 10)}
	if f.asked && f.lag > 0 { // not started yet: nothing in progress, no new save
		f.lag--
	} else if f.asked {
		if f.polls != 0 {
			if f.polls > 0 {
				f.polls--
			}
			m["rdb_bgsave_in_progress"] = "1"
		} else {
			f.saves, f.asked = f.saves+1, false
			m["rdb_last_bgsave_status"] = f.status
			m["rdb_last_save_time"] = strconv.FormatInt(f.now.Unix()+1, 10)
		}
	} else if f.saves > 0 {
		m["rdb_last_save_time"] = strconv.FormatInt(f.now.Unix()+1, 10)
		m["rdb_last_bgsave_status"] = f.status
	}
	if !f.noCounts {
		m["rdb_saves"] = strconv.Itoa(f.saves)
	}
	return m, nil
}
func (f *fakeSnap) DataFile(context.Context) (string, string, error) { return f.dir, f.file, nil }

// rdbFile writes an RDB-looking file at path with mtime at.
func rdbFile(t *testing.T, path string, at time.Time) []byte {
	t.Helper()
	data := []byte("REDIS0012 some keys")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return data
}

func tarOf(t *testing.T, name string, data []byte, at time.Time) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: at, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(data)
	_ = w.Close()
	return b.Bytes()
}

func snapWith(f *fakeSnap, stage string) Snapshotter {
	return Snapshotter{broker: f, Dir: stage, Poll: time.Millisecond, Sleep: func(time.Duration) {}}
}

// A native broker (its data directory on this host): BGSAVE, the wait until
// that save finished ok, then the RDB itself, staged 0600.
func TestSnapshotFromTheHost(t *testing.T) {
	for _, noCounts := range []bool{false, true} {
		dir, stage := t.TempDir(), t.TempDir()
		now := time.Now().Truncate(time.Second)
		f := &fakeSnap{now: now, polls: 3, lag: 2, status: "ok", dir: dir, file: "dump.rdb", noCounts: noCounts}
		data := rdbFile(t, filepath.Join(dir, "dump.rdb"), now.Add(time.Second))
		path, why := snapWith(f, stage).Take(context.Background())
		if why != "" || filepath.Dir(path) != stage {
			t.Fatalf("noCounts %v: %q %q", noCounts, path, why)
		}
		// Only once the save asked for finished: not on a poll that shows
		// nothing running before it starts.
		if f.saves != 1 || f.asked {
			t.Fatalf("noCounts %v: returned before the save finished (saves %d)", noCounts, f.saves)
		}
		got, _ := os.ReadFile(path)
		st, _ := os.Stat(path)
		if !bytes.Equal(got, data) || st.Mode().Perm() != 0o600 {
			t.Fatalf("staged %q, mode %v", got, st.Mode().Perm())
		}
	}
}

// Valkey's RDB starts VALKEY, not REDIS (broker.image may be Valkey).
func TestSnapshotTakesAValkeyRDB(t *testing.T) {
	dir, stage := t.TempDir(), t.TempDir()
	now := time.Now().Truncate(time.Second)
	f := &fakeSnap{now: now, status: "ok", dir: dir, file: "dump.rdb"}
	_ = os.WriteFile(filepath.Join(dir, "dump.rdb"), []byte("VALKEY080 keys"), 0o600)
	_ = os.Chtimes(filepath.Join(dir, "dump.rdb"), now.Add(time.Second), now.Add(time.Second))
	if path, why := snapWith(f, stage).Take(context.Background()); why != "" || path == "" {
		t.Fatalf("%q %q", path, why)
	}
}

// The container (broker.runtime running agent-bus-redis): <runtime> cp
// agent-bus-redis:<dir>/<file> -, a tar of the one file; no other command,
// and never the AOF.
func TestSnapshotFromTheContainer(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	data := []byte("REDIS0012 container keys")
	var ran []string
	s := snapWith(&fakeSnap{now: now, status: "ok", dir: "/data", file: "dump.rdb"}, t.TempDir())
	s.Runtime = "podman"
	s.Output = func(_ context.Context, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "ps -a --filter name=^" + brokerrt.Container + "$ --format {{.State}}":
			return []byte("running\n"), nil
		case "cp " + brokerrt.Container + ":/data/dump.rdb -":
			return tarOf(t, "dump.rdb", data, now.Add(time.Second)), nil
		}
		t.Fatalf("unexpected command %s %q", name, args)
		return nil, nil
	}
	path, why := s.Take(context.Background())
	got, _ := os.ReadFile(path)
	if why != "" || !bytes.Equal(got, data) || len(ran) != 2 {
		t.Fatalf("%q %q, ran %q", got, why, ran)
	}
	for _, r := range ran {
		if strings.Contains(r, "appendonly") || strings.Contains(r, ".aof") {
			t.Fatalf("an AOF read: %q", r)
		}
	}
}

// Every way there is no snapshot: nothing staged, the reason said.
func TestSnapshotLeftOut(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, c := range []struct {
		name string
		set  func(f *fakeSnap, dir string, s *Snapshotter)
		want string
	}{
		{"BGSAVE refused", func(f *fakeSnap, _ string, _ *Snapshotter) {
			f.bgErr = errors.New("ERR Background save already in progress")
		}, "BGSAVE refused: ERR Background save already in progress"},
		{"never finishes", func(f *fakeSnap, _ string, s *Snapshotter) { f.polls, s.Wait = -1, 20*time.Millisecond }, "BGSAVE did not finish within 20ms"},
		{"the save failed", func(f *fakeSnap, _ string, _ *Snapshotter) { f.status = "err" }, "the save failed (rdb_last_bgsave_status err)"},
		{"no data directory here", func(f *fakeSnap, _ string, _ *Snapshotter) { f.dir = "/nowhere/data" }, "the broker's data directory is not on this host"},
		{"an older RDB", func(f *fakeSnap, dir string, _ *Snapshotter) {
			rdbFile(t, filepath.Join(dir, "dump.rdb"), now.Add(-time.Hour))
		}, "predates the save asked for"},
		{"not an RDB", func(f *fakeSnap, dir string, _ *Snapshotter) {
			_ = os.WriteFile(filepath.Join(dir, "dump.rdb"), []byte("hello, not a snapshot"), 0o600)
			_ = os.Chtimes(filepath.Join(dir, "dump.rdb"), now.Add(time.Second), now.Add(time.Second))
		}, "not an RDB file"},
		{"the container's copy is older", func(f *fakeSnap, _ string, s *Snapshotter) {
			s.Runtime = "docker"
			s.Output = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if args[0] == "ps" {
					return []byte("running\n"), nil
				}
				return tarOf(t, "dump.rdb", []byte("REDIS0012"), now.Add(-time.Hour)), nil
			}
		}, "predates the save asked for"},
		{"the container's cp fails", func(f *fakeSnap, _ string, s *Snapshotter) {
			s.Runtime = "docker"
			s.Output = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if args[0] == "ps" {
					return []byte("running\n"), nil
				}
				return nil, errors.New("exit status 1")
			}
		}, "docker cp /data/dump.rdb: exit status 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, stage := t.TempDir(), t.TempDir()
			f := &fakeSnap{now: now, status: "ok", dir: dir, file: "dump.rdb"}
			s := snapWith(f, stage)
			if c.name == "the container's cp fails" || c.name == "the container's copy is older" {
				f.dir = "/data"
			}
			c.set(f, dir, &s)
			path, why := s.Take(context.Background())
			if path != "" || !strings.Contains(why, c.want) {
				t.Fatalf("%q %q", path, why)
			}
			if left, _ := os.ReadDir(stage); len(left) != 0 {
				t.Fatalf("staged files left: %v", left)
			}
		})
	}
}

// Run: broker.rdb after the logical dump, removed from the stage after;
// left out, it is in the manifest's left_out with the reason.
func TestRunArchivesTheSnapshot(t *testing.T) {
	dir, stage := t.TempDir(), t.TempDir()
	staged := filepath.Join(stage, ".broker-1.rdb")
	_ = os.WriteFile(staged, []byte("REDIS0012 keys"), 0o600)
	p := plan(t, "demo")
	p.Snapshot = func(context.Context) (string, string) { return staged, "" }
	res, err := Run(context.Background(), dir, p)
	if err != nil || res.Snapshot != int64(len("REDIS0012 keys")) {
		t.Fatalf("%+v %v", res, err)
	}
	b, _ := os.ReadFile(res.Path)
	names, got := members(t, b)
	if got[SnapshotMember] != "REDIS0012 keys" {
		t.Fatalf("members %v", names)
	}
	for m := range got {
		if strings.Contains(m, "aof") || strings.HasSuffix(m, ".aof") {
			t.Fatalf("an AOF archived: %s", m)
		}
	}
	if _, err := os.Stat(staged); err == nil {
		t.Fatal("the staged copy was kept")
	}

	p = plan(t, "demo")
	p.Now = p.Now.Add(time.Second)
	p.Snapshot = func(context.Context) (string, string) { return "", "BGSAVE did not finish within 2m0s" }
	res, err = Run(context.Background(), dir, p)
	if err != nil || res.Snapshot != 0 || res.SnapshotWhy == "" {
		t.Fatalf("%+v %v", res, err)
	}
	b, _ = os.ReadFile(res.Path)
	_, got = members(t, b)
	if _, ok := got[SnapshotMember]; ok {
		t.Fatal("broker.rdb archived without a snapshot")
	}
	if man := got["manifest.json"]; !strings.Contains(man, `"path": "broker.rdb"`) || !strings.Contains(man, "BGSAVE did not finish") {
		t.Fatalf("manifest %s", man)
	}
}

// The real deps take it; a MakeArchive without the hook takes none.
func TestRealDepsSnapshot(t *testing.T) {
	if RealDeps("").Snapshot == nil {
		t.Fatal("RealDeps takes no snapshot")
	}
}
