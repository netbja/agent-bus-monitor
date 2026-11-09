package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/service"
)

// The Projects page's delete (projects-admin §5): refused while the project
// is live, checked at d and again just before the delete; a backup offered
// first; the name typed; a second y for the open project, after which busmon
// quits. The delete itself is deleteProjectNow, busmon --delete's path.

// deleteTimeout bounds the delete's command (§5.3).
const deleteTimeout = 30 * time.Second

// deleteResult is what the delete did.
type deleteResult struct {
	project  string
	keys     int64
	services []string // removed on this machine
	left     []string // keys written after the script: never a clean delete
	err      error
}

// The panel's steps.
const (
	delBackup    = iota // a backup first? b, n, esc
	delBackingUp        // the backup runs
	delName             // the name typed
	delOpen             // the open project's second y
	delDeleting         // the delete runs
)

// deletePanel is the confirmation over the page.
type deletePanel struct {
	row       adminRow
	unchecked bool // no /proc here: this host's agents are not checked
	step      int
	backup    string // the archive, or "no backup"
	field     string
	note      string // does not match, a failed backup
}

type deleteCheckMsg struct {
	project, reason string
}

type deleteDoneMsg struct {
	res    deleteResult
	became string // the second check refused: nothing was deleted
	open   bool
}

// projectDelete is d on a row: the checks, read in a command (§5.1).
func (m *Model) projectDelete(r adminRow) tea.Cmd {
	if run := m.ui.sleepRun; run != nil {
		m.ui.flash, m.ui.flashErr = run.verb+" already running (see "+run.log+"): delete waits for it", true
		return nil
	}
	if !r.onBroker {
		m.ui.flash, m.ui.flashErr = r.name+" has no key on the broker: nothing to delete (its template stays)", true
		return nil
	}
	quiet := m.adminQuiet()
	ctx := m.cfgCtx()
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, adminReadTimeout)
		defer cancel()
		return deleteCheckMsg{project: r.name, reason: quiet(c, r.name)}
	}
}

// adminQuiet is the source's live check as one reason ("" = quiet); an
// unknown never counts as quiet.
func (m *Model) adminQuiet() func(ctx context.Context, p string) string {
	src := m.cfg.admin
	return func(ctx context.Context, p string) string {
		if src == nil || src.quiet == nil {
			return "cannot check that " + p + " is quiet (no reader)"
		}
		why, err := src.quiet(ctx, p)
		if err != nil {
			return "cannot check that " + p + " is quiet (" + err.Error() + ")"
		}
		return why
	}
}

func (m *Model) cfgCtx() context.Context {
	if m.cfg.ctx == nil {
		return context.Background()
	}
	return m.cfg.ctx
}

// gotDeleteCheck opens the panel on a quiet project, else says why not.
func (m *Model) gotDeleteCheck(msg deleteCheckMsg) {
	p := m.projectsPageOpen()
	if p == nil || p != m.rd.top() || p.proj == nil || p.sel >= len(p.proj.rows) || p.proj.rows[p.sel].name != msg.project {
		return // the person moved on: nothing opens
	}
	if msg.reason != "" {
		m.ui.flash, m.ui.flashErr = msg.reason, true
		return
	}
	m.ui.del = &deletePanel{row: p.proj.rows[p.sel], unchecked: p.proj.agentsUnchecked}
}

