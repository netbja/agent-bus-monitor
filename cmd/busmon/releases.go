package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/plan"
)

// The Plan tab's Releases column (the person's "Re: Plan tab, r3", item 5):
// the next release with its decided ideas, the current plan highlighted,
// then the past releases, newest first, with their date and their plan's
// planned tasks (a plan file says nothing of what was done). Its source is
// the project's checkout on this machine, read as it is: the release tags (vX.Y.Z) with their date, and docs/plans/*.json by
// plan name. No new store: a busmon with no checkout here lists the next
// and the current release only.

// releaseTag is one release tag and its date (YYYY-MM-DD).
type releaseTag struct{ Name, Date string }

// releaseData is what the checkout said: its release tags, newest first,
// and its plan files by plan name.
type releaseData struct {
	read  bool // a read has succeeded at least once
	err   bool // the last read failed: the kept tags may be old
	tags  []releaseTag
	plans map[string]plan.File
}

// releasesEvery is how often the checkout is read again: tags and plan
// files change at a release, not between two polls.
const releasesEvery = 5 * time.Minute

// releaseTagRe is a release tag: vMAJOR.MINOR.PATCH, nothing after.
var releaseTagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// localReleasesOf reads the project's checkout on this machine; nil when
// there is none (projectCheckout).
func localReleasesOf(project string, getenv func(string) string) func(context.Context) (releaseData, error) {
	root := projectCheckout(project, getenv)
	if root == "" {
		return nil
	}
	return func(ctx context.Context) (releaseData, error) { return readReleases(ctx, root) }
}

// readReleases is dir's release tags, newest first, and its plan files. A
// plan file that does not decode, or names no plan, is left out.
func readReleases(ctx context.Context, dir string) (releaseData, error) {
	out, err := mission.Git{Repo: dir}.Run(ctx, "for-each-ref", "--sort=-creatordate", "--format=%(refname:short)%09%(creatordate:short)", "refs/tags")
	if err != nil {
		return releaseData{}, err
	}
	d := releaseData{read: true, plans: map[string]plan.File{}}
	for _, line := range strings.Split(out, "\n") {
		name, date, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && releaseTagRe.MatchString(name) {
			d.tags = append(d.tags, releaseTag{Name: name, Date: date})
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "docs", "plans", "*.json"))
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var f plan.File // as written then: an older plan may carry fields this one has not
		if json.Unmarshal(b, &f) != nil || f.Plan == "" {
			continue
		}
		d.plans[f.Plan] = f
	}
	return d, nil
}

// readReleasesDue reads the checkout when the last read is releasesEvery
// old, before the lock; a failed read keeps the last tags and flags it.
func readReleasesDue(ctx context.Context, st *shared, now time.Time) {
	st.mu.Lock()
	of, at := st.releasesOf, st.releasesAt
	st.mu.Unlock()
	if of == nil || (!at.IsZero() && now.Sub(at) < releasesEvery) {
		return
	}
	d, err := of(ctx)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.releasesAt = now
	if err != nil {
		st.releases.err = true
		return
	}
	st.releases = d
}

// planOfTag is the plan a release tag closes: v0.28.0 closes v0.28; a
// patch closes none.
func planOfTag(tag string) (string, bool) {
	minor, patch, ok := strings.Cut(strings.TrimPrefix(tag, "v"), ".")
	if !ok {
		return "", false
	}
	mid, patch, ok := strings.Cut(patch, ".")
	if !ok || patch != "0" {
		return "", false
	}
	return "v" + minor + "." + mid, true
}

const (
	releaseNext    = "rel:next"    // the next release's row
	releaseCurrent = "rel:current" // the current plan's row
	releaseTagKey  = "tag:"        // a past release's row: the prefix, then its tag
	releaseIdeas   = 5             // decided ideas listed under next, then "+N more"
)

