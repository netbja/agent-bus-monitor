package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// The Projects page (M, spec docs/superpowers/specs/2026-11-08-projects-admin-page-design.md):
// every project this host knows of, on the broker, from a template or both,
// read once when the page opens and on R, in a command, never polled.
// Step 2 is read-only: the actions come in steps 3 and 4.

// adminReadTimeout bounds the page's read (§3.3); a test lowers it.
var adminReadTimeout = 5 * time.Second

// adminFacts is one broker project as the page reads it.
type adminFacts struct {
	sum       bus.ProjectSummary
	keys      int
	streams   map[string]int64 // bus.DetailStreams -> XLEN
	pause     *bus.PauseRecord
	live      int // agents heard from within bus.DormantAfter, on any host
	dispatch  bool
	conductor bool
}

// adminWorkspace is a project's herdr workspace on this machine.
type adminWorkspace struct {
	id    string
	panes int
}

// adminSource is what the page reads; a nil part is unknown (?), never none.
type adminSource struct {
	broker    func(ctx context.Context) ([]adminFacts, error)
	templates func() ([]projlist.Row, error)
	services  func(ctx context.Context) (map[string]map[string]bool, error) // project -> kind -> active, this machine
	herdr     func(ctx context.Context) (map[string]adminWorkspace, error)  // project -> its workspace, this machine
	host      string                                                        // this machine's name
	// quiet is §5.1's live check ("" = quiet), remove the delete (§5.3).
	quiet  func(ctx context.Context, project string) (string, error)
	remove func(ctx context.Context, project string) deleteResult
	// procAgents: this host can tell a verified agent's process alive
	// (/proc); nil = it can. Without it the delete says so (master: §5.1's
	// limit stated, never refused).
	procAgents func() bool
	// memory is what one project's keys take (§3.3, the detail only); nil:
	// not shown.
	memory func(ctx context.Context, project string) (bus.MemoryUsage, error)
}

// Memory read bounds (§3.3): one call per key, so a deadline and a cap.
// memoryDeadline is a variable for its test only.
var memoryDeadline = 2 * time.Second

const memoryMaxKeys = 50000

// adminRow is one row of the page.
type adminRow struct {
	name         string
	open         bool // busmon's own project
	onBroker     bool
	brokerKnown  bool   // the broker was read: a row off it is a template only
	state        string // live, paused, suspended, stopped, template, ?
	live, total  int
	keys         int
	streams      map[string]int64
	pause        *bus.PauseRecord
	dispatch     bool
	conductor    bool
	svc          map[string]bool // kind -> active, this machine
	svcKnown     bool
	ws           adminWorkspace
	herdrKnown   bool
	lastTS       int64
	template     bool
	templatePath string
}

// adminView is the page's read: its rows and what failed.
type adminView struct {
	rows                                 []adminRow
	agentsUnchecked                      bool // no /proc here: the delete cannot see this host's agents
	readAt                               time.Time
	brokerErr, svcErr, herdrErr, tmplErr string
	host                                 string
}

// gatherAdmin reads every part within the page's deadline; each part fails
// alone (§3.3).
func gatherAdmin(ctx context.Context, src adminSource, open string, now time.Time) adminView {
	c, cancel := context.WithTimeout(ctx, adminReadTimeout)
	defer cancel()
	v := adminView{readAt: now, host: src.host, agentsUnchecked: src.procAgents != nil && !src.procAgents()}
	var facts []adminFacts
	brokerOK := false
	if src.broker == nil {
		v.brokerErr = "not read"
	} else if f, err := src.broker(c); err != nil {
		v.brokerErr = err.Error()
	} else {
		facts, brokerOK = f, true
	}
	var tmpls []projlist.Row
	if src.templates != nil {
		t, err := src.templates()
		if err != nil {
			v.tmplErr = err.Error()
		}
		tmpls = t
	}
	var svc map[string]map[string]bool
	svcOK := false
	if src.services == nil {
		v.svcErr = "no service manager"
	} else if s, err := src.services(c); err != nil {
		v.svcErr = err.Error()
	} else {
		svc, svcOK = s, true
	}
	var ws map[string]adminWorkspace
	herdrOK := false
	if src.herdr == nil {
		v.herdrErr = "not read"
	} else if w, err := src.herdr(c); err != nil {
		v.herdrErr = err.Error()
	} else {
		ws, herdrOK = w, true
	}
	v.rows = adminRows(facts, brokerOK, tmpls, svc, svcOK, ws, herdrOK, open)
	return v
}

