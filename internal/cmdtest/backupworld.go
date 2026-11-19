package cmdtest

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/bustest"
)

// BackupWorld: the disposable broker with one project holding a board, a
// temporary HOME (config.toml, a template, a password file) and a fixed clock.
type BackupWorld struct {
	B    *bus.Bus
	Home string
	Env  map[string]string
	D    backup.Deps
}

func NewBackupWorld(t *testing.T) *BackupWorld {
	t.Helper()
	b, r := bustest.Open(t)
	ctx := context.Background()
	t.Cleanup(func() { bus.DeleteProject(context.Background(), r, b.Project()) })
	if err := r.HSet(ctx, bus.BoardKey(b.Project()), "t1", `{"owner":"coder","state":"accepted"}`).Err(); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	w := &BackupWorld{B: b, Home: home, Env: map[string]string{"HOME": home}}
	cfg := filepath.Join(home, ".config", "agentbus")
	os.MkdirAll(filepath.Join(cfg, "projects"), 0o700)
	os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("[backup]\nkeep = 2\n"), 0o600)
	os.WriteFile(filepath.Join(cfg, "projects", b.Project()+".toml"), []byte("working_dir = \"/w\"\n"), 0o600)
	os.WriteFile(filepath.Join(cfg, "redis.env"), []byte(`REDIS_PASSWORD="pw-SECRET"`+"\n"), 0o600)
	clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	w.D = backup.Deps{
		Getenv: func(k string) string { return w.Env[k] },
		Now:    func() time.Time { clock = clock.Add(time.Second); return clock },
		Host:   "h1",
		Dial:   func() (*redis.Client, error) { return r, nil },
	}
	return w
}

func (w *BackupWorld) Dir() string {
	return filepath.Join(w.Home, ".local", "share", "agentbus", "backups")
}

// scheduleWorld: a BackupWorld whose config.toml says daily, backup.dir set.
func NewScheduleWorld(t *testing.T, extra string) *BackupWorld {
	t.Helper()
	w := NewBackupWorld(t)
	os.WriteFile(filepath.Join(w.Home, ".config", "agentbus", "config.toml"),
		[]byte("[backup]\nschedule = \"daily\"\ndir = \""+w.Dir()+"\"\n"+extra), 0o600)
	// A fresh client per dial, as the service has: the pass closes its own.
	w.D.Dial = func() (*redis.Client, error) { return bus.Connect("") }
	// The shared test broker holds other packages' projects: the scope is
	// this world's project and its templates.
	w.D.Projects = func(_ context.Context, _ *redis.Client, dir string) ([]string, error) {
		out := []string{w.B.Project()}
		tpls, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
		for _, tpl := range tpls {
			out = append(out, strings.TrimSuffix(filepath.Base(tpl), ".toml"))
		}
		slices.Sort(out)
		return slices.Compact(out), nil
	}
	return w
}

func (w *BackupWorld) Last(t *testing.T) backup.Last {
	t.Helper()
	l, err := backup.ReadLast(filepath.Join(w.Home, ".local", "state", "agentbus", "backup-last.json"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}