// deleteKey is a key on the panel: its own table; on the name step every
// printable key is the field's, so a d there is a letter.
func (m *Model) deleteKey(k Key) tea.Cmd {
	d := m.ui.del
	if k.Code == keyEscape { // esc cancels at every step but the delete itself
		if d.step != delDeleting {
			m.ui.del = nil
			m.ui.flash, m.ui.flashErr = "nothing deleted", false
		}
		return nil
	}
	switch d.step {
	case delBackup:
		switch {
		case k.Code == keyRune && k.Rune == 'b':
			d.note = ""
			cmd := m.startRunFor(d.row.name, "backup", []string{"backup", "--project", d.row.name})
			if cmd != nil {
				d.step = delBackingUp
			}
			return cmd
		case k.Code == keyRune && k.Rune == 'n':
			d.step, d.backup, d.note = delName, "no backup", ""
		}
	case delName:
		switch {
		case k.Code == keyEnter:
			if d.field != d.row.name { // byte for byte; it never builds a key
				d.note = "does not match"
				return nil
			}
			d.note = ""
			if d.row.open {
				d.step = delOpen
				return nil
			}
			return m.runDelete()
		case k.Code == keyBackspace:
			if r := []rune(d.field); len(r) > 0 {
				d.field = string(r[:len(r)-1])
			}
		case k.Code == keyRune:
			d.field += string(k.Rune)
		}
	case delOpen:
		if k.Code == keyRune && k.Rune == 'y' {
			return m.runDelete()
		}
	}
	return nil
}

// deleteBackupDone moves the panel on once its backup ends (gotSleepDone).
func (m *Model) deleteBackupDone(r *sleepRun, err error) {
	d := m.ui.del
	if d == nil || d.step != delBackingUp || r.project != d.row.name {
		return
	}
	if err != nil {
		d.step, d.note = delBackup, "backup failed: "+Preview(r.last)+" ("+r.log+"); b again, n without, esc cancels"
		return
	}
	d.step, d.backup = delName, r.last
}

// runDelete checks again, then deletes, in one command (§5.1, §5.3).
func (m *Model) runDelete() tea.Cmd {
	d := m.ui.del
	d.step = delDeleting
	name, open := d.row.name, d.row.open
	quiet, src, ctx := m.adminQuiet(), m.cfg.admin, m.cfgCtx()
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, deleteTimeout)
		defer cancel()
		if why := quiet(c, name); why != "" {
			return deleteDoneMsg{res: deleteResult{project: name}, became: why, open: open}
		}
		if src == nil || src.remove == nil {
			return deleteDoneMsg{res: deleteResult{project: name, err: errors.New("no broker to delete from")}, open: open}
		}
		return deleteDoneMsg{res: src.remove(c, name), open: open}
	}
}

// gotDeleteDone closes the panel and says what happened; after a clean
// delete of the open project, busmon quits with its message (§5.2.3).
func (m *Model) gotDeleteDone(msg deleteDoneMsg) tea.Cmd {
	m.ui.del = nil
	r := msg.res
	switch {
	case msg.became != "":
		m.notice.push(notice{text: "became live: " + msg.became + "; nothing deleted", level: noticeAttention})
	case r.err != nil: // deleteProjectNow's words: what was and was not deleted
		m.notice.push(notice{text: r.err.Error(), level: noticeAttention})
	case len(r.left) > 0:
		m.notice.push(notice{text: cameBack(r.project, r.keys, r.left), level: noticeAttention})
	default:
		text := fmt.Sprintf("deleted %d %s of %s", r.keys, plural(int(r.keys), "key"), r.project)
		if len(r.services) > 0 {
			text += fmt.Sprintf("; removed the %s %s here", strings.Join(r.services, " and "), plural(len(r.services), "service"))
		}
		if msg.open {
			m.exitNote = fmt.Sprintf("%s deleted (%d %s); busmon --pick to open another project", r.project, r.keys, plural(int(r.keys), "key"))
			return tea.Quit
		}
		m.notice.push(notice{text: text, level: noticeDone})
	}
	if p := m.projectsPageOpen(); p != nil {
		return m.projectsCmd(p.serial)
	}
	return nil
}

