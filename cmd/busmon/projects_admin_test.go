package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/service"
)

// adminFixture is the page's sources: the open project live, a suspended
// one, a stopped one with an old activity, a template-only one, and a
// template that is also on the broker.
func adminFixture(now time.Time) adminSource {
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	return adminSource{
		broker: func(context.Context) ([]adminFacts, error) {
			return []adminFacts{
				{sum: bus.ProjectSummary{Project: "agent-bus-monitor", Agents: 7, LastTS: ms(time.Minute)}, keys: 1204,
					streams: map[string]int64{"status": 900, "report": 40, "notify": 10, "cmd": 200, "verdicts": 30}, live: 3, dispatch: true},
				{sum: bus.ProjectSummary{Project: "demo", Agents: 4, LastTS: ms(3 * 24 * time.Hour)}, keys: 310,
					streams: map[string]int64{"status": 100}, pause: &bus.PauseRecord{Kind: bus.PauseKindSuspend, By: "charlie", Reason: "holiday", At: ms(3 * 24 * time.Hour)}},
				{sum: bus.ProjectSummary{Project: "old", Agents: 2, LastTS: ms(40 * 24 * time.Hour)}, keys: 12, streams: map[string]int64{}},
				{sum: bus.ProjectSummary{Project: "trading", Agents: 5, LastTS: ms(2 * time.Hour)}, keys: 800, streams: map[string]int64{},
					pause: &bus.PauseRecord{By: "charlie", Until: now.Add(time.Hour).UnixMilli(), At: ms(10 * time.Minute)}, live: 1},
			}, nil
		},
		templates: func() ([]projlist.Row, error) {
			return []projlist.Row{{Name: "demo", Path: "/home/c/.config/agentbus/projects/demo.toml"},
				{Name: "newone", Path: "/home/c/.config/agentbus/projects/newone.toml"}}, nil
		},
		services: func(context.Context) (map[string]map[string]bool, error) {
			return map[string]map[string]bool{"agent-bus-monitor": {"dispatch": true, "missions": false}, "old": {"dispatch": false}}, nil
		},
		herdr: func(context.Context) (map[string]adminWorkspace, error) {
			return map[string]adminWorkspace{"agent-bus-monitor": {id: "w1", panes: 6}}, nil
		},
		host: "vdr",
	}
}

func rowNamed(t *testing.T, v adminView, name string) adminRow {
	t.Helper()
	for _, r := range v.rows {
		if r.name == name {
			return r
		}
	}
	t.Fatalf("no row %s in %+v", name, v.rows)
	return adminRow{}
}

// §3.1-3.2: one row per name of the union, each state from its evidence, the
// open project first, then the newest activity, then templates by name.
func TestAdminRows(t *testing.T) {
	now := shellNow
	v := gatherAdmin(context.Background(), adminFixture(now), "agent-bus-monitor", now)
	var names []string
	for _, r := range v.rows {
		names = append(names, r.name)
	}
	if got := strings.Join(names, " "); got != "agent-bus-monitor trading demo old newone" {
		t.Fatalf("order %q", got)
	}
	for name, want := range map[string]string{"agent-bus-monitor": "live", "trading": "paused", "demo": "suspended", "old": "stopped", "newone": "template"} {
		if r := rowNamed(t, v, name); r.state != want {
			t.Errorf("%s: state %q, want %q", name, r.state, want)
		}
	}
	abm := rowNamed(t, v, "agent-bus-monitor")
	if !abm.open || abm.live != 3 || abm.total != 7 || abm.keys != 1204 || !abm.svcKnown || !abm.svc["dispatch"] || abm.ws.panes != 6 || !abm.herdrKnown || abm.template {
		t.Errorf("open row %+v", abm)
	}
	if d := rowNamed(t, v, "demo"); !d.template || d.templatePath == "" || !d.onBroker || len(d.svc) != 0 || !d.svcKnown {
		t.Errorf("demo %+v", d)
	}
	if n := rowNamed(t, v, "newone"); n.onBroker || !n.template {
		t.Errorf("newone %+v", n)
	}
}

