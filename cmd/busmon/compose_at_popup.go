package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/filepick"
	"github.com/netbja/agent-bus-monitor/peer"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// The @ popup in the compose window's text (compose-at-file step 3, spec
// docs/superpowers/specs/2026-11-08-compose-at-file-design.md §4, and the
// home addendum): the listing runs in a tea.Cmd, once per window opening
// (once per worktree or home directory asked for), the ranking and the
// insert are step 1's and step 2's pure parts.

// atSource is where the popup lists from: the project's checkout, the
// person's home, roles.toml's shared Read denies, and the two listers
// (filepick's; tests replace them). root "" is no checkout on this machine,
// home "" no home directory.
type atSource struct {
	root, home string
	deny       func(string) bool
	// denyErr: roles.toml exists but its shared denies cannot be read; the
	// popup offers nothing then (fail closed) and says so.
	denyErr string
	list    func(ctx context.Context, o filepick.Options) (filepick.Result, error)
	listDir func(ctx context.Context, home, dir string, o filepick.Options) (filepick.Result, error)
}

// atSourceFor is the open project's source on this machine: the template's
// working_dir (made absolute and cleaned), $HOME, and the shared denies of
// roles.toml found as the launcher finds it. No roles.toml is no deny
// (secretpath still applies, §3); one that cannot be read refuses all.
func atSourceFor(project string, getenv func(string) string) *atSource {
	src := &atSource{list: filepick.List, listDir: filepick.ListDir}
	if rows, err := projlist.Templates(rolecfg.ProjectsDir(getenv)); err == nil {
		for _, r := range rows {
			if r.Name == project && r.WorkingDir != "" {
				if abs, err := filepath.Abs(r.WorkingDir); err == nil {
					src.root = filepath.Clean(abs)
				}
			}
		}
	}
	if h := getenv("HOME"); filepath.IsAbs(h) {
		src.home = filepath.Clean(h)
	}
	src.deny, src.denyErr = atDenies(getenv, src.root, src.home)
	return src
}

// atDenies compiles roles.toml's shared Read denies against root and home.
func atDenies(getenv func(string) string, root, home string) (func(string) bool, string) {
	const unreadable = "roles.toml's denies cannot be read: nothing offered"
	mp, err := rolecfg.ManifestPath(getenv)
	if err != nil {
		return nil, unreadable
	}
	man, err := rolecfg.Load(mp)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ""
	}
	if err != nil {
		return nil, unreadable
	}
	pats, err := peer.SharedReadDenies(man)
	if err != nil {
		return nil, unreadable
	}
	if root == "" { // NewDenyMatcher anchors relative patterns at the root
		root = string(filepath.Separator)
	}
	dm, err := peer.NewDenyMatcher(pats, root, home)
	if err != nil {
		return nil, unreadable
	}
	return dm.Denied, ""
}

// atPopup is the open popup: where its @ is, the rows the query ranks, the
// selection and its scroll, and the state its footer and empty list say.
type atPopup struct {
	start    int // the @'s rune index in the text
	query    string
	home     bool // a ~ query: rows are under home, inserted absolute
	rows     []atRow
	sel, top int
	state    string // reading, incomplete, outside, refused, …; "" when the list speaks for itself
	note     string // an insert's refusal (the message is full): kept until the next key
}

// atRow is one row: its path (relative to the root, or to home), as shown.
type atRow struct {
	rel, label string
	dir        bool
}

// atList is one listing of this window: the project's ("" key), a
// worktree's ("sub:" + path), or a home directory ("home:" + dir).
type atList struct {
	done bool
	res  filepick.Result
	err  error
}

// atListMsg is a listing's answer, for the window that asked (c).
type atListMsg struct {
	c   *composeState
	key string
	res filepick.Result
	err error
}

const atReading = "reading the file list" + GlyphEllipsis

// atRecentMax bounds the picks remembered this session.
const atRecentMax = 20

// atFollow keeps the popup on the text after a change: a typed @ opens it
// (typedAt), and only that (a paste never does); while open its query
// follows the cursor, and a space, the cursor out of the token or the token
// gone closes it. It returns the listing to run, if one is needed.
func (m *Model) atFollow(c *composeState, typedAt bool) tea.Cmd {
	if c == nil || m.ui.compose != c {
		return nil
	}
	if c.onTo && c.kind == composeMessage {
		c.at = nil
		return nil
	}
	q, start, ok := atToken(c.area.text, c.area.cur)
	if !ok || c.at != nil && c.at.start != start {
		c.at = nil
	}
	if !ok {
		return nil
	}
	if c.at == nil {
		if !typedAt || start != c.area.cur-1 {
			return nil
		}
		c.at = &atPopup{start: start}
	}
	return m.atCompute(c, q)
}

