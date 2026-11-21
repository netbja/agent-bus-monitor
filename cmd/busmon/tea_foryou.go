package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/plan"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/textfmt"
)

// For you (busmon Charm spec, decision 2): one list of what waits on the
// person, ranked, oldest first within a rank. Every other surface is a named
// subset of it (decisions, unreadItems), so they can never disagree.

const (
	rankAsk    = iota // an open ask, one row per ask
	rankHealth        // the stall guard: the team waits on the person, or the person's focus holds a hand-off
	rankWait          // a waiting agent with no open ask, or its client's prompt
	rankGate          // a pending verdict: an open 4-eyes gate
	rankReport        // a report that needs the person and names no one live target (triage-dedupe §3)
	rankMerge         // a change waiting for the person's merge (merge-queue): Enter opens its page
	rankUnread        // an Activity line for the person not opened yet
)

// forYou is the list, from the snapshot and the feed's marks. The "for you"
// hint (forYouHint) is not a rank: it stays a flag on the agent's row.
func forYou(s snapshot, feed []feedLine) []attentionItem {
	var out []attentionItem
	for _, a := range openAsks(s.asks) {
		if a.Open() == 0 || a.CreatedAt <= 0 {
			continue
		}
		out = append(out, attentionItem{Rank: rankAsk, Key: "ask:" + a.ID, Agent: a.From, Text: a.Title,
			At: msTime(a.CreatedAt), AskID: a.ID, Count: a.Open()})
	}
	for name, a := range s.agents {
		switch {
		case a.state == "waiting" && !answered(a) && a.openAsk == 0:
			out = append(out, attentionItem{Rank: rankWait, Key: fmt.Sprintf("wait:%s:%d", name, a.stateAt.UnixMilli()),
				Agent: name, Text: a.message, At: a.stateAt})
		case screenStuck(a):
			out = append(out, attentionItem{Rank: rankWait, Key: fmt.Sprintf("stuck:%s:%d", name, a.obsScreenSince.UnixMilli()),
				Agent: name, Text: stuckWords(a), At: a.obsScreenSince})
		case promptBlocked(a):
			since := a.obsSince
			if since.IsZero() {
				since = a.obsAt
			}
			text := blockedWords
			if a.trustFolder != "" {
				text = blockedWords + ": " + trustPromptText(a.client, a.trustFolder) // the words first, then what it asks
			}
			out = append(out, attentionItem{Rank: rankWait, Key: fmt.Sprintf("prompt:%s:%d", name, since.UnixMilli()),
				Agent: name, Text: text, At: since})
		}
		out = append(out, personGateItems(s.person, name, a.gates)...)
	}
	if _, isAgent := s.agents[s.person]; !isAgent {
		out = append(out, personGateItems(s.person, s.person, s.personGates)...)
	}
	out = append(out, stallItems(s.guardianHealth, s.guardianNow)...)
	out = append(out, mergeItems(s)...)
	out = foldLines(s, feed, out)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		if a.At.IsZero() != b.At.IsZero() { // an undated item (a gate whose ref is not a stream id) last
			return b.At.IsZero()
		}
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.Key < b.Key
	})
	return out
}