// §3.3: each part fails alone; an unknown is ?, never empty or none.
func TestAdminRowsUnknowns(t *testing.T) {
	now := shellNow
	src := adminFixture(now)
	src.broker = func(context.Context) ([]adminFacts, error) { return nil, errors.New("connection refused") }
	src.services, src.herdr = nil, func(context.Context) (map[string]adminWorkspace, error) { return nil, errors.New("no server") }
	v := gatherAdmin(context.Background(), src, "agent-bus-monitor", now)
	if v.brokerErr == "" || v.herdrErr == "" || v.svcErr == "" {
		t.Fatalf("errors %+v", v)
	}
	for _, r := range v.rows {
		if r.state != "?" || r.svcKnown || r.herdrKnown {
			t.Errorf("%s: %+v", r.name, r)
		}
	}
	if len(v.rows) != 3 { // the two templates, and the open project busmon runs in
		t.Fatalf("rows %+v", v.rows)
	}
	pinLocal(t)
	body, _ := projectsBody(v, 0, TierWide, 130, now, "")
	text := tokLinesText(body)
	for _, l := range body { // the cells: the "this machine" note names the dash itself
		if lt := l.text(); strings.Contains(lt, GlyphDash) && !strings.Contains(lt, "means not here") {
			t.Errorf("an unknown read as none: %q", lt)
		}
	}
	for _, want := range []string{"broker: connection refused", "herdr: no server", "services: no service manager"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q:\n%s", want, text)
		}
	}
}

// adminShell is the fixture shell with the page's sources.
func adminShell(t *testing.T, w, h int) Model {
	t.Helper()
	src := adminFixture(shellNow)
	return shellWith(t, w, h, shellConfig{admin: &src}, false)
}

// §7: M opens the page (read in a command); its own table moves, enter opens
// the detail, R reads again, esc goes back to the panels.
func TestProjectsPageFlow(t *testing.T) {
	pinLocal(t)
	m := adminShell(t, 140, 40)
	m, cmd := pressCmd(t, m, 'M')
	m = run(t, m, cmd)
	p := m.rd.top()
	if p == nil || p.kind != ovProjects || p.proj == nil || m.mode() != ModeProjects {
		t.Fatalf("M opened %+v", p)
	}
	screen := m.grid().plain()
	for _, want := range []string{"Projects", "agent-bus-monitor", GlyphOpenHere + " open", "suspended", "template", "3/7", "1204", "open (6 panes)", "read just now"} {
		if !strings.Contains(screen, want) {
			t.Errorf("no %q:\n%s", want, screen)
		}
	}
	m = keys(t, m, 'j')
	if p := m.rd.top(); p.sel != 1 {
		t.Fatalf("j: sel %d", p.sel)
	}
	m, cmd = pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	m = run(t, m, cmd)
	d := m.rd.top()
	if d.kind != ovProjectDetail || !strings.Contains(d.title, "trading") {
		t.Fatalf("enter opened %+v", d)
	}
	if text := tokLinesText(d.body); !strings.Contains(text, "paused by charlie") {
		t.Errorf("detail:\n%s", text)
	}
	m = keys(t, m, tea.Key{Code: tea.KeyEscape})
	if m.rd.top().kind != ovProjects {
		t.Fatal("esc did not return to the list")
	}
	reads := 0
	src := adminFixture(shellNow)
	inner := src.broker
	src.broker = func(ctx context.Context) ([]adminFacts, error) { // a newer project now lists above trading
		reads++
		f, err := inner(ctx)
		return append(f, adminFacts{sum: bus.ProjectSummary{Project: "brandnew", LastTS: shellNow.UnixMilli()}}), err
	}
	m.cfg.admin = &src
	m, cmd = pressCmd(t, m, 'R')
	if m = run(t, m, cmd); reads != 1 || m.rd.top().sel != 2 || m.rd.top().proj.rows[2].name != "trading" {
		t.Fatalf("R: reads %d sel %d", reads, m.rd.top().sel)
	}
	m = keys(t, m, tea.Key{Code: tea.KeyEscape})
	if m.rd.open() || m.mode() != ModeShell {
		t.Fatal("esc did not leave the page")
	}
}

