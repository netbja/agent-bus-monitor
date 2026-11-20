package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/service"
)

func newHolderNoticeBus(t *testing.T) *bus.Bus {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "hn"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "hn-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if err := b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	return b
}

// putHolder stores a holder record as the health loop does and returns it
// as stored (its since, Redis time).
func putHolder(t *testing.T, b *bus.Bus, st health.Status, detail string) health.Record {
	t.Helper()
	ctx := context.Background()
	rec := health.Record{ID: "holder", Status: st, Detail: detail, Fingerprint: string(st) + detail}
	if err := b.FencedHealthPut(ctx, "hn-holder", "master", rec); err != nil {
		t.Fatal(err)
	}
	recs, err := b.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return recs["holder"]
}

func personNoticesOf(t *testing.T, b *bus.Bus) []string {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{"notify"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.For == bus.ForPerson {
			out = append(out, e.Message)
		}
	}
	return out
}

// H3 (holder hardening §4): a holder that does not answer rings the person
// once per episode, naming the project and the command; a second pass, an
// unknown pass in between, or a dispatcher restart (the mark is in Redis)
// does not ring again. A warn rings nothing. Once the fail ends the mark
// goes, and the next fail is a new episode.
func TestHolderDownNoticeOncePerEpisode(t *testing.T) {
	b := newHolderNoticeBus(t)
	ctx := context.Background()
	notice := holderNotice(b)
	warn := putHolder(t, b, health.Warn, "holder build v1 restarted 1m ago: 1 agent(s) lost: coder")
	if err := notice(ctx, "hn-holder", warn); err != nil || len(personNoticesOf(t, b)) != 0 {
		t.Fatalf("a warn: err %v, notices %q", err, personNoticesOf(t, b))
	}
	fail := putHolder(t, b, health.Fail, "the holder service of build v1 is installed and no holder answers")
	for i := 0; i < 2; i++ {
		if err := holderNotice(b)(ctx, "hn-holder", fail); err != nil { // a fresh closure: a restarted dispatcher
			t.Fatal(err)
		}
	}
	got := personNoticesOf(t, b)
	if len(got) != 1 {
		t.Fatalf("notices %q", got)
	}
	for _, want := range []string{holderNoticePrefix, b.Project(), "no holder answers", "agentbus --project " + b.Project() + " service start holder"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("notice %q misses %q", got[0], want)
		}
	}
	unknown := putHolder(t, b, health.Unknown, "check could not be completed")
	if err := notice(ctx, "hn-holder", unknown); err != nil {
		t.Fatal(err)
	}
	fail = putHolder(t, b, health.Fail, "the holder service of build v1 is installed and no holder answers")
	if err := notice(ctx, "hn-holder", fail); err != nil || len(personNoticesOf(t, b)) != 1 {
		t.Fatalf("fail, unknown, fail: err %v, notices %q", err, personNoticesOf(t, b))
	}
	ok := putHolder(t, b, health.OK, "holder build v1 answers, 0 terminal(s)")
	if err := notice(ctx, "hn-holder", ok); err != nil {
		t.Fatal(err)
	}
	if m, _ := b.QuotaMarks(ctx); m[holderDownMark] != "" {
		t.Fatalf("the mark outlived its episode: %q", m[holderDownMark])
	}
	fail = putHolder(t, b, health.Fail, "the holder service of build v1 is installed and no holder answers")
	if err := notice(ctx, "hn-holder", fail); err != nil || len(personNoticesOf(t, b)) != 2 {
		t.Fatalf("a new episode: err %v, notices %q", err, personNoticesOf(t, b))
	}
}

// A lost lease writes nothing.
func TestHolderDownNoticeWithoutTheLease(t *testing.T) {
	b := newHolderNoticeBus(t)
	fail := putHolder(t, b, health.Fail, "no holder answers")
	if err := holderNotice(b)(context.Background(), "not-the-holder", fail); err == nil || len(personNoticesOf(t, b)) != 0 {
		t.Fatalf("err %v, notices %q", err, personNoticesOf(t, b))
	}
}

// The notifier's fail event already covers a failing holder: its notice is
// never a second line (as the stall guard's).
func TestHolderDownNoticeIsNoSecondNotifierLine(t *testing.T) {
	now := time.Now()
	f := notifyFacts{
		Health:  map[string]health.Record{"holder": {ID: "holder", Status: health.Fail, Detail: "no holder answers", Since: now.UnixMilli(), ObservedAt: now.UnixMilli()}},
		Notices: []bus.RawEntry{{ID: strconv.FormatInt(now.UnixMilli(), 10) + "-0", Fields: map[string]string{"for": "person", "from": "dispatch", "message": holderNoticePrefix + "demo: the holder does not answer"}}},
	}
	evs := notifyEvents(f, nil, now)
	if len(evs) != 1 || evs[0].Kind != "fail" {
		t.Fatalf("events %+v", evs)
	}
}

// foureyes: the dispatch service's own health config carries the notice,
// so dropping it from realHealthConfig fails here, not silently in
// production: its hook, given a stored holder fail, rings the person.
func TestRealHealthConfigRingsTheHolderDown(t *testing.T) {
	b := newHolderNoticeBus(t)
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME": filepath.Join(home, ".state"), "XDG_DATA_HOME": filepath.Join(home, ".data"),
		config.EnvName("services.manager"): "none"} {
		t.Setenv(k, v)
	}
	paths, err := service.PathsFrom(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	master := "master"
	manager, _ := service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, os.Getenv)), os.Getenv) // the host's, as the service's caller detects it
	cfg := realHealthConfig(b, nil, manager, paths, dispatch.ClearConfig{}, &master)
	if cfg.HolderNotify == nil {
		t.Fatal("the dispatch service's health config has no holder notice")
	}
	fail := putHolder(t, b, health.Fail, "the holder service of build v1 is installed and no holder answers")
	if err := cfg.HolderNotify(context.Background(), "hn-holder", fail); err != nil {
		t.Fatal(err)
	}
	if got := personNoticesOf(t, b); len(got) != 1 || !strings.HasPrefix(got[0], holderNoticePrefix) {
		t.Fatalf("notices %q", got)
	}
}
