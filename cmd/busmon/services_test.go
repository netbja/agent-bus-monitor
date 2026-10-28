package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

func TestServiceFact(t *testing.T) {
	now := time.Now()
	fresh, old := now.Add(-3*time.Second).UnixMilli(), now.Add(-time.Minute).UnixMilli()
	cases := []struct {
		name   string
		rec    bus.ServiceRecord
		has    bool
		legacy string
		want   string
	}{
		{"no record: the tab-mode facts", bus.ServiceRecord{}, false, "up", "up"},
		{"no record, nothing", bus.ServiceRecord{}, false, "", ""},
		{"ready and fresh", bus.ServiceRecord{State: "ready", At: fresh}, true, "up", "ready"},
		{"waiting for herdr", bus.ServiceRecord{State: "waiting-herdr", At: fresh}, true, "up", "waiting-herdr"},
		{"missions waits for git", bus.ServiceRecord{State: "waiting-git", At: fresh}, true, "", "waiting-git"},
		{"waiting for git gone stale", bus.ServiceRecord{State: "waiting-git", At: old}, true, "", "down"},
		{"idle conductor", bus.ServiceRecord{State: "idle", At: fresh}, true, "up", "idle"},
		{"a running state gone stale: the process died", bus.ServiceRecord{State: "ready", At: old}, true, "down", "down"},
		{"stale record, a tab holds the lease", bus.ServiceRecord{State: "ready", At: old}, true, "up", "up"},
		{"failed stays visible", bus.ServiceRecord{State: "failed", At: old}, true, "down", "failed"},
		{"gave up stays visible", bus.ServiceRecord{State: "gave-up", At: old}, true, "down", "gave-up"},
		{"stopped by down", bus.ServiceRecord{State: "stopped", At: old}, true, "down", "stopped"},
		{"standby, the other instance is up", bus.ServiceRecord{State: "standby", At: fresh}, true, "up", "up"},
		{"standby, waiting to take over", bus.ServiceRecord{State: "standby", At: fresh}, true, "down", "standby"},
		{"standby in the overlay", bus.ServiceRecord{State: "standby", At: fresh}, true, "", "standby"},
		{"standby gone stale", bus.ServiceRecord{State: "standby", At: old}, true, "down", "down"},
		{"failed but another instance runs", bus.ServiceRecord{State: "failed", At: old}, true, "up", "up"},
	}
	for _, c := range cases {
		if got := serviceFact(c.rec, c.has, c.legacy, now); got != c.want {
			t.Errorf("%s: serviceFact = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestServiceSegWords(t *testing.T) {
	for state, want := range map[string]string{
		"up": "dispatch up", "down": "dispatch down", "ready": "dispatch ready", "waiting-herdr": "dispatch waiting herdr", "waiting-git": "dispatch waiting git",
		"failed": "dispatch failed", "gave-up": "dispatch gave up", "stopped": "dispatch stopped", "starting": "dispatch starting",
	} {
		if got := lineText(serviceSeg("dispatch", "disp", state, false)); !strings.HasSuffix(got, want) {
			t.Errorf("%s: %q, want suffix %q", state, got, want)
		}
	}
	if got := lineText(serviceSeg("missions", "miss", "idle", true)); !strings.HasSuffix(got, "miss") {
		t.Errorf("compact = %q", got)
	}
	if r := serviceSeg("dispatch", "disp", "failed", false)[0].Role; r != RoleError {
		t.Errorf("failed role = %v", r)
	}
	if r := serviceSeg("dispatch", "disp", "waiting-herdr", false)[0].Role; r != RoleAttention {
		t.Errorf("waiting role = %v", r)
	}
	if r := serviceSeg("missions", "miss", "waiting-git", false)[0].Role; r != RoleAttention {
		t.Errorf("waiting git role = %v", r)
	}
}

// The cards: each service's state, pid and since, summary, last error, log
// size; Redis's place, version, uptime, memory and keys; a remote service
// says where it runs; unknown facts read '?'.
func TestServicesCards(t *testing.T) {
	now := time.Now()
	inst := &bus.ServicesInstall{Kinds: []string{"dispatch", "missions"}, Manager: "systemd", Host: "here", At: now.Add(-9 * time.Hour).UnixMilli()}
	recs := map[string]bus.ServiceRecord{
		"dispatch": {State: "failed", Detail: "holds the dispatch lease", Error: "broker went away", At: now.Add(-5 * time.Second).UnixMilli(), PID: 7, Host: "here", Log: "/x/dispatch.log"},
		"missions": {State: "idle", Detail: "waiting for missions", At: now.UnixMilli(), PID: 8, Host: "elsewhere", Log: "/far/missions.log"},
	}
	f := svcFacts{inst: inst, recs: recs, host: "here", logs: map[string]int64{"dispatch": 331 * 1024}, since: map[string]time.Time{"dispatch": now.Add(-2 * time.Hour)},
		redis: redisFacts{where: "docker agent-bus-redis (redis:8-alpine), running", info: bus.RedisInfo{Version: "8.2.1", UptimeSecs: 3 * 3600, UsedMemory: "2.1M", Keys: 1234}}}
	for _, width := range []int{100, 160} {
		body := plainOf(servicesCards(f, 0, now, width))
		for _, want := range []string{"systemd services on here", "dispatch", "failed", "pid 7", "since", "holds the dispatch lease", "broker went away",
			"log 331 KB of 1 MB, 3 kept", "missions", "idle", "on elsewhere", "log ? of 1 MB", "redis", "docker agent-bus-redis (redis:8-alpine), running",
			"version 8.2.1", "up 3h", "memory 2.1M", "1234 keys", "enter or l"} {
			if !strings.Contains(body, want) {
				t.Errorf("width %d: cards lack %q:\n%s", width, want, body)
			}
		}
		for _, l := range servicesCards(f, 0, now, width) {
			if l.Width() > width {
				t.Errorf("width %d: a line is %d cells", width, l.Width())
			}
		}
	}
	unknown := plainOf(servicesCards(svcFacts{recs: recs, host: "here", redis: redisFacts{infoErr: "connection refused"}}, 0, now, 100))
	for _, want := range []string{"redis  " + GlyphBlocked + " ?", "version ? ", "memory ? ", "connection refused"} {
		if !strings.Contains(unknown, want) {
			t.Errorf("unknown Redis lacks %q:\n%s", want, unknown)
		}
	}
	if none := plainOf(servicesCards(svcFacts{}, 0, now, 100)); !strings.Contains(none, "herdr tabs") {
		t.Errorf("no services: %q", none)
	}
}

// A service's log page: the local log's tail and crash output; a remote one
// is named, never read.
func TestServiceLogBody(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "dispatch.log")
	os.WriteFile(log, []byte("old line\nservice: failed: broker went away\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "dispatch.crash.log"), []byte("panic: boom\n"), 0o600)
	body := plainOf(serviceLogBody(bus.ServiceRecord{Log: log, Host: "here"}, "dispatch", "here", 100))
	for _, want := range []string{log, "service: failed: broker went away", "crash output", "panic: boom"} {
		if !strings.Contains(body, want) {
			t.Errorf("log page lacks %q:\n%s", want, body)
		}
	}
	far := plainOf(serviceLogBody(bus.ServiceRecord{Log: "/far/missions.log", Host: "elsewhere"}, "missions", "here", 100))
	if !strings.Contains(far, "the log is on elsewhere") {
		t.Errorf("remote log: %q", far)
	}
}

type fakeRedisInfo struct {
	*fakeReader
	info bus.RedisInfo
	err  error
}

func (f *fakeRedisInfo) RedisInfo(context.Context) (bus.RedisInfo, error) { return f.info, f.err }

// gatherServices: the broker's INFO through the reader, docker only through
// the runner given (nil = no docker call), the bare process otherwise; a
// remote service's log and process are not looked at.
func TestGatherServices(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "dispatch.log")
	os.WriteFile(log, []byte("12345"), 0o600)
	recs := map[string]bus.ServiceRecord{
		"dispatch": {Host: "here", Log: log, PID: os.Getpid()},
		"missions": {Host: "elsewhere", Log: log, PID: os.Getpid()},
	}
	r := &fakeRedisInfo{fakeReader: &fakeReader{calls: map[string]int{}}, info: bus.RedisInfo{Version: "8.2.1", PID: 812, Executable: "/usr/bin/redis-server"}}
	var asked []string
	docker := func(_ context.Context, name string, args ...string) (string, error) {
		asked = append(asked, name+" "+strings.Join(args, " "))
		return "redis:8-alpine|running", nil
	}
	f := gatherServices(context.Background(), r, "p", nil, recs, "here", docker)
	if f.logs["dispatch"] != 5 || !f.since["dispatch"].IsZero() { // the test process is no running service: no since
		t.Errorf("local facts %v %v", f.logs, f.since)
	}
	if _, ok := f.logs["missions"]; ok {
		t.Error("a remote service's log was read")
	}
	if f.redis.where != "docker agent-bus-redis (redis:8-alpine), running" || len(asked) != 1 || !strings.Contains(asked[0], "inspect") {
		t.Errorf("docker: %q %v", f.redis.where, asked)
	}
	bare := gatherServices(context.Background(), r, "p", nil, recs, "here", nil)
	if bare.redis.where != "process 812 (/usr/bin/redis-server)" {
		t.Errorf("bare: %q", bare.redis.where)
	}
	failing := func(context.Context, string, ...string) (string, error) { return "", fmt.Errorf("no such container") }
	if g := gatherServices(context.Background(), r, "p", nil, recs, "here", failing); g.redis.where != "process 812 (/usr/bin/redis-server)" {
		t.Errorf("docker failing: %q", g.redis.where)
	}
	if g := gatherServices(context.Background(), &fakeReader{calls: map[string]int{}}, "p", nil, recs, "here", nil); g.redis.infoErr == "" {
		t.Error("a reader without RedisInfo reads as known")
	}
}

// The tick turns the service records into the header facts, and S opens the
// Services overlay from the model without reading the bus.
func TestPollShowsServiceStatesAndTheOverlayOpens(t *testing.T) {
	now := time.Now().UnixMilli()
	f := &fakeReader{calls: map[string]int{}, conductor: true,
		svcInst: &bus.ServicesInstall{Kinds: []string{"dispatch", "missions"}, Manager: "systemd", Host: "h"},
		svcRecs: map[string]bus.ServiceRecord{
			"dispatch": {State: "waiting-herdr", At: now, Error: "herdr: no server", Host: "h"},
			"missions": {State: "idle", Detail: "waiting for missions", At: now, Host: "h"},
		}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	if st.dispatch != "waiting-herdr" || st.missions != "idle" {
		t.Fatalf("dispatch %q missions %q", st.dispatch, st.missions)
	}
	m := shellOf(t, st, 160, 45, nil)
	top := strings.SplitN(m.grid().plain(), "\n", 2)[0]
	if !strings.Contains(top, "dispatch waiting herdr") || !strings.Contains(top, "missions idle") {
		t.Fatalf("header = %q", top)
	}
	m, cmd := pressCmd(t, m, 's') // the logs are read in a command, then the page opens
	m = run(t, m, cmd)
	if screen := m.grid().plain(); !strings.Contains(screen, "Services") || !strings.Contains(screen, "herdr: no server") {
		t.Fatalf("services page not shown:\n%s", screen)
	}
}

// --delete stops and removes the project's services before it deletes the
// keys; a removal that fails deletes nothing.
func TestDeleteRemovesTheServicesFirst(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	project := "del" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, _ := bus.Open(c, project)
	ctx := context.Background()
	b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}})
	t.Cleanup(func() { c.Del(ctx, bus.ServicesKey(project)) })

	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), func(string) ([]string, error) {
		return nil, errors.New("systemctl refused")
	}); err == nil || !strings.Contains(err.Error(), "nothing deleted") {
		t.Fatalf("err = %v", err)
	}
	if ok, _ := b.ServicesInstalled(ctx); !ok {
		t.Fatal("keys deleted although the services were not removed")
	}
	var keysAtRemoval int
	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), func(p string) ([]string, error) {
		keys, _ := bus.ProjectKeys(ctx, c, p)
		keysAtRemoval = len(keys)
		return []string{"dispatch"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if keysAtRemoval == 0 {
		t.Fatal("the services were removed after the keys")
	}
	if ok, _ := b.ServicesInstalled(ctx); ok {
		t.Fatal("the project survived --delete")
	}
}

// An agent launched and bound but with no status yet has TS 0: no 1970 age.
func TestPollKeepsAnAgentWithoutAStatusUndated(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{"coder": {Pane: "w2:p3", PID: 7}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	a := st.agents["coder"]
	if a == nil || !a.stateAt.IsZero() {
		t.Fatalf("agent = %+v, want an undated entry", a)
	}
}

// Facts never read are unknown: Redis without a reading and a record without
// a pid read '?', never 0.
func TestServicesCardsNeverInventZero(t *testing.T) {
	body := plainOf(servicesCards(svcFacts{recs: map[string]bus.ServiceRecord{"dispatch": {State: "ready", Host: "here"}}, host: "here"}, 0, time.Now(), 100))
	for _, bad := range []string{"pid 0", "0 keys", "up 0s", GlyphWorking + " up"} {
		if strings.Contains(body, bad) {
			t.Errorf("%q in:\n%s", bad, body)
		}
	}
	for _, want := range []string{"pid ?", "? keys", "redis  " + GlyphBlocked + " ?"} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
}

// F3 opens the cards; the arrows choose a service card; enter (or l) opens
// that service's log as a page of its own; esc returns to the cards.
func TestShellServicesCardsOpenTheLog(t *testing.T) {
	dir := t.TempDir()
	host, _ := os.Hostname()
	logD, logM := filepath.Join(dir, "dispatch.log"), filepath.Join(dir, "missions.log")
	os.WriteFile(logD, []byte("dispatch line\n"), 0o600)
	os.WriteFile(logM, []byte("missions line\n"), 0o600)
	m := fixtureShell(t, 140, 40)
	m.snap.svcInst = &bus.ServicesInstall{Manager: "systemd", Host: host}
	m.snap.svcRecs = map[string]bus.ServiceRecord{"dispatch": {State: "ready", Host: host, Log: logD}, "missions": {State: "idle", Host: host, Log: logM}}
	next, cmd := m.Update(keyMsg(tea.Key{Code: tea.KeyF3}))
	m = run(t, next.(Model), cmd)
	if !m.rd.open() || m.rd.top().kind != ovServices {
		t.Fatal("F3 opened no Services cards")
	}
	m = keys(t, m, kDown)
	if m.rd.top().sel != 1 {
		t.Fatalf("down: card %d, want missions", m.rd.top().sel)
	}
	next, cmd = m.Update(keyMsg(kEnter))
	m = run(t, next.(Model), cmd)
	if m.rd.top().kind != ovServiceLog || !strings.Contains(plainOf(m.rd.top().body), "missions line") {
		t.Fatalf("enter opened %v: %s", m.rd.top().kind, plainOf(m.rd.top().body))
	}
	if m = keys(t, m, kEsc); m.rd.top().kind != ovServices {
		t.Error("esc did not return to the cards")
	}
	next, cmd = m.Update(keyMsg('l'))
	if m = run(t, next.(Model), cmd); m.rd.top().kind != ovServiceLog {
		t.Error("l opened no log")
	}
}

// since is read only for a running service whose pid's command line is that
// service: a stopped record's pid (maybe reused) or another process reads ?.
func TestServiceStartOnlyForItsOwnRunningProcess(t *testing.T) {
	proc := t.TempDir()
	os.WriteFile(filepath.Join(proc, "stat"), []byte("cpu 1 2 3\nbtime 1000000\n"), 0o600)
	put := func(pid int, cmd string) {
		d := filepath.Join(proc, fmt.Sprint(pid))
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "cmdline"), []byte(strings.ReplaceAll(cmd, " ", "\x00")), 0o600)
		// field 22 (starttime) = 500 ticks = 5 s after boot; the comm holds a space
		os.WriteFile(filepath.Join(d, "stat"), []byte(fmt.Sprintf("%d (agent bus) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 500 19 20\n", pid)), 0o600)
	}
	put(10, "agentbus --project p service run dispatch")
	put(11, "vim notes.txt")
	put(12, "agentbus --project other service run dispatch") // another project's same-kind service
	got, ok := serviceStart("p", "dispatch", bus.ServiceRecord{State: "ready", PID: 10}, proc)
	if !ok || !got.Equal(time.Unix(1000005, 0)) {
		t.Errorf("running dispatch: %v %v, want btime + 5 s", got, ok)
	}
	for name, rec := range map[string]bus.ServiceRecord{
		"stopped":       {State: "stopped", PID: 10},
		"failed":        {State: "failed", PID: 10},
		"reused pid":    {State: "ready", PID: 11},
		"other service": {State: "ready", PID: 10},
		"gone":          {State: "ready", PID: 99},
		"other project": {State: "ready", PID: 12},
	} {
		kind := "dispatch"
		if name == "other service" {
			kind = "missions"
		}
		if _, ok := serviceStart("p", kind, rec, proc); ok {
			t.Errorf("%s: a start was read", name)
		}
	}
}