// §6: the columns of each tier.
func TestProjectsTiers(t *testing.T) {
	pinLocal(t)
	v := gatherAdmin(context.Background(), adminFixture(shellNow), "agent-bus-monitor", shellNow)
	wide, _ := projectsBody(v, 0, TierWide, 130, shellNow, "")
	medium, _ := projectsBody(v, 0, TierMedium, 100, shellNow, "")
	narrow, lines := projectsBody(v, 0, TierNarrow, 60, shellNow, "")
	for _, c := range []struct {
		name      string
		body      []Line
		has, hasn []string
	}{
		{"wide", wide, []string{"KEYS", "HERDR", "TEMPLATE", "1204", "open (6 panes)"}, nil},
		{"medium", medium, []string{"STATE", "AGENTS", "SERVICES", "LAST"}, []string{"KEYS", "HERDR", "1204"}},
		{"narrow", narrow, []string{"3/7 agents"}, []string{"KEYS", "STATE"}},
	} {
		text := tokLinesText(c.body)
		for _, w := range c.has {
			if !strings.Contains(text, w) {
				t.Errorf("%s: no %q:\n%s", c.name, w, text)
			}
		}
		for _, w := range c.hasn {
			if strings.Contains(text, w) {
				t.Errorf("%s: %q shown:\n%s", c.name, w, text)
			}
		}
		for _, l := range c.body {
			if w := map[string]int{"wide": 130, "medium": 100, "narrow": 60}[c.name]; l.Width() > w {
				t.Errorf("%s: a line of %d cells: %q", c.name, l.Width(), l.text())
			}
		}
	}
	if len(lines) != 5 || lines[1]-lines[0] != 2 { // narrow: two lines a row
		t.Errorf("narrow row lines %v", lines)
	}
}

// fakeManager is a service manager that only lists and reports.
type fakeManager struct {
	units  []service.Unit
	active map[string]bool // project/kind
	err    error
}

func (f fakeManager) Name() string                       { return "fake" }
func (f fakeManager) Install(service.Spec) (bool, error) { return false, nil }
func (f fakeManager) Start(string, string) error         { return nil }
func (f fakeManager) Stop(string, string) error          { return nil }
func (f fakeManager) Remove(string, string) error        { return nil }
func (f fakeManager) List() ([]service.Unit, error)      { return f.units, f.err }
func (f fakeManager) State(p, k string) (service.State, error) {
	return service.State{Installed: true, Active: f.active[p+"/"+k]}, nil
}

// The real readers: this machine's units by project and kind; herdr's
// workspaces whose label is a project, with their panes.
func TestAdminLocalReaders(t *testing.T) {
	got, err := readAdminServices(fakeManager{units: []service.Unit{{Project: "demo", Kind: "dispatch"}, {Project: "demo", Kind: "missions"}},
		active: map[string]bool{"demo/dispatch": true}})
	if err != nil || !got["demo"]["dispatch"] || got["demo"]["missions"] || len(got["demo"]) != 2 {
		t.Fatalf("services %v %v", got, err)
	}
	if _, err := readAdminServices(fakeManager{err: errors.New("systemctl down")}); err == nil {
		t.Fatal("a failed list read as none")
	}
	run := func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "workspace list":
			return []byte(`{"result":{"workspaces":[{"label":"demo","workspace_id":"w7"},{"label":"Not A Project","workspace_id":"w8"}]}}`), nil
		case "pane list --workspace w7":
			return []byte(`{"result":{"panes":[{"pane_id":"p1"},{"pane_id":"p2"}]}}`), nil
		}
		return nil, errors.New("unexpected " + strings.Join(args, " "))
	}
	ws, err := readAdminHerdr(context.Background(), run)
	if err != nil || len(ws) != 1 || ws["demo"] != (adminWorkspace{id: "w7", panes: 2}) {
		t.Fatalf("herdr %v %v", ws, err)
	}
	if _, err := readAdminHerdr(context.Background(), func(context.Context, ...string) ([]byte, error) { return nil, errors.New("no server") }); err == nil {
		t.Fatal("herdr down read as no workspace")
	}
}