// atCompute ranks the rows of query q, starting the listing it needs.
func (m *Model) atCompute(c *composeState, q string) tea.Cmd {
	p := c.at
	keep := ""
	if p.query == q {
		keep = p.note
	}
	p.query, p.rows, p.state, p.home, p.note = q, nil, "", false, keep
	src := m.cfg.files
	defer p.clamp(atRowsFor(m.width))
	if src != nil && src.denyErr != "" {
		p.state = src.denyErr
		return nil
	}
	if h, why, isHome := filepick.ParseHome(q); isHome {
		p.home = true
		if why != "" {
			p.state = why
			return nil
		}
		if src == nil || src.home == "" {
			p.state = filepick.ErrNoHome.Error()
			return nil
		}
		l, cmd := m.atList(c, "home:"+h.Dir)
		if l == nil {
			p.state = atReading
			return cmd
		}
		if l.err != nil {
			p.state = l.err.Error()
			if errors.Is(l.err, filepick.ErrNoHome) {
				p.state = filepick.ErrNoHome.Error()
			}
			return nil
		}
		p.rows = atHomeRows(l.res.Entries, h.Filter)
		if len(p.rows) == 0 && h.Filter != "" {
			if why := filepick.HomeRefused(src.home, path.Join(h.Dir, h.Filter), src.deny); why != "" {
				p.state = atRefusal(why)
				return nil
			}
		}
		p.state = atIncomplete(l.res.Incomplete)
		return nil
	}
	if strings.HasPrefix(q, "/") || q == ".." || strings.HasPrefix(q, "../") {
		p.state = "outside the project: type the path yourself"
		return nil
	}
	noCheckout := "no checkout of " + m.cfg.project + " on this machine"
	if src == nil || src.root == "" {
		p.state = noCheckout
		return nil
	}
	l, cmd := m.atList(c, "")
	if l == nil {
		p.state = atReading
		return cmd
	}
	if l.err != nil {
		p.state = l.err.Error()
		if errors.Is(l.err, filepick.ErrNoCheckout) {
			p.state = noCheckout
		}
		return nil
	}
	entries, incomplete := l.res.Entries, l.res.Incomplete
	if sub, ok := filepick.SubFor(entries, q); ok { // a worktree, on demand (§2.2)
		sl, cmd := m.atList(c, "sub:"+sub)
		if sl == nil {
			p.state = atReading
			return cmd
		}
		if sl.err != nil {
			p.state = sl.err.Error()
			return nil
		}
		entries, incomplete = sl.res.Entries, sl.res.Incomplete
	}
	for _, e := range filepick.Rank(entries, q, m.ui.atRecent) {
		p.rows = append(p.rows, atRow{rel: e.Path, dir: e.Dir, label: atLabel(e.Path, e.Dir)})
	}
	if len(p.rows) == 0 && q != "" {
		if why := filepick.Refused(src.root, strings.TrimSuffix(q, "/"), src.deny); why != "" {
			p.state = atRefusal(why)
			return nil
		}
	}
	p.state = atIncomplete(incomplete)
	return nil
}

// atRefusal is a refusal's footer: a kind gets ": not offered", a sentence
// that already says what to do is kept.
func atRefusal(why string) string {
	if strings.Contains(why, ":") {
		return why
	}
	return why + ": not offered"
}

func atIncomplete(why string) string {
	if why == "" {
		return ""
	}
	return "list incomplete (" + why + ")"
}

func atLabel(rel string, dir bool) string {
	if dir {
		return rel + "/"
	}
	return rel
}

// atHomeRows ranks one home directory's entries on their names alone
// (addendum §3), shown as ~/….
func atHomeRows(entries []filepick.Entry, filter string) []atRow {
	byName := make(map[string]filepick.Entry, len(entries))
	names := make([]filepick.Entry, 0, len(entries))
	for _, e := range entries {
		n := path.Base(e.Path)
		byName[n] = e
		names = append(names, filepick.Entry{Path: n, Dir: e.Dir})
	}
	var rows []atRow
	for _, n := range filepick.Rank(names, filter, nil) {
		e := byName[n.Path]
		rows = append(rows, atRow{rel: e.Path, dir: e.Dir, label: "~/" + atLabel(e.Path, e.Dir)})
	}
	return rows
}

