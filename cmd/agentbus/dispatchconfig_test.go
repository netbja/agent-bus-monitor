package main

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
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