// decidedIdeas are the ideas decided and not yet started: the next
// release's, by id.
func decidedIdeas(ideas []bus.Idea) []bus.Idea {
	var out []bus.Idea
	for _, it := range ideas {
		if it.State == bus.IdeaDecided {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// releaseItems are the column's rows, width cells wide: next and its
// decided ideas, the current plan, then the past releases.
func releaseItems(s snapshot, now time.Time, width int) []ListItem {
	sep := Seg{Text: "  ", Role: RoleMuted}
	ideas := decidedIdeas(s.ideas)
	next := Line{{Text: "next", Role: RoleText, Bold: true}, sep}
	switch {
	case s.ideasErr && len(ideas) == 0:
		next = append(next, Seg{Text: "ideas unreadable", Role: RoleMuted})
	case len(ideas) == 1:
		next = append(next, Seg{Text: "1 decided idea", Role: RoleMuted})
	default:
		next = append(next, Seg{Text: fmt.Sprintf("%d decided ideas", len(ideas)), Role: RoleMuted})
	}
	items := []ListItem{{Key: releaseNext, Line: FitLine(next, width), Selectable: true}}
	for i, it := range ideas {
		if i == releaseIdeas {
			items = append(items, ListItem{Line: Line{{Text: fmt.Sprintf("  +%d more", len(ideas)-i), Role: RoleMuted}}})
			break
		}
		items = append(items, ListItem{Line: FitLine(Line{{Text: "  " + GlyphIdea + " " + Preview(it.Title), Role: RoleMuted}}, width)})
	}
	if sum, ok := s.planState.summary(now); ok {
		cur := Line{{Text: GlyphDotFull + " " + Preview(sum.name), Role: RoleAccent, Bold: true}, sep,
			{Text: fmt.Sprintf("%d/%d", sum.done, sum.total), Role: RoleText}, sep, {Text: "now", Role: RoleAccent}}
		items = append(items, ListItem{Key: releaseCurrent, Line: FitLine(cur, width), Selectable: true})
	}
	d := s.releases
	switch {
	case !d.read && d.err:
		items = append(items, ListItem{Line: Line{{Text: "past releases unreadable", Role: RoleMuted}}})
	case !d.read:
		items = append(items, ListItem{Line: Line{{Text: "no checkout here", Role: RoleMuted}}})
	}
	for _, t := range d.tags {
		l := Line{{Text: Preview(t.Name), Role: RoleText}, sep, {Text: t.Date, Role: RoleMuted}}
		if name, ok := planOfTag(t.Name); ok {
			if f, ok := d.plans[name]; ok {
				l = append(l, sep, Seg{Text: planTasks(len(f.Tasks)), Role: RoleMuted}) // planned: a plan file holds no completion (reviewer-codex)
			}
		}
		items = append(items, ListItem{Key: releaseTagKey + t.Name, Line: FitLine(l, width), Selectable: true})
	}
	return items
}

// fillReleases fills the column's list; a no-op while it is hidden.
func (m *Model) fillReleases() {
	l := &m.lists.releases
	if m.layout.Side.W == 0 {
		l.SetItems(nil)
		return
	}
	l.SetItems(releaseItems(m.snap, m.now, max(0, m.layout.Side.W-2-listGutter)))
	if l.Selected() == "" {
		l.First()
	}
	title := Line{{Text: "Releases", Role: RoleText}}
	var label Line
	if m.snap.releases.err {
		label = Line{{Text: "read failed", Role: RoleAttention}}
	}
	l.SetFrame(title, label)
	l.SetFooter(Line{{Text: "enter plan " + GlyphNone + " V hide", Role: RoleMuted}})
}

// releasesShown says whether the Plan tab draws its Releases column: V's
// choice, else busmon's width (releasesMinWidth).
func (m *Model) releasesShown() bool {
	switch m.ui.releases {
	case 1:
		return true
	case -1:
		return false
	}
	return m.width >= releasesMinWidth
}

// toggleReleases is V: the column the other way from what is drawn now.
func (m *Model) toggleReleases() {
	if m.releasesShown() {
		m.ui.releases = -1
	} else {
		m.ui.releases = 1
	}
	if m.ui.releases == 1 && m.width >= releasesMinWidth || m.ui.releases == -1 && m.width < releasesMinWidth {
		m.ui.releases = 0 // the width's own choice again
	}
	m.refresh()
}

// openRelease is Enter on the column: a release's plan, read only.
func (m *Model) openRelease(key string) tea.Cmd {
	width := m.readerWidth()
	s := m.snap
	switch {
	case key == releaseNext:
		body := []Line{{{Text: "The ideas decided for the next release, not started yet (F6 changes them).", Role: RoleMuted}}, nil}
		ideas := decidedIdeas(s.ideas)
		if len(ideas) == 0 {
			body = append(body, Line{{Text: "no decided idea", Role: RoleMuted}})
		}
		for _, it := range ideas {
			body = append(body, FitLine(Line{{Text: GlyphIdea + " " + Preview(it.ID), Role: RoleText, Bold: true}, {Text: "  " + Preview(it.Title), Role: RoleText}}, width))
		}
		return m.push(page{kind: ovRelease, key: key, title: "Next release " + GlyphNone + " read only", body: body, fixed: true})
	case key == releaseCurrent:
		if !s.planState.loaded {
			return nil
		}
		state := map[string]plan.Row{}
		for _, r := range s.planState.rows {
			state[r.Task.ID] = r
		}
		f := s.planState.file
		return m.push(page{kind: ovRelease, key: key, title: "Release " + Preview(f.Plan) + " " + GlyphNone + " current " + GlyphNone + " read only",
			body: releaseBody(f, func(t plan.Task) Seg {
				word, role := planStateWord(state[t.ID])
				return Seg{Text: word, Role: role}
			}, width), fixed: true})
	}
	tag, ok := strings.CutPrefix(key, releaseTagKey)
	if !ok {
		return nil
	}
	title := "Release " + Preview(tag) + " " + GlyphNone + " read only"
	name, ok := planOfTag(tag)
	f, has := s.releases.plans[name]
	if !ok || !has {
		what := "a patch release: its plan is its minor's"
		if ok {
			what = "no plan file for " + Preview(name) + " in docs/plans"
		}
		return m.push(page{kind: ovRelease, key: key, title: title, body: []Line{{{Text: what, Role: RoleMuted}}}, fixed: true})
	}
	return m.push(page{kind: ovRelease, key: key, title: title, body: releaseBody(f, nil, width), fixed: true})
}

// releaseBody is a plan file as a page: its goal, then each group's tasks
// (id, title, owner, and the state word when state is given), the tasks of
// no listed group last, under Other.
func releaseBody(f plan.File, state func(plan.Task) Seg, width int) []Line {
	body := []Line{{{Text: Preview(f.Goal), Role: RoleMuted}}, nil}
	groups := append([]plan.Group(nil), f.Groups...)
	listed := map[string]bool{}
	for _, g := range groups {
		listed[g.ID] = true
	}
	groups = append(groups, plan.Group{Title: "Other"})
	for _, g := range groups {
		var rows []Line
		for _, t := range f.Tasks {
			if t.Group != g.ID && (g.ID != "" || listed[t.Group]) {
				continue
			}
			l := Line{{Text: "  " + Preview(t.ID), Role: RoleText, Bold: true}, {Text: "  " + Preview(t.Title), Role: RoleText}, {Text: "  " + Preview(t.Owner), Role: RoleMuted}}
			if state != nil {
				l = append(l, Seg{Text: "  ", Role: RoleMuted}, state(t))
			}
			rows = append(rows, FitLine(l, width))
		}
		if len(rows) == 0 {
			continue
		}
		body = append(body, Line{{Text: Preview(g.Title), Role: RoleAccent, Bold: true}, {Text: "  " + planTasks(len(rows)), Role: RoleMuted}})
		body = append(body, rows...)
	}
	return body
}