// atList is this window's listing key: done, or nil with the command that
// reads it (nil again while it is being read).
func (m *Model) atList(c *composeState, key string) (*atList, tea.Cmd) {
	if l := c.atLists[key]; l != nil {
		if l.done {
			return l, nil
		}
		return nil, nil
	}
	if c.atLists == nil {
		c.atLists = map[string]*atList{}
	}
	c.atLists[key] = &atList{}
	src, ctx := m.cfg.files, m.cfg.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return nil, func() tea.Msg {
		var res filepick.Result
		var err error
		switch {
		case strings.HasPrefix(key, "home:"):
			res, err = src.listDir(ctx, src.home, strings.TrimPrefix(key, "home:"), filepick.Options{Deny: src.deny})
		default:
			res, err = src.list(ctx, filepick.Options{Root: src.root, Sub: strings.TrimPrefix(key, "sub:"), Deny: src.deny})
		}
		return atListMsg{c: c, key: key, res: res, err: err}
	}
}

// atListed takes a listing's answer: dropped when its window is gone.
func (m *Model) atListed(msg atListMsg) tea.Cmd {
	c := m.ui.compose
	if c == nil || c != msg.c || c.atLists[msg.key] == nil {
		return nil
	}
	*c.atLists[msg.key] = atList{done: true, res: msg.res, err: msg.err}
	if c.at == nil {
		return nil
	}
	return m.atCompute(c, c.at.query)
}

// atRowsFor is the popup's rows at this screen width (§4.3).
func atRowsFor(w int) int {
	switch {
	case w >= wideMinWidth:
		return 8
	case w >= mediumMinWidth:
		return 6
	}
	return 4
}

// clamp keeps the selection on a row and in view.
func (p *atPopup) clamp(visible int) {
	p.sel = min(max(p.sel, 0), max(0, len(p.rows)-1))
	p.top = min(max(p.top, p.sel-visible+1, 0), p.sel)
	p.top = min(p.top, max(0, len(p.rows)-visible))
}

// composeFileKey is a key while the popup is open (composeFileBindings);
// any other key goes to the text, and the query follows it.
func (m *Model) composeFileKey(act Action, k Key) tea.Cmd {
	c := m.ui.compose
	p := c.at
	vis := atRowsFor(m.width)
	switch act {
	case ActUp, ActDown, ActPageUp, ActPageDown:
		step := map[Action]int{ActUp: -1, ActDown: 1, ActPageUp: -vis, ActPageDown: vis}[act]
		p.sel += step
		p.clamp(vis)
		return nil
	case ActOpen:
		if len(p.rows) > 0 {
			m.atInsert(c, p.rows[p.sel])
		}
		return nil
	case ActFieldNext:
		// A message's text that is only @prefix of an agent: tab keeps the
		// window's rule and makes it the recipient (prefixToRecipient).
		if c.kind == composeMessage && len(recipientCompletions(c.area.value(), m.names(m.ui.showAll))) > 0 {
			c.at = nil
			return m.composeKey(ActComposeField, k)
		}
		if len(p.rows) == 0 {
			return nil
		}
		if r := p.rows[p.sel]; r.dir {
			return m.atDescend(c, r)
		}
		m.atInsert(c, p.rows[p.sel])
		return nil
	case ActEscape:
		c.at = nil
		return nil
	}
	cmd := m.composeKey(lookup(ModeCompose, m.ui.focus, m.layout.Tier, k), k)
	return tea.Batch(cmd, m.atFollow(c, false))
}

// atInsert replaces the @query with the row's path (§5): one undo step, the
// cap kept whole; the popup closes, or stays and says why it did not.
func (m *Model) atInsert(c *composeState, r atRow) {
	p, src := c.at, m.cfg.files
	if src == nil {
		return
	}
	text, why := atPathText(src.root, src.home, r.rel, r.dir, p.home, m.cfg.settings.AtPath)
	if why != "" {
		p.note = why
		return
	}
	if !c.area.replaceAt(p.start, c.area.cur, text) {
		p.note = "the message is full"
		return
	}
	if !p.home {
		m.ui.atRecent = append([]string{r.rel}, slices.DeleteFunc(m.ui.atRecent, func(s string) bool { return s == r.rel })...)
		m.ui.atRecent = m.ui.atRecent[:min(len(m.ui.atRecent), atRecentMax)]
	}
	c.at = nil
	m.layoutCompose()
}

