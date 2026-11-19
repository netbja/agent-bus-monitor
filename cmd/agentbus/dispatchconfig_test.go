package main

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

// popEndProbe is a Deps.PopEnd that records the runtime it was given and
// marks the config it returns.
type popEndProbe struct {
	called bool
	rt     dispatch.Runtime
}

func (p *popEndProbe) end(c dispatch.PopRetireConfig, rt dispatch.Runtime) dispatch.PopRetireConfig {
	p.called, p.rt = true, rt
	c.After = 4242 * time.Second
	return c
}

// The pop's end reaches both configurations through Deps.PopEnd only
// (dispatchsvc spec §4, P8 step 4): the service's, with the runtime it built,
// and the foreground's, with the caller's runtime.
func TestDispatchConfigsTakeThePopEndFromDeps(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	b, err := bus.Open(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "demo") // never dialled
	if err != nil {
		t.Fatal(err)
	}
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}

	svc := &popEndProbe{}
	d := serviceDispatchDeps(b, "", "", paths, io.Discard)
	d.PopEnd = svc.end
	var cfg dispatch.Config
	h, err := serviceDispatchConfig(d, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !svc.called || cfg.PopRetire.After != 4242*time.Second {
		t.Fatalf("the service's pop retire did not come from Deps.PopEnd: called %v, after %v", svc.called, cfg.PopRetire.After)
	}
	if svc.rt != dispatch.Runtime(h) {
		t.Fatalf("Deps.PopEnd got runtime %v, want the service's own %v", svc.rt, h)
	}

	fg := &popEndProbe{}
	var fcfg dispatch.Config
	rt := noRuntime{t}
	foregroundConfig(dispatchDeps{Bus: b, PopEnd: fg.end}, &fcfg, rt)
	if !fg.called || fcfg.PopRetire.After != 4242*time.Second || fg.rt != dispatch.Runtime(rt) {
		t.Fatalf("the foreground's pop retire did not come from Deps.PopEnd with its runtime: called %v, after %v", fg.called, fcfg.PopRetire.After)
	}
}

// merge-queue --live derives the queue in the current directory: true, and a
// record whose failed read is its reason (the broker here is unreachable).
func TestMergeQueueLiveDerivesHere(t *testing.T) {
	b, err := bus.Open(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "demo")
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := mergeQueueLive(context.Background(), b, func(string) string { return "" })
	if !ok {
		t.Fatal("a working directory exists: mergeQueueLive must derive")
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
		d := dispatchDeps{Bus: b, Getenv: os.Getenv, Paths: paths, Log: io.Discard, Base: dispatch.DefaultConfig(), Manager: m,
			PopEnd: func(c dispatch.PopRetireConfig, _ dispatch.Runtime) dispatch.PopRetireConfig { return c }}
		if _, err := serviceDispatchConfig(d, &cfg); err != nil {
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
	foregroundConfig(dispatchDeps{Bus: b, Manager: unitsUnreadable{&cmdtest.Holders{Manager: cmdtest.NewManager()}},
		PopEnd: func(c dispatch.PopRetireConfig, _ dispatch.Runtime) dispatch.PopRetireConfig { return c }}, &fcfg, noRuntime{t})
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
	d := dispatchDeps{Bus: b, Getenv: os.Getenv, Paths: paths, Log: io.Discard, Base: dispatch.DefaultConfig(), Boot: "B7",
		PopEnd:         func(c dispatch.PopRetireConfig, _ dispatch.Runtime) dispatch.PopRetireConfig { return c },
		SelfRepairSkew: func(now time.Time) time.Time { skewed = append(skewed, now); return now.Add(time.Hour) }}
	var cfg dispatch.Config
	if _, err := serviceDispatchConfig(d, &cfg); err != nil {
		t.Fatal(err)
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