// deletePanelLines is the panel's body at width w.
func (m Model) deletePanelLines(w int) []Line {
	d := m.ui.del
	r := d.row
	muted := func(s string) Line { return Line{{Text: s, Role: RoleMuted}} }
	svc := "services: " + adminServicesCell(r).text() + " here"
	if r.svcKnown && len(r.svc) == 0 {
		svc = "services: none here"
	}
	herdr := "herdr: " + adminHerdrCell(r)
	lines := []Line{
		muted(fmt.Sprintf("%d %s %s %d agents (%d live) %s last activity %s %s %s %s %s",
			r.keys, plural(r.keys, "key"), GlyphNone, r.total, r.live, GlyphNone, adminLast(r, m.now), GlyphNone, svc, GlyphNone, herdr)),
	}
	if d.unchecked {
		lines = append(lines, Line{{Text: "agents: not checkable on this host (no /proc): only the leases, the services' records and the scan after the delete see them", Role: RoleAttention}})
	}
	lines = append(lines, []Line{
		{{Text: "This is a DEL, not a reset: streams, consumer groups, agents/usage/budget/verdicts, pilot and gate leases all go. The template and the worktrees stay.", Role: RoleText}},
		nil,
	}...)
	key := func(k, what string) Line {
		return Line{{Text: k, Role: RoleAccent, Bold: true}, {Text: " " + what, Role: RoleMuted}}
	}
	switch d.step {
	case delBackup:
		l := append(Line{{Text: "A backup first?  ", Role: RoleAttention}}, key("b", "back up, then continue")...)
		l = append(append(append(l, Seg{Text: " " + GlyphNone + " ", Role: RoleMuted}), key("n", "no backup")...), Seg{Text: " " + GlyphNone + " ", Role: RoleMuted})
		lines = append(lines, append(l, key("esc", "cancel")...))
	case delBackingUp:
		last := ""
		if run := m.ui.sleepRun; run != nil {
			last = run.last
		}
		lines = append(lines, muted("backing up"+GlyphEllipsis+" "+Preview(last)))
	case delName, delOpen:
		lines = append(lines, muted("backup: "+Preview(d.backup)),
			Line{{Text: "type " + r.name + " to delete:", Role: RoleAttention}},
			Line{{Text: d.field, Role: RoleText, Bold: true}, {Text: "▏", Role: RoleAccent}})
		if d.step == delOpen {
			lines = append(lines, append(Line{{Text: "busmon has " + r.name + " open: after the delete busmon exits. ", Role: RoleAttention}},
				append(append(key("y", "deletes"), Seg{Text: " " + GlyphNone + " ", Role: RoleMuted}), key("esc", "cancels")...)...))
		}
	case delDeleting:
		lines = append(lines, muted("deleting"+GlyphEllipsis))
	}
	if d.note != "" {
		lines = append(lines, Line{{Text: d.note, Role: RoleError}})
	}
	var out []Line
	for _, l := range lines {
		out = append(out, wrapLine(l, w)...)
	}
	return out
}

// wrapLine cuts l into lines of at most w cells, on spaces where it can.
func wrapLine(l Line, w int) []Line {
	if w <= 0 || l.Width() <= w {
		return []Line{l}
	}
	var out []Line
	var cur Line
	for _, s := range l {
		for _, word := range strings.SplitAfter(s.Text, " ") {
			if word == "" {
				continue
			}
			if cur.Width()+Width(word) > w && cur.Width() > 0 {
				out = append(out, cur)
				cur = nil
			}
			seg := s
			seg.Text = word
			cur = append(cur, seg)
		}
	}
	if cur != nil {
		out = append(out, cur)
	}
	for i := range out {
		out[i] = FitLine(out[i], w)
	}
	return out
}

// deleteRect is the panel's place: centred, its lines' height.
func (m *Model) deleteRect(lines int) Rect {
	w := min(96, m.width-2)
	h := min(m.height-m.layout.Header.H, lines+2)
	return Rect{(m.width - w) / 2, max(m.layout.Header.H, (m.height-h)/2), w, h}
}

