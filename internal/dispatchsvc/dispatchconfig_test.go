package dispatchsvc

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
	"github.com/netbja/agent-bus-monitor/service"
)

// merge-queue --live derives the queue in the current directory: true, and a
// record whose failed read is its reason (the broker here is unreachable).
func TestMergeQueueLiveDerivesHere(t *testing.T) {
	b, err := bus.Open(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "demo")
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := MergeQueueLive(context.Background(), b, func(string) string { return "" })
	if !ok {
		t.Fatal("a working directory exists: MergeQueueLive must derive")
	}
	if rec.Reason == "" {
		t.Fatalf("an unreachable broker derived a clean queue: %+v", rec)
	}
}

// unitsUnreadable is a holder manager whose units cannot be read.
type unitsUnreadable struct{ *cmdtest.Holders }

func (unitsUnreadable) HolderUnits(string) ([]service.HolderUnit, error) {
	return nil, errors.New("units unreadable")
}

// The health checks read the service manager the caller gives in
// Deps.Manager (dispatchsvc spec §3), never one detected again: the holder
// check of a manager whose units are unreadable has no evidence.
func TestServiceDispatchConfigReadsDepsManager(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	b, err := bus.Open(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "demo") // never dialled
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root+"/run", 0o700); err != nil { // the holder's sockets: none
		t.Fatal(err)
	}
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	holderCheckOf := func(m service.Manager) error {
		t.Helper()
		var cfg dispatch.Config
		d := Deps{Bus: b, Getenv: os.Getenv, Paths: paths, Log: io.Discard, Base: dispatch.DefaultConfig(), Manager: m,
			PopEnd: func(c dispatch.PopRetireConfig, _ dispatch.Runtime) dispatch.PopRetireConfig { return c }}
		if _, err := Config(d, &cfg); err != nil {
			t.Fatal(err)
		}
		for _, c := range cfg.Health.Checks {
			if c.Definition.ID == "holder-leftovers" {
				_, err := c.Run(context.Background())
				return err
			}
		}
		t.Fatal("no holder-leftovers check")
		return nil
	}
	if err := holderCheckOf(unitsUnreadable{&cmdtest.Holders{Manager: cmdtest.NewManager()}}); err == nil {
		t.Fatal("a manager whose units are unreadable gave the holder evidence")
	}
	if err := holderCheckOf(&cmdtest.Holders{Manager: cmdtest.NewManager()}); err != nil {
		t.Fatalf("a readable manager: %v", err)
	}
	// The foreground dispatcher's checks read Deps.Manager too.
	var fcfg dispatch.Config
	ForegroundConfig(Deps{Bus: b, Manager: unitsUnreadable{&cmdtest.Holders{Manager: cmdtest.NewManager()}},
		PopEnd: func(c dispatch.PopRetireConfig, _ dispatch.Runtime) dispatch.PopRetireConfig { return c }}, &fcfg, nil) // the holder checks read no runtime
	for _, c := range fcfg.Health.Checks {
		if c.Definition.ID == "holder-leftovers" {
			if _, err := c.Run(context.Background()); err == nil {
				t.Fatal("the foreground's holder check did not read Deps.Manager")
			}
			return
		}
	}
	t.Fatal("no foreground holder-leftovers check")
}

// Deps.SelfRepairSkew moves the time each self-repair read got from Redis
// TIME (tests only); nil leaves it.
func TestServiceDispatchConfigSkewsTheSelfRepairRead(t *testing.T) {
	b := notifyBus(t) // holds the lease as "tok"
	ctx := context.Background()
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	dir := filepath.Join(root, "cfg", "agentbus", "projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := "name = \"" + b.Project() + "\"\n[[tabs]]\nname = \"busmon\"\ncommand = \"busmon --project " + b.Project() + "\"\n" // no agent to relaunch
	if err := os.WriteFile(filepath.Join(dir, b.Project()+".toml"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	t.Setenv("ROLES_TOML", filepath.Join(wd, "..", "..", "roles.toml"))
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	var skewed []time.Time
	d := Deps{Bus: b, Getenv: os.Getenv, Paths: paths, Log: io.Discard, Base: dispatch.DefaultConfig(), Boot: "B7",
		PopEnd:         func(c dispatch.PopRetireConfig, _ dispatch.Runtime) dispatch.PopRetireConfig { return c },
		SelfRepairSkew: func(now time.Time) time.Time { skewed = append(skewed, now); return now.Add(time.Hour) }}
	var cfg dispatch.Config
	if _, err := Config(d, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Memory.WarnAt == nil || cfg.Memory.Caps == nil { // agent-resource-limits step 3e
		t.Fatal("no memory notice: limits.warn_at or the holder caps unread")
	}
	if cfg.SelfRepair.Pass == nil {
		t.Fatal("no self-repair pass (dispatch.self_repair is on by default)")
	}
	if err := cfg.SelfRepair.Pass(ctx, "tok"); err != nil {
		t.Fatal(err)
	}
	if len(skewed) != 1 || skewed[0].IsZero() {
		t.Fatalf("one read, its Redis time skewed: %v", skewed)
	}
}

// SelfRepairSkew is for tests only: no non-test file of the module sets it.
func TestNoProductionSelfRepairSkew(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() && (strings.HasPrefix(e.Name(), ".") && path != root || e.Name() == "testdata") {
			return filepath.SkipDir
		}
		if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok && id.Name == "SelfRepairSkew" {
					t.Errorf("%s sets SelfRepairSkew", path)
				}
			case *ast.AssignStmt:
				for _, l := range n.Lhs {
					if s, ok := l.(*ast.SelectorExpr); ok && s.Sel.Name == "SelfRepairSkew" {
						t.Errorf("%s sets SelfRepairSkew", path)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// spec §3: the escalation target is read through the config the caller
// keeps. Set after Config returns, it is the target the stall notice names.
func TestServiceDispatchConfigReadsTheTargetSetAfter(t *testing.T) {
	w := cmdtest.NewStallWorld(t)
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	d := Deps{Bus: w.B, Getenv: os.Getenv, Paths: paths, Log: io.Discard, Base: dispatch.DefaultConfig(), Boot: "B7",
		PopEnd: func(c dispatch.PopRetireConfig, _ dispatch.Runtime) dispatch.PopRetireConfig { return c }}
	var cfg dispatch.Config
	if _, err := Config(d, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.EscalateTo = "lead" // after the call, as RunDispatch sets it
	rec := w.PutStall()
	if err := cfg.Health.StallNotify(context.Background(), "stall-holder", rec); err != nil {
		t.Fatal(err)
	}
	if notes := w.PersonNotices(); len(notes) != 1 || !strings.Contains(notes[0], "lead cannot act (lead is not registered)") {
		t.Fatalf("stall notice %q, want it to name lead", notes)
	}
}
