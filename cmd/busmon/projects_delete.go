package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/projfiles"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
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
	hosts     []unitHosts
	hostsErr  error
	step      int
	backup    string // the archive, or "no backup"
	field     string
	note      string // does not match, a failed backup
	// What the delete also removes from disk (§5.5): the options as they
	// were at d, the plan read then (Apply plans again when it removes).
	files    projfiles.Options
	plan     projfiles.Plan
	planErr  error
	archive  string // the archive b made: never removed (Q7)
	unknownB bool   // b made an archive whose path the log does not name: no archive removed
}

// filesOn: an option is on, so the panel lists what goes from disk.
func (d *deletePanel) filesOn() bool { return d.files.Files || d.files.Backups }

type deleteCheckMsg struct {
	project, reason string
	hosts           []unitHosts // where units remain, read with the check (§5.2)
	hostsErr        error
	files           projfiles.Options // the options on, and where the project's files are (§5.5)
	plan            projfiles.Plan
	planErr         error
}

// unitHosts is one other host where a project's units remain installed, and
// the kinds they run (§5.2, the person's Q5).
type unitHosts struct {
	host  string
	kinds []string
}

// remoteUnitHosts reads them from {p}:services: the installed marker's host
// and each service record's, minus here, whose units the delete removes. A
// record that names no host is listed as "?".
func remoteUnitHosts(inst *bus.ServicesInstall, recs map[string]bus.ServiceRecord, here string) []unitHosts {
	kinds := map[string]map[string]bool{}
	add := func(host, kind string) {
		if host == "" {
			host = "?"
		}
		if host == here {
			return
		}
		if kinds[host] == nil {
			kinds[host] = map[string]bool{}
		}
		kinds[host][kind] = true
	}
	if inst != nil {
		for _, k := range inst.Kinds {
			add(inst.Host, k)
		}
	}
	for k, r := range recs {
		add(r.Host, k)
	}
	var out []unitHosts
	for h, ks := range kinds {
		u := unitHosts{host: h}
		for k := range ks {
			u.kinds = append(u.kinds, k)
		}
		sort.Strings(u.kinds)
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].host < out[j].host })
	return out
}

// unitHostsText is "vdr (dispatch, missions), box2 (extra)".
func unitHostsText(hs []unitHosts) string {
	var parts []string
	for _, h := range hs {
		parts = append(parts, h.host+" ("+strings.Join(h.kinds, ", ")+")")
	}
	return strings.Join(parts, ", ")
}

type deleteDoneMsg struct {
	res     deleteResult
	became  string // the second check refused: nothing was deleted
	open    bool
	hosts   []unitHosts     // where units remain: repeated in the notice
	filesOn bool            // an option was on (§5.5)
	files   *projfiles.Done // what went from disk: only after a clean delete
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
	src := m.cfg.admin
	filesOpt, backupsOpt := m.cfg.settings.DeleteFiles, m.cfg.settings.DeleteBackups
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, adminReadTimeout)
		defer cancel()
		msg := deleteCheckMsg{project: r.name, reason: quiet(c, r.name)}
		if msg.reason == "" && src != nil && src.unitHosts != nil {
			msg.hosts, msg.hostsErr = src.unitHosts(c, r.name) // read before the DEL removes them
		}
		if msg.reason == "" && (filesOpt || backupsOpt) {
			msg.files, msg.plan, msg.planErr = planFiles(src, r.name, filesOpt, backupsOpt)
		}
		return msg
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
	m.ui.del = &deletePanel{row: p.proj.rows[p.sel], unchecked: p.proj.agentsUnchecked, hosts: msg.hosts, hostsErr: msg.hostsErr,
		files: msg.files, plan: msg.plan, planErr: msg.planErr}
}

// planFiles is where the project's files are, with the options on, and what
// a delete would remove from them (§5.5). Nothing is removed here.
func planFiles(src *adminSource, project string, files, backups bool) (projfiles.Options, projfiles.Plan, error) {
	o := projfiles.Options{Project: project}
	if src == nil || src.files == nil {
		o.Files, o.Backups = files, backups
		return o, projfiles.Plan{}, errors.New("no reader of the project's files")
	}
	o, err := src.files(project)
	o.Project, o.Files, o.Backups = project, files, backups
	if err != nil {
		return o, projfiles.Plan{}, err
	}
	p, err := projfiles.PlanFor(o)
	return o, p, err
}

