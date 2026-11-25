package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// wipRequests: coder holds two accepted (one accepted 3h ago, past a 1h
// no-reply), one in review, one blocked, one queued; coder2 holds one; an
// untracked board task naming coder is not a request.
func wipRequests(now time.Time) []request {
	tr := func(task, target, state string, accepted time.Duration) request {
		r := request{Task: task, Owner: target, Target: target, State: state, Tracked: true, Created: now.Add(-4 * time.Hour)}
		if accepted > 0 {
			r.Accepted = now.Add(-accepted)
		}
		return r
	}
	return []request{
		tr("parser", "coder", "accepted", 3*time.Hour), tr("lexer", "coder", "accepted", 20*time.Minute),
		tr("pr-42", "coder", "review", time.Hour), tr("held", "coder", "blocked", time.Hour), tr("next-one", "coder", "requested", 0),
		tr("docs-fix", "coder2", "accepted", time.Hour), tr("for-coder2", "coder2", "requested", 0),
		{Task: "plain", Owner: "coder", State: "accepted"},
	}
}

func wipAgents() map[string]*agentState {
	return map[string]*agentState{"coder": {state: "working"}, "coder2": {state: "idle"}, "docs": {state: "idle"}}
}

// In flight = tracked, accepted, this agent the target (§3.1): review,
// blocked, queued, another agent's and an untracked task are not counted;
// the oldest accept first.
func TestStampWipCountsAcceptedOnly(t *testing.T) {
	now := time.Now()
	ag := wipAgents()
	stampWip(ag, wipRequests(now), 2, time.Hour)
	c := ag["coder"].wip
	if len(c.inFlight) != 2 || c.inFlight[0].Task != "parser" || c.inFlight[1].Task != "lexer" || !c.atLimit() || c.noReply != time.Hour {
		t.Fatalf("coder: %+v", c)
	}
	if w := ag["coder2"].wip; len(w.inFlight) != 1 || w.atLimit() {
		t.Fatalf("coder2: %+v", w)
	}
	if w := ag["docs"].wip; len(w.inFlight) != 0 || w.max != 2 {
		t.Fatalf("docs: %+v", w)
	}
	stampWip(ag, wipRequests(now), 0, 0)
	if ag["coder"].wip.atLimit() {
		t.Error("0 is no limit")
	}
}

// The agents row: wip n/m beside the state; at the limit in the attention
// role; nothing at limit 0, nothing with none in flight.
func TestWipFlagOnTheAgentsRow(t *testing.T) {
	now := time.Now()
	ag := wipAgents()
	stampWip(ag, wipRequests(now), 2, 0)
	flag := func(a *agentState) (agentFlag, bool) {
		for _, f := range agentFlags(a, false) {
			if strings.HasPrefix(f.Word, "wip ") {
				return f, true
			}
		}
		return agentFlag{}, false
	}
	if f, ok := flag(ag["coder"]); !ok || f.Word != "wip 2/2" || f.Role != RoleAttention {
		t.Errorf("coder at the limit: %+v %v", f, ok)
	}
	if f, ok := flag(ag["coder2"]); !ok || f.Word != "wip 1/2" || f.Role != RoleMuted {
		t.Errorf("coder2 under it: %+v %v", f, ok)
	}
	if _, ok := flag(ag["docs"]); ok {
		t.Error("docs holds nothing: no flag")
	}
	if row := agentRow("", "coder", ag["coder"], false, TierWide, 160, now).text(); !strings.Contains(row, "wip 2/2") {
		t.Errorf("row: %q", row)
	}
	stampWip(ag, wipRequests(now), 0, 0)
	if _, ok := flag(ag["coder"]); ok {
		t.Error("limit 0: no flag")
	}
}

