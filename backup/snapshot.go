package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/internal/brokerrt"
)

// The broker's snapshot in an archive (broker-hardening spec, step 5):
// after the logical dump, BGSAVE; once INFO persistence shows that save
// finished, and finished ok, the RDB it completed (dbfilename in CONFIG GET
// dir) is copied whole as broker.rdb. Redis writes an RDB to a temporary file
// and renames it, so the copy is one point-in-time snapshot, with the keys
// whose TTL the logical dump skips. The AOF files are never copied: Redis
// appends to them while a copy runs. Anything that fails leaves the archive
// logical only, with the reason. Restoring broker.rdb is manual (the
// operator notes), never automatic.

// SnapshotMember is the archive's member for the broker's RDB.
const SnapshotMember = "broker.rdb"

// snapBroker is the broker side of a snapshot.
type snapBroker interface {
	Time(ctx context.Context) (time.Time, error)
	BgSave(ctx context.Context) error
	Persistence(ctx context.Context) (map[string]string, error)
	DataFile(ctx context.Context) (dir, file string, err error)
}

// redisSnap is snapBroker on a client.
type redisSnap struct{ c *redis.Client }

func (r redisSnap) Time(ctx context.Context) (time.Time, error) { return r.c.Time(ctx).Result() }
func (r redisSnap) BgSave(ctx context.Context) error            { return r.c.BgSave(ctx).Err() }

func (r redisSnap) Persistence(ctx context.Context) (map[string]string, error) {
	s, err := r.c.Info(ctx, "persistence").Result()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, l := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), ":"); ok {
			out[k] = v
		}
	}
	return out, nil
}

func (r redisSnap) DataFile(ctx context.Context) (string, string, error) {
	get := func(name string) (string, error) {
		m, err := r.c.ConfigGet(ctx, name).Result()
		if err != nil {
			return "", err
		}
		if v, ok := m[name]; ok && v != "" {
			return v, nil
		}
		return "", fmt.Errorf("CONFIG GET %s: no value", name)
	}
	dir, err := get("dir")
	if err != nil {
		return "", "", err
	}
	file, err := get("dbfilename")
	return dir, file, err
}

// Snapshotter takes one snapshot of the broker.
type Snapshotter struct {
	broker snapBroker
	// Runtime is the container runtime's binary (brokerrt); Output runs it
	// and returns its stdout. Either empty: no container is read, only a
	// broker whose data directory is on this host.
	Runtime string
	Output  func(ctx context.Context, name string, args ...string) ([]byte, error)
	Dir     string        // where the copy is staged (the archive's directory)
	Wait    time.Duration // how long the save may take; 0: DefaultSnapshotWait
	Poll    time.Duration // 0: 200 ms
	Sleep   func(time.Duration)
}

// DefaultSnapshotWait bounds the wait for BGSAVE.
const DefaultSnapshotWait = 2 * time.Minute

// NewSnapshotter is a Snapshotter of the broker c answers for.
func NewSnapshotter(c *redis.Client, runtime string, output func(ctx context.Context, name string, args ...string) ([]byte, error), dir string) Snapshotter {
	return Snapshotter{broker: redisSnap{c}, Runtime: runtime, Output: output, Dir: dir}
}

