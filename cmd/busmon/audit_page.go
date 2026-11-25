package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The Audit list (a on History; audit spec docs/superpowers/specs/2026-11-24-audit-stream-design.md
// §4): the project's last auditListMax audit entries, newest first, read in a
// command when the page opens. Read-only: f shows one action, then the
// next, then all; enter opens the selected entry in place, its ref first.

// auditListMax is what the page reads (§4: the last 200 entries).
const auditListMax = 200

// auditReader reads the audit stream (*bus.Bus).
type auditReader interface {
	AuditLast(ctx context.Context, count int64) ([]bus.AuditEntry, error)
}

// auditView is the page's read and what the person chose on it.
type auditView struct {
	entries []bus.AuditEntry // newest first
	loaded  bool
	err     string          // the read failed, or no broker
	action  string          // "" every action, else the one shown
	open    map[string]bool // entries opened in place, by id
}

// auditItem is a selectable row: its body line and its entry's id.
type auditItem struct {
	line int
	id   string
}

// auditMsg is a load's result for the page of that serial.
type auditMsg struct {
	serial  int
	entries []bus.AuditEntry
	err     string
}

// auditWide is the width from which a row carries its detail.
const auditWide = 100

// auditBody lays the page out: its title, its body and its selectable rows.
func auditBody(v auditView, sel int, loc *time.Location, width int) (string, []Line, []auditItem) {
	shown := auditShown(v)
	title := fmt.Sprintf("Audit %s %d entries", GlyphNone, len(shown))
	if v.action != "" {
		title += " " + GlyphNone + " action: " + v.action
	}
	var body []Line
	muted := func(s string) { body = append(body, Line{S(RoleMuted, "  "+s)}) }
	if v.err != "" {
		muted("the audit stream could not be read: " + v.err)
	}
	if !v.loaded {
		muted("loading" + GlyphEllipsis)
		return title, body, nil
	}
	if len(shown) == 0 && v.err == "" {
		muted("no audit entry")
	}
	actorW, targetW := 16, 24
	if width < auditWide {
		targetW = 0
	}
	var items []auditItem
	for _, e := range shown {
		at := time.UnixMilli(e.At).In(loc)
		actor := e.Actor
		if e.Human == "yes" {
			actor += " (person)"
		}
		row := append(planMark(len(items) == sel), S(RoleMuted, at.Format("01-02 15:04")+"  "),
			S(RoleAccent, Fit(e.Action, 11)+"  "), S(RoleText, Fit(Preview(actor), actorW)+"  "),
			S(auditOutcomeRole(e.Outcome), Fit(e.Outcome, 7)+"  "))
		rest := e.Target
		if targetW > 0 {
			row = append(row, S(RoleText, Fit(Preview(e.Target), targetW)+"  "))
			rest = e.Detail
		}
		row = append(row, S(RoleMuted, Fit(Preview(rest), max(0, width-row.Width()))))
		items = append(items, auditItem{line: len(body), id: e.ID})
		body = append(body, row)
		if v.open[e.ID] {
			body = append(body, auditOpened(e, width)...)
		}
	}
	if len(v.entries) >= auditListMax {
		muted(fmt.Sprintf("the last %d entries: agentbus audit --since <d> reads further back", auditListMax))
	}
	return title, body, items
}

// auditOpened is an entry opened in place: its ref first (§4), then the rest
// of its fields.
func auditOpened(e bus.AuditEntry, width int) []Line {
	var out []Line
	field := func(k, v string) {
		if v == "" {
			v = "none"
		}
		out = append(out, Line{S(RoleMuted, "      "+Fit(k, 8)), S(RoleText, Fit(Preview(v), max(0, width-14)))})
	}
	field("ref", e.Ref)
	field("target", e.Target)
	field("detail", e.Detail)
	if e.Human == "yes" {
		how := "a person's entry"
		if e.Typed == "yes" {
			how += ", typed"
		}
		field("by", how)
	}
	field("id", e.ID)
	return out
}

// auditOutcomeRole colours an outcome: ok plain, refused and failed seen.
func auditOutcomeRole(o string) Role {
	switch o {
	case bus.AuditRefused:
		return RoleAttention
	case bus.AuditFailed:
		return RoleError
	}
	return RoleOK
}

// auditShown are the entries the action filter passes.
func auditShown(v auditView) []bus.AuditEntry {
	if v.action == "" {
		return v.entries
	}
	var out []bus.AuditEntry
	for _, e := range v.entries {
		if e.Action == v.action {
			out = append(out, e)
		}
	}
	return out
}

// auditNextAction is f's step: the actions present, in the spec's order,
// then every action again.
func auditNextAction(v auditView) string {
	var present []string
	for _, a := range bus.AuditActions {
		if slices.ContainsFunc(v.entries, func(e bus.AuditEntry) bool { return e.Action == a }) {
			present = append(present, a)
		}
	}
	i := slices.Index(present, v.action)
	if i+1 >= len(present) { // the last, or "" with nothing present
		if v.action == "" && len(present) > 0 {
			return present[0]
		}
		return ""
	}
	return present[i+1]
}

func (m *Model) openAudit() tea.Cmd {
	m.push(page{kind: ovAudit, audit: &auditView{}})
	return m.auditLoadCmd(m.rd.top().serial)
}

// auditLoadCmd reads the last auditListMax entries.
func (m *Model) auditLoadCmd(serial int) tea.Cmd {
	r, _ := innerWriter(m.cfg.w).(auditReader)
	ctx := m.cfg.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if r == nil {
			return auditMsg{serial: serial, err: "no broker"}
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		es, err := r.AuditLast(c, auditListMax)
		if err != nil {
			return auditMsg{serial: serial, err: err.Error()}
		}
		return auditMsg{serial: serial, entries: es}
	}
}

// gotAudit puts a load in its page, wherever it is on the stack.
func (m *Model) gotAudit(msg auditMsg) {
	i := slices.IndexFunc(m.rd.pages, func(p page) bool { return p.serial == msg.serial })
	if i < 0 || m.rd.pages[i].kind != ovAudit {
		return
	}
	p := &m.rd.pages[i]
	p.audit = &auditView{entries: msg.entries, loaded: true, err: msg.err}
	p.dirty = true
	m.refresh()
}

// buildAudit is the page's render.
func (m *Model) buildAudit(p *page, width int) {
	v := auditView{}
	if p.audit != nil {
		v = *p.audit
	}
	_, _, items := auditBody(v, -1, time.Local, width)
	p.sel = clamp(p.sel, 0, max(0, len(items)-1))
	p.title, p.body, p.aitems = auditBody(v, p.sel, time.Local, width)
}

// auditKey is the page's keys: f filters by action, enter opens the
// selected entry in place, the moves move the selection.
func (m *Model) auditKey(p *page, act Action, k Key) (tea.Cmd, bool) {
	switch {
	case act == ActFilter && p.audit != nil:
		v := *p.audit
		v.action = auditNextAction(v)
		p.audit, p.sel, p.dirty = &v, 0, true
		m.refresh()
		return nil, true
	case k.Code == keyEnter:
		m.toggleAuditRow(p)
		return nil, true
	case act == ActUp || act == ActDown || act == ActPageUp || act == ActPageDown || act == ActFirst || act == ActLast:
		m.moveSel(p, act, m.readerRows(), auditLines(p))
		return nil, true
	}
	return nil, false
}

// toggleAuditRow opens the selected entry in place, or closes it.
func (m *Model) toggleAuditRow(p *page) {
	if p.audit == nil || p.sel < 0 || p.sel >= len(p.aitems) {
		return
	}
	v := *p.audit
	open := map[string]bool{}
	for k, o := range v.open {
		open[k] = o
	}
	id := p.aitems[p.sel].id
	open[id] = !open[id]
	v.open = open
	p.audit, p.dirty = &v, true
	m.refresh()
}

func auditLines(p *page) func() []int {
	return func() []int {
		lines := make([]int, len(p.aitems))
		for i, it := range p.aitems {
			lines[i] = it.line
		}
		return lines
	}
}

// auditClick is a click on the page's row at line: a first click selects
// it, a second opens it in place.
func (m *Model) auditClick(p *page, line int) {
	for i, it := range p.aitems {
		if it.line == line {
			if i == p.sel {
				m.toggleAuditRow(p)
				return
			}
			p.sel, p.dirty = i, true
			m.refresh()
		}
	}
}