// The agent's page lists its in-flight requests with their age; one past
// board.no_reply_after is marked as the Board marks it.
func TestAgentPageListsInFlight(t *testing.T) {
	now := time.Now()
	ag := wipAgents()
	stampWip(ag, wipRequests(now), 2, time.Hour)
	_, lines := agentDetail("coder", ag["coder"], false, nil, nil, now)
	got := strings.Join(histTexts(lines), "\n")
	for _, w := range []string{"In flight", "2 of 2: at the wip limit", "parser " + GlyphNone + " accepted 3h ago " + GlyphWarn + " no reply 3h since accept", "lexer " + GlyphNone + " accepted 20m ago"} {
		if !strings.Contains(got, w) {
			t.Errorf("lacks %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "lexer "+GlyphNone+" accepted 20m ago "+GlyphWarn) || strings.Contains(got, "pr-42") {
		t.Errorf("a recent accept marked, or a review counted:\n%s", got)
	}
	_, lines = agentDetail("coder2", ag["coder2"], false, nil, nil, now)
	if got := strings.Join(histTexts(lines), "\n"); !strings.Contains(got, "1 of 2 (board.wip_limit)") {
		t.Errorf("coder2:\n%s", got)
	}
	stampWip(ag, nil, 0, 0)
	if _, lines = agentDetail("docs", ag["docs"], false, nil, nil, now); strings.Contains(strings.Join(histTexts(lines), "\n"), "In flight") {
		t.Error("no limit, nothing held: no In flight row")
	}
}

// A queued request whose target is at its limit reads waits: on the Board
// and in the Requests view; one to an agent under it does not.
func TestWaitsTag(t *testing.T) {
	now := time.Now()
	ag := wipAgents()
	reqs := wipRequests(now)
	stampWip(ag, reqs, 2, 0)
	board := map[string]bus.BoardEntry{}
	for _, r := range reqs {
		if r.Tracked {
			q := &bus.RequestInfo{Target: r.Target, CreatedAt: r.Created.UnixMilli()}
			if !r.Accepted.IsZero() {
				q.AcceptedAt = r.Accepted.UnixMilli()
			}
			board[r.Task] = bus.BoardEntry{Owner: r.Owner, State: r.State, Updated: now.Unix(), Request: q}
		}
	}
	v := boardViewOf(board, now, 160, boardOpts{agents: ag})
	rows := map[string]string{}
	for _, it := range v.items {
		rows[it.Key] = it.Line.text()
	}
	if !strings.Contains(rows["next-one"], "waits: coder at wip limit") {
		t.Errorf("next-one: %q", rows["next-one"])
	}
	for _, k := range []string{"for-coder2", "parser", "pr-42"} {
		if strings.Contains(rows[k], "waits:") {
			t.Errorf("%s: %q", k, rows[k])
		}
	}
	s := snapshot{agents: ag, requests: reqs}
	in := requestInputOf(s, "charlie", now)
	if in.waits["next-one"] != "waits: coder at wip limit" || in.waits["for-coder2"] != "" || len(in.waits) != 1 {
		t.Errorf("requests input: %v", in.waits)
	}
}

// The Shift+C save: the rows follow at once, and the flash says when the
// bus applies it.
func TestWipLimitSaveFlash(t *testing.T) {
	now := time.Now()
	for _, c := range []struct{ file, flash string }{
		{"[board]\nwip_limit = 3\n", "wip limit 3: applies at each agent's next accept"},
		{"[board]\nwip_limit = 0\n", "no wip limit: applies at each agent's next accept"},
		{"[board]\nwip_limit = 3\n[project.demo.board]\nwip_limit = 1\n", "wip limit 1: applies at each agent's next accept"},
	} {
		ag := wipAgents()
		m := &Model{cfg: shellConfig{project: "demo", getenv: settingsDir(t, c.file, "", nil)}, snap: snapshot{agents: ag, requests: wipRequests(now)}}
		if w := m.applySettings("board.wip_limit"); w != c.flash {
			t.Errorf("%q: flash %q", c.file, w)
		}
		if want := fmt.Sprintf("%d", m.cfg.settings.WipLimit); ag["coder"].wip.max != m.cfg.settings.WipLimit {
			t.Errorf("%q: rows not restamped (max %d, want %s)", c.file, ag["coder"].wip.max, want)
		}
	}
}

// A poll stamps the agents from the board and the project's limit: the
// Agents panel shows wip 2/2 (attention) and wip 1/2, the Board waits:.
// Goldens at the usual widths; none at limit 0 (the fixture's goldens).
func TestWipOnTheShell(t *testing.T) {
	pinLocal(t)
	for _, sz := range [][2]int{{160, 40}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			withPalette(t, "dark", false, 1<<24)
			m := fixtureShell(t, sz[0], sz[1])
			m.cfg.settings.WipLimit = 2
			snap := shellFixture(shellNow)
			for _, a := range snap.agents {
				a.judgedAt = shellNow
			}
			snap.requests = wipRequests(shellNow)
			snap.agents["coder2"] = &agentState{state: "idle", message: "on docs-fix", stateAt: shellNow.Add(-time.Minute), lastSeen: shellNow.Add(-time.Minute), registered: true, judgedAt: shellNow}
			m = step(t, m, pollMsg{snap: snap, at: shellNow})
			if w := m.snap.agents["coder"].wip; len(w.inFlight) != 2 || !w.atLimit() {
				t.Fatalf("the poll did not stamp coder: %+v", w)
			}
			m.paint = newPainter(th)
			checkGolden(t, fmt.Sprintf("wip-%dx%d.txt", sz[0], sz[1]), m.grid().plain()+"\n")
		})
	}
}