// atDescend makes the query the directory's (tab, §4.3): the popup stays
// open on its entries.
func (m *Model) atDescend(c *composeState, r atRow) tea.Cmd {
	p := c.at
	q := r.rel + "/"
	if p.home {
		q = "~/" + q
	}
	t := &c.area
	t.edit("", func() {
		t.unmark()
		t.text = append(append(append([]rune(nil), t.text[:p.start+1]...), []rune(q)...), t.text[t.cur:]...)
		t.cur = p.start + 1 + len([]rune(q))
		t.changed()
	})
	p.sel, p.top = 0, 0
	m.layoutCompose()
	return m.atFollow(c, false)
}

// atRects are the popup's frame and its rows on this screen: under the
// cursor's row inside the window, flipped above it when there is no room
// below; the window's text width, the full width on a narrow screen.
func (m *Model) atRects() (frame, rows Rect) {
	c := m.ui.compose
	if c == nil || c.at == nil {
		return Rect{}, Rect{}
	}
	f, tr := m.composeRects()
	n := atRowsFor(m.width)
	x, w := tr.X-1, tr.W+2
	if m.width < mediumMinWidth {
		x, w = f.X, f.W
	}
	rs := c.area.rowsAt(tr.W)
	cy := tr.Y + rowOf(rs, c.area.cur) - c.area.top
	h := n + 2
	y := cy + 1
	if y+h > f.Y+f.H-1 { // no room below, inside the window: above the row
		y = max(f.Y+1, cy-h)
	}
	h = min(h, f.Y+f.H-1-y)
	return Rect{x, y, w, h}, Rect{x + 1, y + 1, max(0, w-2), max(0, h-2)}
}

// drawAtPopup draws the popup over the window's text.
func (m Model) drawAtPopup(g *grid) {
	p := m.ui.compose.at
	fr, rr := m.atRects()
	if fr.H < 3 || fr.W < 6 {
		return
	}
	for y := fr.Y; y < fr.Y+fr.H; y++ {
		drawLine(g, fr.X, y, fr.W, nil, false)
	}
	drawFrame(g, fr.X, fr.Y, fr.W, fr.H, Frame{Focused: true, Hue: panelHue[PanelActivity],
		Scroll: ScrollState{Total: len(p.rows), Top: p.top, Visible: rr.H}})
	if len(p.rows) == 0 {
		msg := p.note
		if msg == "" {
			msg = p.state
		}
		if msg == "" {
			msg = "no file matches"
		}
		drawLine(g, rr.X, rr.Y, rr.W, Line{{Text: " " + msg, Role: RoleMuted}}, false)
		return
	}
	for i := 0; i < rr.H && p.top+i < len(p.rows); i++ {
		r := p.rows[p.top+i]
		label := truncLeft(Preview(r.label), max(1, rr.W-2))
		seg := Seg{Text: " " + label + " ", Role: RoleText}
		if p.top+i == p.sel {
			seg.Role, seg.Pill = RoleAccent, true
		}
		drawLine(g, rr.X, rr.Y+i, rr.W, Line{seg}, false)
	}
}

// atFootRight is the popup's footer end: its state or note, else the count.
func (p *atPopup) atFootRight() (string, Role) {
	switch {
	case p.note != "":
		return p.note, RoleAttention
	case p.state != "":
		return p.state, RoleMuted
	case len(p.rows) == 1:
		return "1 file", RoleMuted
	}
	return fmt.Sprintf("%d files", len(p.rows)), RoleMuted
}

// atFit cuts s to w cells only when it is wider (Fit pads).
func atFit(s string, w int) string {
	if Width(s) <= w {
		return s
	}
	return Fit(s, max(1, w))
}

// atClick: a click on a row inserts it (§4.3).
func (m *Model) atClick(x, y int) bool {
	p := m.ui.compose.at
	fr, rr := m.atRects()
	if !fr.contains(x, y) {
		return false
	}
	if rr.contains(x, y) {
		if i := p.top + y - rr.Y; i < len(p.rows) {
			p.sel = i
			m.atInsert(m.ui.compose, p.rows[i])
		}
	}
	return true
}

// atWheel scrolls the list under the pointer; false when it is not there.
func (m *Model) atWheel(x, y, delta int) bool {
	c := m.ui.compose
	if c == nil || c.at == nil {
		return false
	}
	fr, rr := m.atRects()
	if !fr.contains(x, y) {
		return false
	}
	p := c.at
	p.top = min(max(0, p.top+delta), max(0, len(p.rows)-rr.H))
	p.sel = min(max(p.sel, p.top), p.top+max(0, rr.H-1))
	return true
}