// busmon-f3-keys: side by side (wide), the cards are a row, so ←/→ move
// between them as ↑/↓ do; stacked (narrow), they are a column and only ↑/↓
// move, ←/→ keeping their reader meaning. The footer and the help come from
// the key table.
func TestShellServicesCardsLeftRight(t *testing.T) {
	open := func(w, h int) Model {
		m := fixtureShell(t, w, h)
		next, cmd := m.Update(keyMsg(tea.Key{Code: tea.KeyF3}))
		m = run(t, next.(Model), cmd)
		if !m.rd.open() || m.rd.top().kind != ovServices {
			t.Fatal("F3 opened no Services cards")
		}
		return m
	}
	m := open(140, 40)
	if m = keys(t, m, kRight); m.rd.top().sel != 1 {
		t.Fatalf("wide right: card %d, want missions", m.rd.top().sel)
	}
	if m = keys(t, m, kRight); m.rd.top().sel != 2 { // the Redis card is selectable too (busmon-redis-detail)
		t.Fatalf("wide right: card %d, want redis", m.rd.top().sel)
	}
	if m = keys(t, m, kRight); m.rd.top().sel != 2 {
		t.Fatalf("wide right at the last card: %d", m.rd.top().sel)
	}
	if m = keys(t, m, kLeft); m.rd.top().sel != 1 {
		t.Fatalf("wide left from redis: card %d, want missions", m.rd.top().sel)
	}
	if m = keys(t, m, kLeft); m.rd.top().sel != 0 || m.rd.top().kind != ovServices {
		t.Fatalf("wide left: card %d, page %v", m.rd.top().sel, m.rd.top().kind)
	}
	if m = keys(t, m, kLeft); m.rd.top().sel != 0 || m.rd.top().kind != ovServices {
		t.Fatalf("wide left at the first card: card %d, page %v", m.rd.top().sel, m.rd.top().kind)
	}
	if m = keys(t, m, kDown); m.rd.top().sel != 1 {
		t.Fatalf("wide down kept: card %d", m.rd.top().sel)
	}
	n := open(90, 40)
	if n = keys(t, n, kRight); n.rd.top().kind == ovServices && n.rd.top().sel != 0 {
		t.Fatalf("stacked right moved the card to %d", n.rd.top().sel)
	}
	n = open(90, 40)
	if n = keys(t, n, kDown); n.rd.top().sel != 1 {
		t.Fatalf("stacked down: card %d", n.rd.top().sel)
	}
	help := plainOf(helpText())
	if !strings.Contains(help, GlyphArrowL+GlyphArrowR+" too when side by side") {
		t.Fatalf("the help lacks the F3 card keys:\n%s", help)
	}
}