// foldLines adds the feed's lines to the targets' items (triage-dedupe §2,
// §3; the person's §7 answers): a line naming exactly one live target (a
// merge item by #<n>, PR <n> or its branch, an ask by its id, a waiting
// agent by its own newest report) attaches to it; a report is an item only
// with --needs (answer: until the person writes to its sender; read: until
// read), its own when it names no single live target, replaced by the same
// sender's newer one naming the same targets; any other unread line for the
// person stays its own row, as before.
func foldLines(s snapshot, feed []feedLine, out []attentionItem) []attentionItem {
	tg := liveTargets(out)
	newest := map[string]string{} // agent -> its newest report's id
	for _, fl := range feed {
		if !fl.sep && fl.ev.Kind == "report" {
			newest[fl.ev.Agent] = fl.ev.ID
		}
	}
	own := map[string]int{} // sender and targets -> its own report item
	for _, fl := range feed {
		if fl.sep {
			continue
		}
		unread := fl.mark == markUnread
		if i, ok := tg.asks[fl.ev.ID]; ok && fl.ev.Type == bus.CmdAsk {
			out[i].Unread = out[i].Unread || unread // listed once: the ask's row carries the mark
			continue
		}
		report := fl.ev.Kind == "report" || len(fl.personNotices) > 0
		if !report {
			if _, _, ok := reportSubject(fl.ev.Message); ok && fl.ev.Kind == "cmd" {
				report = true // a notice whose report is not at hand
			}
		}
		needs := lineNeeds(fl)
		if fl.ev.Kind == "report" { // a waiting agent's own newest report is its wait's
			if i, ok := tg.waits[fl.ev.Agent]; ok && newest[fl.ev.Agent] == fl.ev.ID {
				out[i].Unread = out[i].Unread || unread
				if needs != "" {
					out[i].Needs = needs
				}
				continue
			}
		}
		switch {
		case report && needs == "":
			continue // Activity only (§3)
		case report && needs == bus.NeedsRead && !unread:
			continue
		case report && needs == bus.NeedsAnswer && answeredSince(s.person, sender(fl.ev), fl.at, feed):
			continue
		case !report && !unread:
			continue
		}
		ids := []string{fl.ev.ID}
		if len(fl.personNotices) > 0 { // one row per report, holding every notice to the person
			ids = fl.personNotices
		}
		named := tg.named(lineFullText(fl))
		if len(named) == 1 {
			i := named[0]
			out[i].Unread = out[i].Unread || unread
			if needs != "" {
				out[i].Needs = needs
			}
			continue
		}
		text := fl.ev.Message
		if report {
			text = lineHeadline(fl.ev) // a report notice reads as its report's headline (report-one-row §4)
		} else if sub, _, ok := reportSubject(text); ok {
			text = sub
		}
		if !report {
			out = append(out, attentionItem{Rank: rankUnread, Key: unreadKey(fl), Agent: sender(fl.ev),
				Text: text, At: fl.at, EventID: ids[0], IDs: ids})
			continue
		}
		reportID := fl.ev.ID
		if fl.ev.Kind == "cmd" && fl.ev.Ref != "" {
			reportID = fl.ev.Ref // a notice alone: its report's id
		}
		it := attentionItem{Rank: rankReport, Key: "report:" + reportID, Agent: sender(fl.ev), Text: text, At: fl.at,
			EventID: ids[0], IDs: ids, Needs: needs, Unread: unread}
		sig := sender(fl.ev) + "|" + fmt.Sprint(named)
		if j, ok := own[sig]; ok {
			out[j] = it // the feed is in order: the newer replaces
			continue
		}
		own[sig] = len(out)
		out = append(out, it)
	}
	return out
}

// lineNeeds is what a line's report asks of the person: the report's own
// field, else one of its notices' (a notice alone carries it too).
func lineNeeds(fl feedLine) string {
	if fl.ev.Needs != "" {
		return fl.ev.Needs
	}
	for _, n := range fl.notices {
		if n.Needs != "" {
			return n.Needs
		}
	}
	return ""
}

// lineFullText is what a line says in full, for the targets it names.
func lineFullText(fl feedLine) string {
	if fl.ev.Full != "" {
		return fl.ev.Full
	}
	return fl.ev.Message
}

// answeredSince: the person wrote to who after at (the send that closes a
// --needs answer report, as a waiting agent's row today).
func answeredSince(person, who string, at time.Time, feed []feedLine) bool {
	if person == "" {
		return false
	}
	for _, fl := range feed {
		if !fl.sep && fl.ev.Kind == "cmd" && fl.ev.From == person && fl.ev.Target == who && fl.at.After(at) {
			return true
		}
	}
	return false
}

// targets are the live items a line may name, by what names them.
type targets struct {
	prs      map[int]int
	branches map[string]int
	asks     map[string]int
	waits    map[string]int
}

func liveTargets(out []attentionItem) targets {
	t := targets{prs: map[int]int{}, branches: map[string]int{}, asks: map[string]int{}, waits: map[string]int{}}
	for i, it := range out {
		switch {
		case it.Rank == rankAsk:
			t.asks[it.AskID] = i
		case it.Rank == rankWait:
			t.waits[it.Agent] = i
		case it.Rank == rankMerge && it.Key != mergeUnknownKey:
			if n, ok := strings.CutPrefix(it.Key, "merge:pr:"); ok {
				if v, err := strconv.Atoi(n); err == nil {
					t.prs[v] = i
				}
			} else if b, ok := strings.CutPrefix(it.Key, "merge:branch:"); ok {
				t.branches[b] = i
			}
			if it.Branch != "" {
				t.branches[it.Branch] = i
			}
		}
	}
	return t
}