// Take saves and copies the RDB: the staged file's path (the caller
// removes it), or why there is none.
func (s Snapshotter) Take(ctx context.Context) (string, string) {
	wait, poll, sleep := s.Wait, s.Poll, s.Sleep
	if wait <= 0 {
		wait = DefaultSnapshotWait
	}
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	before, err := s.broker.Persistence(ctx)
	if err != nil {
		return "", "INFO persistence: " + err.Error()
	}
	asked, err := s.broker.Time(ctx)
	if err != nil {
		return "", "TIME: " + err.Error()
	}
	if err := s.broker.BgSave(ctx); err != nil {
		return "", "BGSAVE refused: " + err.Error()
	}
	for start := time.Now(); ; sleep(poll) {
		now, err := s.broker.Persistence(ctx)
		if err != nil {
			return "", "INFO persistence: " + err.Error()
		}
		if now["rdb_bgsave_in_progress"] == "0" && newSave(before, now, asked) {
			if st := now["rdb_last_bgsave_status"]; st != "ok" {
				return "", "the save failed (rdb_last_bgsave_status " + st + ")"
			}
			break
		}
		if time.Since(start) >= wait {
			return "", "BGSAVE did not finish within " + wait.String()
		}
	}
	dir, file, err := s.broker.DataFile(ctx)
	if err != nil {
		return "", err.Error()
	}
	src := path.Join(dir, file)
	if s.Runtime != "" && s.Output != nil {
		out, err := s.Output(ctx, s.Runtime, "ps", "-a", "--filter", "name=^"+brokerrt.Container+"$", "--format", "{{.State}}")
		if err == nil && strings.TrimSpace(string(out)) == "running" {
			return s.fromContainer(ctx, src, asked)
		}
	}
	if st, err := os.Stat(src); err == nil && st.Mode().IsRegular() {
		return s.fromHost(src, asked)
	}
	return "", "the broker's data directory is not on this host (no running " + brokerrt.Container + ", no " + src + ")"
}

// newSave: a save finished after asked, by the save count when the broker
// reports it, else by the last save's time.
func newSave(before, now map[string]string, asked time.Time) bool {
	if b, err := strconv.ParseInt(before["rdb_saves"], 10, 64); err == nil {
		n, err := strconv.ParseInt(now["rdb_saves"], 10, 64)
		return err == nil && n > b
	}
	at, err := strconv.ParseInt(now["rdb_last_save_time"], 10, 64)
	return err == nil && at >= asked.Unix() && now["rdb_last_save_time"] != before["rdb_last_save_time"]
}

// fromContainer reads <runtime> cp <container>:<src> -, a tar of the one
// file.
func (s Snapshotter) fromContainer(ctx context.Context, src string, asked time.Time) (string, string) {
	out, err := s.Output(ctx, s.Runtime, "cp", brokerrt.Container+":"+src, "-")
	if err != nil {
		return "", s.Runtime + " cp " + src + ": " + err.Error()
	}
	tr := tar.NewReader(bytes.NewReader(out))
	h, err := tr.Next()
	if err != nil || h.Typeflag != tar.TypeReg {
		return "", s.Runtime + " cp " + src + ": not one regular file"
	}
	return s.stage(tr, h.ModTime, asked)
}

// fromHost reads the file itself (a broker running natively).
func (s Snapshotter) fromHost(src string, asked time.Time) (string, string) {
	f, err := os.Open(src)
	if err != nil {
		return "", err.Error()
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err.Error()
	}
	return s.stage(io.LimitReader(f, st.Size()), st.ModTime(), asked)
}

// stage writes r to a 0600 file in s.Dir. The file must be written by the
// save asked for (its time not before it: an older one is another broker's,
// or a save that did not reach the disk) and be an RDB.
func (s Snapshotter) stage(r io.Reader, mod, asked time.Time) (string, string) {
	if mod.Unix() < asked.Unix() {
		return "", "the RDB found predates the save asked for (" + mod.UTC().Format(time.RFC3339) + "): not this broker's, or not written"
	}
	f, err := os.CreateTemp(s.Dir, ".broker-*.rdb")
	if err != nil {
		return "", err.Error()
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return "", err.Error()
	}
	head := make([]byte, 6) // the RDB magic: REDIS, or VALKEY (Valkey 8.1+, broker.image)
	if _, err := io.ReadFull(r, head); err != nil || !strings.HasPrefix(string(head), "REDIS") && string(head) != "VALKEY" {
		return "", "not an RDB file"
	}
	if _, err := f.Write(head); err != nil {
		return "", err.Error()
	}
	if _, err := io.Copy(f, r); err != nil {
		return "", err.Error()
	}
	if err := f.Close(); err != nil {
		return "", err.Error()
	}
	ok = true
	return f.Name(), ""
}