// adminRows is the union of the broker's projects and the templates, the
// open project always among them (§3.1), each named only when valid.
func adminRows(facts []adminFacts, brokerOK bool, tmpls []projlist.Row, svc map[string]map[string]bool, svcOK bool,
	ws map[string]adminWorkspace, herdrOK bool, open string) []adminRow {
	byName := map[string]*adminRow{}
	row := func(name string) *adminRow {
		if r := byName[name]; r != nil {
			return r
		}
		r := &adminRow{name: name, open: name == open, svcKnown: svcOK, herdrKnown: herdrOK, brokerKnown: brokerOK}
		byName[name] = r
		return r
	}
	for _, f := range facts {
		if !bus.ValidName(f.sum.Project) {
			continue
		}
		r := row(f.sum.Project)
		r.onBroker, r.total, r.live, r.keys, r.streams = true, f.sum.Agents, f.live, f.keys, f.streams
		r.pause, r.dispatch, r.conductor, r.lastTS = f.pause, f.dispatch, f.conductor, f.sum.LastTS
	}
	for _, t := range tmpls {
		if !bus.ValidName(t.Name) {
			continue
		}
		r := row(t.Name)
		r.template, r.templatePath = true, t.Path
	}
	if bus.ValidName(open) {
		row(open)
	}
	out := make([]adminRow, 0, len(byName))
	for _, r := range byName {
		r.svc = svc[r.name]
		r.ws = ws[r.name]
		r.state = adminState(*r, brokerOK)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.open != b.open {
			return a.open
		}
		if a.onBroker != b.onBroker {
			return a.onBroker
		}
		if a.lastTS != b.lastTS {
			return a.lastTS > b.lastTS
		}
		return a.name < b.name
	})
	return out
}

// adminState is the row's state from its evidence (§3.2).
func adminState(r adminRow, brokerOK bool) string {
	switch {
	case !brokerOK:
		return "?"
	case !r.onBroker:
		return "template"
	case r.pause != nil && r.pause.Suspended():
		return "suspended"
	case r.pause != nil:
		return "paused"
	}
	active := r.dispatch || r.conductor || r.live > 0
	for _, on := range r.svc {
		active = active || on
	}
	switch {
	case active:
		return "live"
	case !r.svcKnown: // stopped means no active service either, unknown here (reviewer-codex)
		return "?"
	}
	return "stopped"
}

// --- the real sources

// realAdminSource reads through client and this machine's service manager
// and herdr.
func realAdminSource(client *redis.Client, getenv func(string) string) adminSource {
	host, _ := os.Hostname()
	src := adminSource{host: host,
		broker:    func(ctx context.Context) ([]adminFacts, error) { return readAdminBroker(ctx, client) },
		templates: func() ([]projlist.Row, error) { return projlist.Templates(rolecfg.ProjectsDir(getenv)) },
		herdr: func(ctx context.Context) (map[string]adminWorkspace, error) {
			return readAdminHerdr(ctx, herdrRunner(getenv))
		},
	}
	src.quiet, src.remove = realQuiet(client, getenv), realRemove(client)
	src.memory = func(ctx context.Context, p string) (bus.MemoryUsage, error) {
		ctx, cancel := context.WithTimeout(ctx, memoryDeadline)
		defer cancel()
		return bus.ProjectMemory(ctx, client, p, memoryMaxKeys)
	}
	src.procAgents = hasProc
	// The manager is found and asked inside the page's read, never at
	// busmon's start, and within its deadline (reviewer-codex).
	src.services = func(ctx context.Context) (map[string]map[string]bool, error) {
		return withDeadline(ctx, func() (map[string]map[string]bool, error) {
			m, _ := service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, getenv)), getenv)
			if m == nil {
				return nil, errNoManager
			}
			return readAdminServices(m)
		})
	}
	return src
}

// errNoManager: this machine has no service manager agentbus uses.
var errNoManager = errors.New("no service manager")