// prRef is a PR named in text: #<n>, PR <n> or PR #<n>.
var prRef = regexp.MustCompile(`(?i)(?:\bPR\s*#?|#)(\d+)\b`)

// named are the live items text names, by index, sorted, once each; a
// number no live item holds names nothing ("#1 priority").
func (t targets) named(text string) []int {
	seen := map[int]bool{}
	for _, m := range prRef.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			if i, ok := t.prs[n]; ok {
				seen[i] = true
			}
		}
	}
	for b, i := range t.branches {
		if containsWord(text, b) {
			seen[i] = true
		}
	}
	for id, i := range t.asks {
		if strings.Contains(text, id) {
			seen[i] = true
		}
	}
	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

// containsWord: w in s, not inside a longer name (feat/a is not in feat/ab).
func containsWord(s, w string) bool {
	name := func(r byte) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.IndexByte("-_./", r) >= 0
	}
	for i := 0; ; {
		j := strings.Index(s[i:], w)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(w)
		if (j == 0 || !name(s[j-1])) && (end == len(s) || !name(s[end]) || s[end] == '.' && (end+1 == len(s) || !name(s[end+1]))) {
			return true
		}
		i = j + 1
	}
}

// activityUnread is the unread badge (the person's §7 answer 3): Activity's
// unread marks, the plain reports included, apart from For you's items.
func activityUnread(feed []feedLine) int {
	n := 0
	for _, fl := range feed {
		if !fl.sep && fl.mark == markUnread {
			n++
		}
	}
	return n
}

// stallItems are the stall guard's rows for the person (spec
// 2026-11-01-stall-guard-design.md, busmon): team-stall waiting on the
// person's own task, and clear-stall held by the person's focused pane. Only
// a current warn with that reason; the key carries the episode's since, so a
// new episode is a new row.
func stallItems(records map[string]guardian.Record, now time.Time) []attentionItem {
	var out []attentionItem
	for _, c := range []struct{ id, reason, word string }{
		{"team-stall", "waiting-person", "stall"},
		{"clear-stall", "pane-focused", "clear"},
	} {
		d, ok := guardian.ByID(c.id)
		rec, has := records[c.id]
		if !ok || !has || rec.Reason != c.reason || guardian.EffectiveStatus(rec, now.UnixMilli(), d.Interval) != guardian.Warn {
			continue
		}
		out = append(out, attentionItem{Rank: rankHealth, Key: fmt.Sprintf("health:%s:%d", c.id, rec.Since),
			Agent: c.word, Text: rec.Detail, At: msTime(rec.Since)})
	}
	return out
}

// personGateItems are the rows of the gates on gated that concern the person
// (foryou-gates): a gate the person opened (they asked for that review) or
// one holding the person. A review between agents is theirs to finish, never
// a row: master's challenges to reviewers stay off the list and the badge.
// The default identity is no person, so it never matches. Who reviews whom
// depends on who challenged, so the row names both, neutrally.
func personGateItems(person, gated string, gates map[string]string) []attentionItem {
	if person == "" || person == defaultHuman {
		return nil
	}
	var out []attentionItem
	for ref, meta := range gates {
		g := bus.ParseGateMeta(meta)
		if g.Challenger != person && gated != person {
			continue
		}
		summary := g.Text
		if summary == "" {
			summary = ref
		}
		out = append(out, attentionItem{Rank: rankGate, Key: "gate:" + gated + ":" + ref, Agent: gated, By: g.Challenger,
			Text: summary, At: eventTimestamp(ref)})
	}
	return out
}

// decisions are the items that wait on a person's decision: asks, waiting
// agents and gates. The header's for-you badge counts them; the reminder lists
// them; never an unread line.
func decisions(items []attentionItem) []attentionItem {
	var out []attentionItem
	for _, it := range items {
		if it.Rank != rankUnread && it.Key != mergeUnknownKey {
			out = append(out, it)
		}
	}
	return out
}

// unreadItems is the header's unread badge: the unread lines of the list,
// an ask's folded line included, so it equals the Activity marks.
func unreadItems(items []attentionItem) int {
	n := 0
	for _, it := range items {
		if it.Rank == rankUnread || it.Unread {
			n++
		}
	}
	return n
}