// backupArchiveInLog is the archive a backup run made: its "backup: <path>"
// line, an absolute path to a .tar.zst, after the run's own header (the
// last "--- " line), so never an earlier run's (§5.5, Q7). "" when none.
func backupArchiveInLog(log string) string {
	f, err := os.Open(log)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 64<<10 {
		f.Seek(-64<<10, io.SeekEnd)
	}
	b, _ := io.ReadAll(io.LimitReader(f, 64<<10))
	lines := strings.Split(string(b), "\n")
	start := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "--- ") {
			start = i + 1
		}
	}
	archive := ""
	for _, l := range lines[start:] {
		p, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), "backup: ")
		if ok && filepath.IsAbs(p) && strings.HasSuffix(p, ".tar.zst") && !strings.ContainsAny(p, "\x00") {
			archive = p
		}
	}
	return archive
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
func (m *Model) deleteBackupDone(r *sleepRun, err error, archive string) {
	d := m.ui.del
	if d == nil || d.step != delBackingUp || r.project != d.row.name {
		return
	}
	if err != nil {
		d.step, d.note = delBackup, "backup failed: "+Preview(r.last)+" ("+r.log+"); b again, n without, esc cancels"
		return
	}
	// The archive is the path the run printed, not its last line (a note,
	// most often); one it does not name keeps every archive (fail closed).
	d.step, d.backup, d.archive, d.unknownB = delName, r.last, archive, archive == ""
	if archive != "" {
		d.backup = archive
	}
}

// runDelete checks again, then deletes, in one command (§5.1, §5.3).
func (m *Model) runDelete() tea.Cmd {
	d := m.ui.del
	d.step = delDeleting
	name, open, hosts := d.row.name, d.row.open, d.hosts
	quiet, src, ctx := m.adminQuiet(), m.cfg.admin, m.cfgCtx()
	files, filesOn := d.files, d.filesOn() && d.planErr == nil
	files.KeepArchive = d.archive
	if d.unknownB {
		files.Backups = false // the new archive cannot be told apart: none goes
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, deleteTimeout)
		defer cancel()
		if why := quiet(c, name); why != "" {
			return deleteDoneMsg{res: deleteResult{project: name}, became: why, open: open, filesOn: filesOn}
		}
		if src == nil || src.remove == nil {
			return deleteDoneMsg{res: deleteResult{project: name, err: errors.New("no broker to delete from")}, open: open, filesOn: filesOn}
		}
		msg := deleteDoneMsg{res: src.remove(c, name), open: open, hosts: hosts, filesOn: filesOn}
		if filesOn && msg.res.err == nil && len(msg.res.left) == 0 { // files only after a clean delete
			done := projfiles.Apply(files)
			msg.files = &done
		}
		return msg
	}
}

// gotDeleteDone closes the panel and says what happened; after a clean
// delete of the open project, busmon quits with its message (§5.2.3).
func (m *Model) gotDeleteDone(msg deleteDoneMsg) tea.Cmd {
	m.ui.del = nil
	r := msg.res
	noFiles := ""
	if msg.filesOn {
		noFiles = "; no file removed"
	}
	switch {
	case msg.became != "":
		m.notice.push(notice{text: "became live: " + msg.became + "; nothing deleted", level: noticeAttention})
	case r.err != nil: // deleteProjectNow's words: what was and was not deleted
		m.notice.push(notice{text: r.err.Error() + noFiles, level: noticeAttention})
	case len(r.left) > 0:
		m.notice.push(notice{text: cameBack(r.project, r.keys, r.left) + noFiles, level: noticeAttention})
	default:
		text := fmt.Sprintf("deleted %d %s of %s", r.keys, plural(int(r.keys), "key"), r.project)
		if len(r.services) > 0 {
			text += fmt.Sprintf("; removed the %s %s here", strings.Join(r.services, " and "), plural(len(r.services), "service"))
		}
		if len(msg.hosts) > 0 {
			var names []string
			for _, h := range msg.hosts {
				names = append(names, h.host)
			}
			text += "; units reported on " + strings.Join(names, ", ") + ": agentbus --project " + r.project + " service remove there"
		}
		level := noticeDone
		if msg.files != nil {
			text += filesDoneText(*msg.files)
			if len(msg.files.Errs) > 0 {
				level = noticeAttention
			}
		}
		if msg.open {
			m.exitNote = fmt.Sprintf("%s deleted (%d %s); busmon --pick to open another project", r.project, r.keys, plural(int(r.keys), "key"))
			return tea.Quit
		}
		m.notice.push(notice{text: text, level: level})
	}
	if p := m.projectsPageOpen(); p != nil {
		return m.projectsCmd(p.serial)
	}
	return nil
}