// withDeadline runs read, whose commands take no context, and gives up at
// ctx's end: the page never waits past its deadline (its goroutine ends
// when the command does).
func withDeadline[T any](ctx context.Context, read func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := read()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func readAdminBroker(ctx context.Context, client *redis.Client) ([]adminFacts, error) {
	projects, err := bus.Projects(ctx, client)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out []adminFacts
	for _, p := range projects {
		b, err := bus.Open(client, p.Project)
		if err != nil {
			continue
		}
		f := adminFacts{sum: p}
		fp, err := bus.ProjectFootprint(ctx, client, p.Project)
		if err != nil {
			return nil, err
		}
		f.keys, f.streams = fp.Keys, fp.Streams
		if f.pause, err = b.Pause(ctx); err != nil {
			return nil, err
		}
		snaps, err := b.Agents(ctx)
		if err != nil {
			return nil, err
		}
		f.live = liveAgents(snaps, now)
		if f.dispatch, err = b.DispatchAlive(ctx); err != nil {
			return nil, err
		}
		if f.conductor, err = b.ConductorAlive(ctx); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// liveAgents counts the agents heard from within bus.DormantAfter, on any
// host: the Agents panel's rule, read from the broker.
func liveAgents(snaps map[string]bus.AgentSnapshot, now time.Time) int {
	n := 0
	for _, s := range snaps {
		if s.TS > 0 && now.Sub(time.UnixMilli(s.TS)) <= bus.DormantAfter {
			n++
		}
	}
	return n
}

func readAdminServices(m service.Manager) (map[string]map[string]bool, error) {
	units, err := m.List()
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, u := range units {
		st, err := m.State(u.Project, u.Kind)
		if err != nil {
			return nil, err
		}
		if out[u.Project] == nil {
			out[u.Project] = map[string]bool{}
		}
		out[u.Project][u.Kind] = st.Active
	}
	return out, nil
}

// herdrRunner runs this machine's herdr CLI: HERDR_BIN_PATH, else HERDR_BIN,
// else herdr on PATH (agentbus's herdrBin).
func herdrRunner(getenv func(string) string) func(ctx context.Context, args ...string) ([]byte, error) {
	bin := getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = getenv("HERDR_BIN")
	}
	if bin == "" {
		bin = "herdr"
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, bin, args...).Output()
	}
}

// readAdminHerdr maps each workspace whose label is a valid project name to
// its id and pane count.
func readAdminHerdr(ctx context.Context, run func(ctx context.Context, args ...string) ([]byte, error)) (map[string]adminWorkspace, error) {
	out, err := run(ctx, "workspace", "list")
	if err != nil {
		return nil, fmt.Errorf("herdr workspace list: %w", err)
	}
	var v struct {
		Result struct {
			Workspaces []struct {
				Label       string `json:"label"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("herdr workspace list: %w", err)
	}
	ws := map[string]adminWorkspace{}
	for _, w := range v.Result.Workspaces {
		if !bus.ValidName(w.Label) || w.WorkspaceID == "" {
			continue
		}
		out, err := run(ctx, "pane", "list", "--workspace", w.WorkspaceID)
		if err != nil {
			return nil, fmt.Errorf("herdr pane list: %w", err)
		}
		var p struct {
			Result struct {
				Panes []json.RawMessage `json:"panes"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out, &p); err != nil {
			return nil, fmt.Errorf("herdr pane list: %w", err)
		}
		ws[w.Label] = adminWorkspace{id: w.WorkspaceID, panes: len(p.Result.Panes)}
	}
	return ws, nil
}

// --- drawing

// adminServicesCell is "dispatch ● missions ○", – with none installed here,
// ? when unknown.
func adminServicesCell(r adminRow) Line {
	if !r.svcKnown {
		return Line{{Text: "?", Role: RoleMuted}}
	}
	if len(r.svc) == 0 {
		return Line{{Text: GlyphDash, Role: RoleMuted}}
	}
	var l Line
	for _, k := range service.Kinds {
		on, ok := r.svc[k]
		if !ok {
			continue
		}
		if len(l) > 0 {
			l = append(l, Seg{Text: " ", Role: RoleText})
		}
		g, role := GlyphIdle, RoleMuted
		if on {
			g, role = GlyphWorking, RoleOK
		}
		l = append(l, Seg{Text: k + " ", Role: RoleMuted}, Seg{Text: g, Role: role})
	}
	return l
}

func adminHerdrCell(r adminRow) string {
	switch {
	case !r.herdrKnown:
		return "?"
	case r.ws.id == "":
		return GlyphDash
	}
	return fmt.Sprintf("open (%d %s)", r.ws.panes, plural(r.ws.panes, "pane"))
}

func adminStateRole(s string) Role {
	switch s {
	case "live":
		return RoleOK
	case "paused", "suspended":
		return RoleAttention
	}
	return RoleMuted
}

// brokerCell is ? when the broker was not read, — for a template only.
func brokerCell(r adminRow) (string, bool) {
	if !r.brokerKnown {
		return "?", false
	}
	if !r.onBroker {
		return GlyphDash, false
	}
	return "", true
}

func adminAgents(r adminRow) string {
	if c, ok := brokerCell(r); !ok {
		return c
	}
	return fmt.Sprintf("%d/%d", r.live, r.total)
}

func adminKeys(r adminRow) string {
	if c, ok := brokerCell(r); !ok {
		return c
	}
	return strconv.Itoa(r.keys)
}

func adminLast(r adminRow, now time.Time) string {
	if c, ok := brokerCell(r); !ok {
		return c
	}
	return lastSeen(r.lastTS, now)
}

func adminName(r adminRow) Line {
	l := Line{{Text: r.name, Role: RoleText, Bold: r.open}}
	if r.open {
		l = append(l, Seg{Text: " " + GlyphOpenHere + " open", Role: RoleAccent})
	}
	return l
}

// adminCol is one column: its header and its cell.
type adminCol struct {
	head string
	w    int
	cell func(adminRow) Line
}

func adminCols(tier Tier, now time.Time, running string) []adminCol {
	text := func(f func(adminRow) string) func(adminRow) Line {
		return func(r adminRow) Line { return Line{{Text: f(r), Role: RoleMuted}} }
	}
	project := adminCol{"PROJECT", 0, adminName} // the width left (projectsBody)
	state := adminCol{"STATE", 11, func(r adminRow) Line { return adminStateCell(r, running) }}
	agents := adminCol{"AGENTS", 7, text(adminAgents)}
	keys := adminCol{"KEYS", 7, text(adminKeys)}
	services := adminCol{"SERVICES", 22, adminServicesCell}
	herdr := adminCol{"HERDR", 16, text(adminHerdrCell)}
	last := adminCol{"LAST", 8, text(func(r adminRow) string { return adminLast(r, now) })}
	tmpl := adminCol{"TEMPLATE", 8, text(func(r adminRow) string {
		if r.template {
			return GlyphDone
		}
		return ""
	})}
	if tier == TierWide {
		return []adminCol{project, state, agents, keys, services, herdr, last, tmpl}
	}
	return []adminCol{project, state, agents, services, last}
}

// projectsTitle: "Projects · 5 · read 12s ago".
func projectsTitle(v adminView, now time.Time) string {
	read := "read just now"
	if d := now.Sub(v.readAt); d >= time.Second {
		read = "read " + humanAge(d) + " ago"
	}
	return fmt.Sprintf("Projects %s %d %s %s", GlyphNone, len(v.rows), GlyphNone, read)
}

// projectsBody is the page: its notes, then a row per project (two on a
// narrow page), the selected one marked; lines is each row's first line.
func projectsBody(v adminView, sel int, tier Tier, width int, now time.Time, running string) ([]Line, []int) {
	var body []Line
	note := func(s string) { body = append(body, FitLine(Line{{Text: s, Role: RoleAttention}}, width)) }
	if v.brokerErr != "" {
		note("broker: " + v.brokerErr + " (its columns read ?)")
	}
	if v.svcErr != "" {
		note("services: " + v.svcErr)
	}
	if v.herdrErr != "" {
		note("herdr: " + v.herdrErr)
	}
	if v.tmplErr != "" {
		note("templates: " + v.tmplErr)
	}
	// Always: busmon cannot tell a tunnel (localhost:6380 to another host's
	// broker) from a local broker, and services and herdr are this machine's
	// (foureyes).
	body = append(body, FitLine(Line{{Text: "services and herdr: this machine (" + or(v.host, "?") + "); " + GlyphDash + " means not here", Role: RoleMuted}}, width), nil)
	lines := make([]int, 0, len(v.rows))
	mark := func(i int) Seg {
		if i == sel {
			return Seg{Text: GlyphSelect + " ", Role: RoleAccent}
		}
		return Seg{Text: "  ", Role: RoleText}
	}
	if tier == TierNarrow {
		for i, r := range v.rows {
			lines = append(lines, len(body))
			first := append(append(Line{mark(i)}, adminName(r)...), Seg{Text: "  ", Role: RoleText})
			first = append(first, adminStateCell(r, running)...)
			second := Line{{Text: "  " + adminAgents(r) + " agents " + GlyphNone + " " + adminLast(r, now) + " " + GlyphNone + " ", Role: RoleMuted}}
			second = append(second, adminServicesCell(r)...)
			if r.brokerKnown && !r.onBroker {
				second = Line{{Text: "  a template only: no key on the broker", Role: RoleMuted}}
			}
			body = append(body, FitLine(first, width), FitLine(second, width))
		}
		return body, lines
	}
	cols := adminCols(tier, now, running)
	rest := 2 // the selection mark
	for _, c := range cols[1:] {
		rest += c.w + 1
	}
	cols[0].w = clamp(width-rest-1, 12, 34) // a long name is cut, never the columns after it
	pad := func(l Line, w int) Line {       // w cells and one blank: a cut cell never touches the next
		l = FitLine(l, w)
		if n := w + 1 - l.Width(); n > 0 {
			l = append(l, Seg{Text: strings.Repeat(" ", n), Role: RoleText})
		}
		return l
	}
	head := Line{{Text: "  ", Role: RoleText}}
	for _, c := range cols {
		head = append(head, pad(Line{{Text: c.head, Role: RoleMuted, Bold: true}}, c.w)...)
	}
	body = append(body, FitLine(head, width))
	for i, r := range v.rows {
		lines = append(lines, len(body))
		l := Line{mark(i)}
		for i, c := range cols {
			cell := c.cell(r)
			if i == 0 && r.open && cell.Width() > c.w { // the mark alone when "open" does not fit
				cell = Line{{Text: r.name, Role: RoleText, Bold: true}, {Text: " " + GlyphOpenHere, Role: RoleAccent}}
			}
			l = append(l, pad(cell, c.w)...)
		}
		body = append(body, FitLine(l, width))
	}
	return body, lines
}

// adminDetail is a project's every column, its streams, its pause or
// suspend record, its template and workspace (§4 enter).
// memoryText is the detail's memory value (§3.3): reading, the estimate
// ("about": each key is sampled), the forms of a cut read, "?" with why when
// it is unknown.
func memoryText(done bool, u bus.MemoryUsage, err error) string {
	size := "about " + humanBytes(u.Bytes)
	switch {
	case !done:
		return "reading" + GlyphEllipsis
	case err != nil:
		return "? (" + Preview(err.Error()) + ")"
	case u.Refused:
		return "? (the broker refuses MEMORY)"
	case u.ScanCut == "cap":
		return fmt.Sprintf("%s for the first %d keys; the scan stopped (%d keys)", size, u.Read, memoryMaxKeys)
	case u.ScanCut != "":
		return fmt.Sprintf("%s for the first %d keys; the scan stopped (%s)", size, u.Read, memoryDeadline)
	case u.Read < u.Keys:
		return fmt.Sprintf("%s for %d of %d keys", size, u.Read, u.Keys)
	}
	return fmt.Sprintf("%s (%d %s)", size, u.Keys, plural(u.Keys, "key"))
}

// adminDetail is the detail page; mem is its memory value, "" for none.
func adminDetail(r adminRow, v adminView, now time.Time, mem string) []Line {
	var out []Line
	kv := func(k, val string) {
		out = append(out, Line{{Text: fmt.Sprintf("%-12s", k), Role: RoleMuted}, {Text: val, Role: RoleText}})
	}
	kv("state", r.state)
	if r.onBroker {
		kv("agents", fmt.Sprintf("%d live of %d recorded (heard from within %s)", r.live, r.total, humanAge(bus.DormantAfter)))
		kv("keys", strconv.Itoa(r.keys))
		if mem != "" {
			kv("memory", mem)
		}
		var s []string
		for _, k := range bus.DetailStreams() {
			if n, ok := r.streams[k]; ok {
				s = append(s, fmt.Sprintf("%s %d", k, n))
			}
		}
		kv("streams", or(strings.Join(s, " "+GlyphNone+" "), GlyphDash))
		kv("last", adminLast(r, now))
		leases := []string{}
		if r.dispatch {
			leases = append(leases, "dispatch")
		}
		if r.conductor {
			leases = append(leases, "conductor")
		}
		kv("leases", or(strings.Join(leases, ", "), "none held"))
	} else if r.brokerKnown {
		kv("broker", "no key: a template only")
	} else {
		kv("broker", "? (not read)")
	}
	if p := r.pause; p != nil {
		what := "paused"
		if p.Suspended() {
			what = "suspended"
		}
		line := what + " by " + p.By
		if p.Reason != "" {
			line += ": " + p.Reason
		}
		if p.At > 0 {
			line += " (" + humanAge(now.Sub(time.UnixMilli(p.At))) + " ago)"
		}
		if p.Host != "" {
			line += " on " + p.Host
		}
		kv("pause", line)
	}
	kv("services", adminServicesCell(r).text()+" (this machine, "+or(v.host, "?")+")")
	herdr := adminHerdrCell(r)
	if r.ws.id != "" {
		herdr += ", workspace " + r.ws.id
	}
	kv("herdr", herdr)
	tmpl := GlyphDash
	if r.template {
		tmpl = r.templatePath
	}
	kv("template", tmpl)
	return out
}

// --- the model

// projectMemoryMsg carries one project's memory read for its detail page.
type projectMemoryMsg struct {
	serial int
	usage  bus.MemoryUsage
	err    error
}

// memoryCmd reads a project's memory for the detail page serial (§3.3).
func (m *Model) memoryCmd(serial int, project string) tea.Cmd {
	read, ctx := m.cfg.admin.memory, m.cfg.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		u, err := read(ctx, project)
		return projectMemoryMsg{serial: serial, usage: u, err: err}
	}
}

// gotMemory fills the detail page it was read for; a page closed meanwhile
// takes nothing.
func (m *Model) gotMemory(msg projectMemoryMsg) {
	for i := range m.rd.pages {
		p := &m.rd.pages[i]
		if p.serial == msg.serial && p.kind == ovProjectDetail && p.proj != nil {
			p.body = adminDetail(p.projRow, *p.proj, m.now, memoryText(true, msg.usage, msg.err))
			m.refresh()
			return
		}
	}
}

// projectsMsg carries the page's read; serial 0 opens the page.
type projectsMsg struct {
	view   adminView
	serial int
}

// projectsCmd reads the page in a command.
func (m *Model) projectsCmd(serial int) tea.Cmd {
	src, ctx, open := m.cfg.admin, m.cfg.ctx, m.cfg.project
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		var s adminSource
		if src != nil {
			s = *src
		}
		return projectsMsg{view: gatherAdmin(ctx, s, open, time.Now()), serial: serial}
	}
}