// The broker reader on a disposable Redis: keys, streams, the pause, live
// agents and the leases of each project.
func TestAdminBrokerReader(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "adm" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, _ := bus.Open(c, project)
	t.Cleanup(func() {
		keys, _ := bus.ProjectKeys(ctx, c, project)
		if len(keys) > 0 {
			c.Del(ctx, keys...)
		}
	})
	if _, err := b.Status(ctx, "coder", "working", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetPause(ctx, "charlie", "master", "lunch", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	facts, err := readAdminBroker(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if f.sum.Project != project {
			continue
		}
		if f.keys < 2 || f.streams["status"] != 1 || f.live != 1 || f.pause == nil || f.pause.By != "charlie" || f.dispatch || f.conductor {
			t.Fatalf("facts %+v", f)
		}
		return
	}
	t.Fatalf("no %s in %+v", project, facts)
}

// Mutation survivors: an invalid name never shows; the open project comes
// first even when older; a broker project with no dated activity still comes
// before a template; agents heard from alone make a project live; the
// liveness rule is bus.DormantAfter.
func TestAdminRowsEdges(t *testing.T) {
	now := shellNow
	facts := []adminFacts{
		{sum: bus.ProjectSummary{Project: "Bad*", LastTS: now.UnixMilli()}},
		{sum: bus.ProjectSummary{Project: "fresh", LastTS: now.UnixMilli()}},
		{sum: bus.ProjectSummary{Project: "mine", LastTS: now.Add(-time.Hour).UnixMilli()}},
		{sum: bus.ProjectSummary{Project: "zzz"}, live: 2},
		{sum: bus.ProjectSummary{Project: "svc"}},
	}
	rows := adminRows(facts, true, []projlist.Row{{Name: "aaa"}}, map[string]map[string]bool{"svc": {"missions": true}}, true, nil, true, "mine")
	var names []string
	for _, r := range rows {
		names = append(names, r.name+":"+r.state)
	}
	if got := strings.Join(names, " "); got != "mine:stopped fresh:stopped svc:live zzz:live aaa:template" {
		t.Fatalf("rows %q", got)
	}
	snaps := map[string]bus.AgentSnapshot{
		"a": {TS: now.Add(-time.Minute).UnixMilli()},
		"b": {TS: now.Add(-bus.DormantAfter).UnixMilli()},
		"c": {TS: now.Add(-bus.DormantAfter - time.Second).UnixMilli()},
		"d": {},
	}
	if n := liveAgents(snaps, now); n != 2 {
		t.Fatalf("live %d, want 2", n)
	}
}

// Review fixes on step 2: an unknown service manager makes a row with no
// other evidence ?, never stopped (reviewer-codex); a conductor lease alone
// is live; the "this machine" note always shows, a tunnel looks local
// (foureyes); the read never waits past its deadline (reviewer-codex).
func TestAdminReviewFixes(t *testing.T) {
	now := shellNow
	facts := []adminFacts{
		{sum: bus.ProjectSummary{Project: "quiet", LastTS: now.UnixMilli()}},
		{sum: bus.ProjectSummary{Project: "cond"}, conductor: true},
	}
	rows := adminRows(facts, true, nil, nil, false, nil, true, "")
	for _, r := range rows {
		if want := map[string]string{"quiet": "?", "cond": "live"}[r.name]; r.state != want {
			t.Errorf("%s: %q, want %q", r.name, r.state, want)
		}
	}
	src := adminFixture(now)
	src.host = "laptop"
	pinLocal(t)
	body, _ := projectsBody(gatherAdmin(context.Background(), src, "agent-bus-monitor", now), 0, TierWide, 130, now, "")
	if !strings.Contains(tokLinesText(body), "services and herdr: this machine (laptop)") {
		t.Errorf("no note:\n%s", tokLinesText(body))
	}

	old := adminReadTimeout
	adminReadTimeout = 50 * time.Millisecond
	t.Cleanup(func() { adminReadTimeout = old })
	release := make(chan struct{})
	defer close(release)
	src.services = func(ctx context.Context) (map[string]map[string]bool, error) {
		return withDeadline(ctx, func() (map[string]map[string]bool, error) { <-release; return nil, nil })
	}
	start := time.Now()
	v := gatherAdmin(context.Background(), src, "agent-bus-monitor", now)
	if d := time.Since(start); d > time.Second || !strings.Contains(v.svcErr, "deadline") {
		t.Fatalf("read took %v, svcErr %q", d, v.svcErr)
	}
	if got, err := withDeadline(context.Background(), func() (int, error) { return 7, nil }); got != 7 || err != nil {
		t.Fatalf("withDeadline: %d %v", got, err)
	}
}

// foureyes P3: the page is on screen at once, filled when its read returns;
// a read for a page closed meanwhile lands nowhere.
func TestProjectsPageOpensFirst(t *testing.T) {
	m := adminShell(t, 140, 40)
	m, cmd := pressCmd(t, m, 'M')
	if p := m.rd.top(); p == nil || p.kind != ovProjects || p.proj != nil || !strings.Contains(m.grid().plain(), "reading every project") {
		t.Fatalf("M: %+v", p)
	}
	m = keys(t, m, tea.Key{Code: tea.KeyEscape})
	m = run(t, m, cmd)
	if m.rd.open() {
		t.Fatalf("a late read reopened the page: %+v", m.rd.top())
	}
}

// projects-admin §3.3 (the person, Q2): the memory a project's keys take is
// read for the detail only, on enter, in its own command: "reading…", then
// the size, a partial size when cut, "?" when refused or failed; the list
// never asks for it.
func TestProjectDetailMemory(t *testing.T) {
	pinLocal(t)
	var asked []string
	answer := bus.MemoryUsage{Bytes: 3 << 20, Read: 800, Keys: 800}
	var answerErr error
	src := adminFixture(shellNow)
	src.memory = func(_ context.Context, p string) (bus.MemoryUsage, error) {
		asked = append(asked, p)
		return answer, answerErr
	}
	m := adminShell(t, 140, 40)
	m.cfg.admin = &src
	m, cmd := pressCmd(t, m, 'M')
	m = run(t, m, cmd)
	if len(asked) != 0 {
		t.Fatalf("the list asked for memory: %v", asked)
	}
	m = keys(t, m, 'j')              // trading
	open := func() (Model, string) { // enter, read, then back to the list for the next case
		m2, cmd := pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
		m2 = run(t, m2, cmd)
		text := tokLinesText(m2.rd.top().body)
		m = keys(t, m2, tea.Key{Code: tea.KeyEscape})
		return m2, text
	}
	if got := memoryText(false, bus.MemoryUsage{}, nil); got != "reading"+GlyphEllipsis { // until the read answers
		t.Fatalf("before the read: %q", got)
	}
	_, text := open()
	if !strings.Contains(text, "memory      about 3 MB (800 keys)") || len(asked) != 1 || asked[0] != "trading" {
		t.Fatalf("read for %v:\n%s", asked, text)
	}
	answer = bus.MemoryUsage{Bytes: 2 << 20, Read: 799, Keys: 800} // one key short is still partial
	if _, text = open(); !strings.Contains(text, "memory      about 2 MB for 799 of 800 keys") {
		t.Fatalf("partial:\n%s", text)
	}
	answer = bus.MemoryUsage{Bytes: 2 << 20, Read: 900, Keys: 900, ScanCut: "deadline"}
	if _, text = open(); !strings.Contains(text, "memory      about 2 MB for the first 900 keys; the scan stopped (2s)") {
		t.Fatalf("scan cut by the clock:\n%s", text)
	}
	answer = bus.MemoryUsage{Bytes: 2 << 20, Read: 50000, Keys: 50000, ScanCut: "cap"}
	if _, text = open(); !strings.Contains(text, "memory      about 2 MB for the first 50000 keys; the scan stopped (50000 keys)") {
		t.Fatalf("scan cut by the cap:\n%s", text)
	}
	answer = bus.MemoryUsage{Keys: 800, Refused: true}
	if _, text = open(); !strings.Contains(text, "memory      ? (the broker refuses MEMORY)") {
		t.Fatalf("refused:\n%s", text)
	}
	answer, answerErr = bus.MemoryUsage{}, errors.New("i/o timeout")
	if _, text = open(); !strings.Contains(text, "memory      ? (i/o timeout)") {
		t.Fatalf("failed:\n%s", text)
	}
	// a template only: no broker keys, no memory line, nothing asked
	asked, answerErr = nil, nil
	for range 3 {
		m = keys(t, m, 'j')
	}
	m2, cmd := pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	m2 = run(t, m2, cmd)
	if d := m2.rd.top(); !strings.Contains(d.title, "newone") || strings.Contains(tokLinesText(d.body), "memory") || len(asked) != 0 {
		t.Fatalf("a template only: %q asked %v\n%s", d.title, asked, tokLinesText(d.body))
	}
}

// foureyes: a read answering late, for a detail since closed, never lands
// on another project's detail.
func TestProjectDetailMemoryLateAnswer(t *testing.T) {
	pinLocal(t)
	src := adminFixture(shellNow)
	src.memory = func(_ context.Context, p string) (bus.MemoryUsage, error) {
		return bus.MemoryUsage{Bytes: int64(len(p)) << 20, Read: 1, Keys: 1}, nil
	}
	m := adminShell(t, 140, 40)
	m.cfg.admin = &src
	m, cmd := pressCmd(t, m, 'M')
	m = run(t, m, cmd)
	m = keys(t, m, 'j') // trading
	m, cmdA := pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	late := cmdA // trading's read, not yet answered
	m = keys(t, m, tea.Key{Code: tea.KeyEscape}, 'k')
	m, cmdB := pressCmd(t, m, tea.Key{Code: tea.KeyEnter})
	if d := m.rd.top(); !strings.Contains(d.title, "agent-bus-monitor") {
		t.Fatalf("second detail: %q", d.title)
	}
	m = run(t, m, late) // trading's answer arrives on agent-bus-monitor's detail
	if text := tokLinesText(m.rd.top().body); strings.Contains(text, "about 7 MB") || !strings.Contains(text, "reading") {
		t.Fatalf("a late answer landed on another detail:\n%s", text)
	}
	m = run(t, m, cmdB)
	if text := tokLinesText(m.rd.top().body); !strings.Contains(text, "about 17 MB (1 key)") {
		t.Fatalf("its own answer:\n%s", text)
	}
}

// foureyes: the real reader has a deadline: with a tiny one, a read of many
// keys is cut and shown as such, never as complete or as an error.
func TestProjectMemoryRealDeadline(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	p := "dl" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() {
		keys, _ := bus.ProjectKeys(ctx, c, p)
		c.Del(ctx, keys...)
	})
	for i := range 3000 {
		c.Set(ctx, p+":k"+strconv.Itoa(i), "x", 0)
	}
	old := memoryDeadline
	memoryDeadline = time.Microsecond
	t.Cleanup(func() { memoryDeadline = old })
	src := realAdminSource(c, func(string) string { return "" })
	u, err := src.memory(ctx, p)
	if text := memoryText(true, u, err); err != nil || u.ScanCut == "" && u.Read >= u.Keys || !strings.Contains(text, "about") {
		t.Fatalf("a read past its deadline: %+v %v %q", u, err, text)
	}
}