// planNextPrefix is the plan pointer's key: plan-next:<task> (home-inbox-drawer
// §3.3). Enter opens the task page.
const planNextPrefix = "plan-next:"

// nextPlanTask is the plan pointer's task (§3.3): the first person task, in
// the plan file's order, that is Waiting; with none, the first Blocked, then
// Upcoming. False with none at all, or no plan loaded.
//
// Plan tasks are no For you rows any more (the person's choice, 2026-11-07,
// home-inbox-drawer): a task waiting for the person is counted in the
// header's plan item (planSeg's ♙), not in decide, and this one pointer
// says which comes next. It supersedes "a decision like any other, counted
// in the title and the badge": no "Waiting for you" line remains in For you.
func nextPlanTask(p planState) (plan.Row, bool) {
	if !p.loaded {
		return plan.Row{}, false
	}
	for _, st := range []plan.State{plan.Waiting, plan.Blocked, plan.Upcoming} {
		for _, r := range p.rows {
			if r.Task.Owner == plan.Person && r.State == st {
				return r, true
			}
		}
	}
	return plan.Row{}, false
}

// planPointer is the pointer's row: muted, never counted (§3.3).
func planPointer(s snapshot) (emptyRow, bool) {
	r, ok := nextPlanTask(s.planState)
	if !ok {
		return emptyRow{}, false
	}
	return emptyRow{key: planNextPrefix + r.Task.ID, line: Line{{Text: "Next for you: ", Role: RoleMuted}, {Text: Preview(r.Task.Title), Role: RoleMuted}}}, true
}

// forYouPanelItems is what the For you panel draws: the list minus its
// unread lines (§3.2). They stay in the model: the strip's unread chip, u and
// the walk reach them; an ask's own unread mark stays on its row.
func forYouPanelItems(items []attentionItem) []attentionItem {
	var out []attentionItem
	for _, it := range items {
		if it.Rank != rankUnread {
			out = append(out, it)
		}
	}
	return out
}

// forYouLabel is the panel's frame label: the strip's words, decide and
// merge, each at 0 left out (§3.2), so the panel and the strip never disagree.
func forYouLabel(q queueStrip) string {
	var parts []string
	if q.decide > 0 {
		parts = append(parts, fmt.Sprintf("%d decide", q.decide))
	}
	if q.merge > 0 {
		parts = append(parts, fmt.Sprintf("%d merge", q.merge))
	}
	return strings.Join(parts, " "+GlyphNone+" ")
}

// emptyRow is one line of the empty state, selectable when it has a key:
// plan-next:<task> opens the plan task.
type emptyRow struct {
	key  string
	line Line
}

// forYouEmpty is the empty state's lines (the recap and the layout read them).
func forYouEmpty(s snapshot, owed, decide int) []Line {
	var out []Line
	for _, r := range forYouEmptyRows(s, owed, decide) {
		out = append(out, r.line)
	}
	return out
}

// forYouEmptyRows: what is owed, then the plan's decisions the strip counts
// (decide: Work's decide rows, workModel.decideCount; views-redesign §6),
// else "Nothing expected", then the plan pointer (planPointer, the one
// producer), if any. "Nothing expected from you." only when the strip counts
// nothing. What is in progress is the Agents panel's: never repeated here
// (foryou-no-progress, the person's answer). A plan task waiting for the
// person is the pointer, not something owed (home-inbox-drawer §3.3).
func forYouEmptyRows(s snapshot, owed, decide int) []emptyRow {
	var rows []emptyRow
	if owed > 0 {
		word := "requests"
		if owed == 1 {
			word = "request"
		}
		rows = append(rows, emptyRow{line: Line{{Text: fmt.Sprintf("Owed by you: %d %s (Requests)", owed, word), Role: RoleAttention}}})
	}
	if decide > 0 {
		rows = append(rows, emptyRow{line: Line{{Text: textfmt.Count(decide, "decision") + " in the plan (3 Work)", Role: RoleAttention}}})
	}
	if owed == 0 && decide == 0 {
		rows = append(rows, emptyRow{line: Line{{Text: "Nothing expected from you.", Role: RoleOK}}})
	}
	if p, ok := planPointer(s); ok {
		rows = append(rows, p)
	}
	return rows
}