// openProjects pushes the page at once, then reads it (foureyes): a second
// M meets the open page, and a slow read never lands over another page.
func (m *Model) openProjects() tea.Cmd {
	cmd := m.push(page{kind: ovProjects, title: "Projects"})
	return tea.Batch(cmd, m.projectsCmd(m.rd.top().serial))
}

// gotProjects fills the page it was read for, in place: the selection stays
// on the same project when it is still listed. A page closed meanwhile takes
// nothing.
func (m *Model) gotProjects(msg projectsMsg) tea.Cmd {
	v := msg.view
	for i := range m.rd.pages {
		p := &m.rd.pages[i]
		if p.serial != msg.serial || p.kind != ovProjects {
			continue
		}
		name := ""
		if p.proj != nil && p.sel < len(p.proj.rows) {
			name = p.proj.rows[p.sel].name
		}
		p.proj, p.dirty = &v, true
		for j, r := range v.rows {
			if r.name == name {
				p.sel = j
			}
		}
		p.sel = clamp(p.sel, 0, max(0, len(v.rows)-1))
	}
	m.refresh()
	return nil
}

// projectsKey is a key on the page (ModeProjects).
func (m *Model) projectsKey(act Action, _ Key) tea.Cmd {
	p := m.rd.top()
	if p.proj == nil {
		if act == ActClose {
			m.pop()
		}
		return nil
	}
	switch act {
	case ActClose:
		m.pop()
	case ActUp, ActDown, ActPageUp, ActPageDown, ActFirst, ActLast:
		m.moveSel(p, act, m.readerRows(), func() []int { return p.plines })
	case ActOpen:
		if p.sel < len(p.proj.rows) {
			r, v := p.proj.rows[p.sel], p.proj
			src := m.cfg.admin
			if !r.onBroker || src == nil || src.memory == nil {
				return m.push(page{kind: ovProjectDetail, title: "Project " + r.name, body: adminDetail(r, *v, m.now, ""), fixed: true})
			}
			cmd := m.push(page{kind: ovProjectDetail, title: "Project " + r.name, body: adminDetail(r, *v, m.now, memoryText(false, bus.MemoryUsage{}, nil)),
				fixed: true, proj: v, projRow: r})
			return tea.Batch(cmd, m.memoryCmd(m.rd.top().serial, r.name))
		}
	case ActRecap: // R: read again
		return m.projectsCmd(p.serial)
	case ActDelete:
		if p.sel < len(p.proj.rows) {
			return m.projectDelete(p.proj.rows[p.sel])
		}
	case ActSleep, ActStopServices, ActBackupNow:
		if p.sel < len(p.proj.rows) {
			return m.projectAction(act, p.proj.rows[p.sel])
		}
	case ActHelp:
		return m.push(page{kind: ovHelp})
	}
	return nil
}