func (m Model) drawDeletePanel(g *grid) {
	w := min(96, m.width-2)
	lines := m.deletePanelLines(w - 4)
	r := m.deleteRect(len(lines))
	if r.W < 20 || r.H < 3 {
		return
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		drawLine(g, r.X, y, r.W, nil, false)
	}
	drawFrame(g, r.X, r.Y, r.W, r.H, Frame{Title: Line{{Text: "Delete project '" + m.ui.del.row.name + "'", Role: RoleError}}, Focused: true, Hue: RoleError})
	for i, l := range lines {
		if i >= r.H-2 {
			break
		}
		drawLine(g, r.X+2, r.Y+1+i, r.W-4, l, false)
	}
}

// --- the real sources

// realQuiet is §5.1's checks, on any host where the broker can tell (the
// leases, the services' records) and on this machine for the rest: its
// services, its verified agents' processes, its herdr workspace. The rule
// agentbus restore uses (projectLive).
func realQuiet(client *redis.Client, getenv func(string) string) func(ctx context.Context, p string) (string, error) {
	return func(ctx context.Context, p string) (string, error) {
		var ref *bus.DeleteRefusal
		switch err := bus.ProjectQuiet(ctx, client, p); {
		case errors.As(err, &ref) && (ref.What == "dispatch" || ref.What == "conductor"):
			return "a " + ref.What + " holds " + p + "'s lease: agentbus down " + p + " where it runs, or Z", nil
		case errors.As(err, &ref):
			return ref.What + " runs on " + or(ref.Host, "another host") + ": agentbus down " + p + " there, or Z", nil
		case err != nil:
			return "", err
		}
		active, err := withDeadline(ctx, func() (bool, error) {
			return servicesActive(p, func() (service.Manager, error) { return localManager(getenv) })
		})
		if err != nil {
			return "", err
		}
		if active {
			return "stop its services first: x", nil
		}
		b, err := bus.Open(client, p)
		if err != nil {
			return "", err
		}
		snaps, err := b.Agents(ctx)
		if err != nil {
			return "", err
		}
		live := 0
		if hasProc() { // without /proc the limit is stated in the panel (master, §5.1)
			for _, s := range snaps {
				if s.PID == 0 || s.PIDStart == 0 {
					continue
				}
				start, err := dispatch.ProcStart(s.PID)
				switch {
				case err == nil && start == s.PIDStart:
					live++
				case err == nil, errors.Is(err, dispatch.ErrNoProcess): // gone, or another process now
				default: // unknown is never quiet (foureyes)
					return "", fmt.Errorf("agent pid %d: %w", s.PID, err)
				}
			}
		}
		if live > 0 {
			if live == 1 {
				return "1 agent is live here: Z puts the project to sleep", nil
			}
			return fmt.Sprintf("%d agents are live here: Z puts the project to sleep", live), nil
		}
		ws, err := readAdminHerdr(ctx, herdrRunner(getenv))
		if err != nil {
			return "", err
		}
		if w, ok := ws[p]; ok {
			return fmt.Sprintf("close its herdr workspace first (%d %s)", w.panes, plural(w.panes, "pane")), nil
		}
		return "", nil
	}
}

// servicesActive says whether a unit of p runs under detect's manager; a
// manager that cannot be checked is an error, so the delete is refused
// (reviewer-codex); only an explicit none has no unit here.
func servicesActive(p string, detect func() (service.Manager, error)) (bool, error) {
	m, err := detect()
	if err != nil || m == nil {
		return false, err
	}
	for _, k := range service.Kinds {
		st, err := m.State(p, k)
		if err != nil {
			return false, err
		}
		if st.Active {
			return true, nil
		}
	}
	return false, nil
}

// realRemove is the delete: deleteProjectNow, this machine's units removed.
func realRemove(client *redis.Client) func(ctx context.Context, p string) deleteResult {
	return func(ctx context.Context, p string) deleteResult {
		n, kinds, left, err := deleteProjectNow(ctx, client, p, localServices)
		return deleteResult{project: p, keys: n, services: kinds, left: left, err: err}
	}
}

// hasProc: this host has /proc, where an agent's verified process can be
// checked (Linux); macOS has none.
func hasProc() bool {
	_, err := os.Stat("/proc/self/stat")
	return err == nil
}