// filesDoneText is what went from disk, for the done notice.
func filesDoneText(d projfiles.Done) string {
	var parts []string
	if d.Template != "" {
		parts = append(parts, "the template")
	}
	if n := len(d.Worktrees); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "worktree")))
	}
	if n := len(d.Archives); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "archive")))
	}
	text := ""
	if len(parts) > 0 {
		text = "; also removed: " + strings.Join(parts, ", ")
	}
	if len(d.Errs) > 0 {
		text += fmt.Sprintf("; %d could not be removed (%s)", len(d.Errs), Preview(d.Errs[0].Error()))
	}
	return text
}

// filesLines are the panel's lines on what goes from disk (§5.5).
func (m Model) filesLines() []Line {
	d := m.ui.del
	if !d.filesOn() {
		return nil
	}
	att := func(s string) Line { return Line{{Text: s, Role: RoleAttention}} }
	if d.planErr != nil {
		return []Line{att("also deleted (settings): ? (" + Preview(d.planErr.Error()) + "): no file will be removed")}
	}
	p := d.plan
	if d.archive != "" && d.files.Backups {
		p.Kept = d.archive
	}
	lines := []Line{att("also deleted (settings):")}
	for _, s := range p.Lines(d.row.name) {
		lines = append(lines, att("  "+s))
	}
	if p.Empty() {
		lines = append(lines, Line{{Text: "  nothing found on disk", Role: RoleMuted}})
	}
	if d.unknownB && d.files.Backups {
		lines = append(lines, att("  backups: kept, the new archive's path is not in the log"))
	}
	return lines
}

// unitHostsLines are the panel's lines on the other hosts' units (§5.2):
// where they stay, and the command that removes them there.
func (m Model) unitHostsLines() []Line {
	d := m.ui.del
	// The broker reports only the last up's marker and each kind's last
	// record: a host that fell silent leaves no trace (reviewer-codex), so
	// the panel never claims to name them all.
	caveat := Line{{Text: "other hosts may keep units the broker no longer records: agentbus service list there", Role: RoleMuted}}
	switch {
	case d.hostsErr != nil:
		return []Line{{{Text: "units on other hosts: ? (" + Preview(d.hostsErr.Error()) + ")", Role: RoleAttention}}, caveat}
	case len(d.hosts) == 0:
		return []Line{{{Text: "units reported: this machine only", Role: RoleMuted}}, caveat}
	}
	return []Line{{{Text: "units reported on: " + unitHostsText(d.hosts), Role: RoleAttention}},
		{{Text: "remove them there: agentbus --project " + d.row.name + " service remove", Role: RoleMuted}}, caveat}
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
	del := "This is a DEL, not a reset: streams, consumer groups, agents/usage/budget/verdicts, pilot and gate leases all go."
	if !d.filesOn() {
		del += " The template and the worktrees stay."
	}
	lines = append(lines, Line{{Text: del, Role: RoleText}})
	lines = append(lines, m.filesLines()...)
	lines = append(append(lines, m.unitHostsLines()...), nil)
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

// projectFiles is where project p's files are on this machine (§5.5): its
// template in the projects folder and the template's working_dir, the backup
// folder, and this host as archives name it. The options are the caller's.
func projectFiles(p string, getenv func(string) string, host string) (projfiles.Options, error) {
	o := projfiles.Options{Project: p, Host: host}
	rows, err := projlist.Templates(rolecfg.ProjectsDir(getenv))
	if err != nil {
		return o, fmt.Errorf("the templates: %w", err)
	}
	for _, r := range rows {
		if r.Name == p {
			o.Template, o.WorkingDir = r.Path, r.WorkingDir
		}
	}
	cfg := config.Load(config.Path(getenv), "config.toml")
	dir, _, err := config.BackupDir(config.Resolver{Getenv: getenv, Files: []*config.File{cfg}}, getenv)
	if err != nil {
		return o, fmt.Errorf("the backup folder: %w", err)
	}
	o.BackupDir = dir
	return o, nil
}