// adminSourceFor is the page's sources for busmon's broker.
func adminSourceFor(client *redis.Client, getenv func(string) string) *adminSource {
	src := realAdminSource(client, getenv)
	return &src
}

// projectsPageOpen is the Projects page when it is on the stack.
func (m *Model) projectsPageOpen() *page {
	for i := range m.rd.pages {
		if m.rd.pages[i].kind == ovProjects {
			return &m.rd.pages[i]
		}
	}
	return nil
}

// projectAction is Z, x or b on a row (§4): through the shell's one run at a
// time (startRun), Z and x after a y, b at once.
func (m *Model) projectAction(act Action, r adminRow) tea.Cmd {
	if run := m.ui.sleepRun; run != nil {
		m.ui.flash, m.ui.flashErr = run.verb+" already running (see "+run.log+")", true
		return nil
	}
	switch act {
	case ActSleep:
		if r.open { // the open project's row is the shell's Z
			return m.sleepKey()
		}
		if !r.brokerKnown { // foureyes: unknown is not none
			m.ui.flash, m.ui.flashErr = "the broker was not read: "+r.name+"'s state is unknown (R reads again)", true
			return nil
		}
		if !r.onBroker {
			m.ui.flash, m.ui.flashErr = r.name+" is a template only: nothing runs to put to sleep", true
			return nil
		}
		return m.sleepKeyFor(r.name, r.pause != nil && r.pause.Suspended())
	case ActStopServices:
		active := false
		for _, on := range r.svc {
			active = active || on
		}
		if !r.svcKnown {
			m.ui.flash, m.ui.flashErr = "the services of "+r.name+" here are unknown (R reads again)", true
			return nil
		}
		if !active {
			m.ui.flash, m.ui.flashErr = "no active service of "+r.name+" on this machine: nothing to stop", true
			return nil
		}
		m.ui.sleepAsk = &sleepAsk{project: r.name, verb: "down", args: []string{"down", r.name}}
		m.ui.flash, m.ui.flashErr = "Stop the services of "+r.name+" here? agentbus down: nothing is deleted. y / n", true
		return nil
	case ActBackupNow:
		return m.startRunFor(r.name, "backup", []string{"backup", "--project", r.name})
	}
	return nil
}

// adminStateCell is the row's state, with ⋯ while a run goes on it (§4).
func adminStateCell(r adminRow, running string) Line {
	l := Line{{Text: r.state, Role: adminStateRole(r.state)}}
	if running != "" && running == r.name {
		l = append(l, Seg{Text: " " + GlyphEllipsis, Role: RoleAccent})
	}
	return l
}

// runningProject is the project the current run acts on: a Projects page
// run's, or the open project's for the shell's Z; "" for none, or for the
// C page's backup of every project.
func (m *Model) runningProject() string {
	r := m.ui.sleepRun
	switch {
	case r == nil:
		return ""
	case r.project != "":
		return r.project
	case r.verb == "suspend" || r.verb == "resume":
		return m.cfg.project
	}
	return ""
}

// markProjectsDirty rebuilds the Projects page: a run started or ended on a
// row (its ⋯ mark), or its project became known.
func (m *Model) markProjectsDirty() {
	if p := m.projectsPageOpen(); p != nil {
		p.dirty = true
	}
}
