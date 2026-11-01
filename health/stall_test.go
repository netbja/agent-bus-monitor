package health

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var stallNow = time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return stallNow.Add(-d) }

// idAt is a stream id d before stallNow.
func idAt(d time.Duration, seq int) string { return fmt.Sprintf("%d-%d", ago(d).UnixMilli(), seq) }

func idleAgent(name string, since time.Duration) StallAgent {
	return StallAgent{Name: name, State: "idle", DeclaredAt: ago(since), Pane: PaneIdle}
}

func stalledTeam() TeamStallInput {
	return TeamStallInput{
		Now: stallNow, After: 15 * time.Minute,
		Agents:      []StallAgent{idleAgent("master", 20*time.Minute), idleAgent("coder", 18*time.Minute)},
		Quiet:       OK,
		PlanLoaded:  true,
		AgentReady:  []ReadyTask{{ID: "pages-5", Title: "GitHub check", Owner: "coder"}},
		PersonReady: []string{"Reinstall and tag v0.26"},
	}
}

// The spec's table (stall-guard, team-stall), row by row.
func TestTeamStallTable(t *testing.T) {
	for _, c := range []struct {
		name   string
		mod    func(*TeamStallInput)
		want   Status
		reason string
		detail string
	}{
		{"stalled, agent work ready", func(*TeamStallInput) {}, Fail, "stalled", "team idle 18m, nothing open; next ready: pages-5 (coder)"},
		{"person work only", func(in *TeamStallInput) { in.AgentReady = nil }, Warn, "waiting-person", "team idle 18m; waiting for you: Reinstall and tag v0.26"},
		{"nothing ready", func(in *TeamStallInput) { in.AgentReady, in.PersonReady = nil, nil }, OK, "", "team idle, plan has nothing ready"},
		{"no plan", func(in *TeamStallInput) { in.PlanLoaded = false }, Unknown, "", "no plan loaded: cannot tell whether work remains"},
		{"a read failed", func(in *TeamStallInput) { in.ReadErr = "agents unavailable" }, Unknown, "", "agents unavailable"},
		{"off", func(in *TeamStallInput) { in.After = 0 }, Unknown, "", "off (stall_after = 0)"},
		{"one working", func(in *TeamStallInput) { in.Agents[1].State = "working" }, OK, "", "coder working"},
		{"one waiting", func(in *TeamStallInput) { in.Agents[1].State = "waiting" }, OK, "", "coder waiting"},
		{"one blocked", func(in *TeamStallInput) { in.Agents[1].State = "blocked" }, OK, "", "coder blocked"},
		{"idle, not long enough", func(in *TeamStallInput) { in.Agents[1].DeclaredAt = ago(14 * time.Minute) }, OK, "", "coder idle only 14m"},
		{"the boundary is stalled", func(in *TeamStallInput) { in.Agents[1].DeclaredAt = ago(15 * time.Minute) }, Fail, "stalled", "team idle 15m"},
		{"done counts as idle", func(in *TeamStallInput) { in.Agents[1].State = "done" }, Fail, "stalled", ""},
		{"a busy pane", func(in *TeamStallInput) { in.Agents[1].Pane = PaneBusy }, OK, "", "coder busy (not declared)"},
		{"no fresh observation", func(in *TeamStallInput) { in.Agents[1].Pane = PaneUnseen }, Unknown, "", "cannot see coder's pane (no fresh observation)"},
		{"no identities on this host", func(in *TeamStallInput) {
			in.Agents[0].Pane, in.Agents[1].Pane, in.NoIdentities = PaneUnseen, PaneUnseen, true
		}, Unknown, "", "cannot see panes on this host: no verified identities"},
		{"a dormant agent is ignored", func(in *TeamStallInput) {
			in.Agents = append(in.Agents, StallAgent{Name: "docs", State: "working", Dormant: true})
		}, Fail, "stalled", ""},
		{"no agent", func(in *TeamStallInput) { in.Agents = nil }, Unknown, "", "no registered agent"},
		{"open work", func(in *TeamStallInput) { in.Open = TeamOpenWork{Gates: 1} }, OK, "", "work is open: 1 gate"},
		{"quiet agent suppresses", func(in *TeamStallInput) { in.Quiet, in.QuietDetail = Warn, "coder: directive from master 25m ago" }, OK, "", "quiet agent: coder: directive from master 25m ago; see quiet-agent"},
		{"quiet agent unmeasured", func(in *TeamStallInput) { in.Quiet = Unknown }, Unknown, "", "quiet-agent not measured"},
		{"quiet-agent off does not turn team-stall off (foureyes)", func(in *TeamStallInput) {
			in.Quiet, in.QuietOff = Unknown, true
		}, Fail, "stalled", ""},
		{"ready owner held", func(in *TeamStallInput) {
			in.AgentReady[0].OwnerHeld, in.AgentReady[0].Hold = true, "anthropic until 15:40"
			in.PersonReady = nil
		}, OK, "", "ready work waits on a quota hold: pages-5 (coder, anthropic until 15:40)"},
		{"ready owner held, the person has work", func(in *TeamStallInput) {
			in.AgentReady[0].OwnerHeld = true
		}, Warn, "waiting-person", "waiting for you: Reinstall and tag v0.26"},
		{"a free task beats a held one", func(in *TeamStallInput) {
			in.AgentReady = []ReadyTask{{ID: "a", Owner: "kimi", OwnerHeld: true, Hold: "moonshot"}, {ID: "b", Owner: "coder"}}
		}, Fail, "stalled", "next ready: b (coder)"},
	} {
		in := stalledTeam()
		c.mod(&in)
		out := EvaluateTeamStall(in)
		if out.Status != c.want || out.Reason != c.reason || !strings.Contains(out.Detail, c.detail) {
			t.Errorf("%s: %s %q %q, want %s %q containing %q", c.name, out.Status, out.Reason, out.Detail, c.want, c.reason, c.detail)
		}
		if !SafeText(out.Detail) {
			t.Errorf("%s: detail not safe: %q", c.name, out.Detail)
		}
	}
}

// Every kind of open work keeps the team out of a stall, and is named.
func TestTeamOpenWorkItems(t *testing.T) {
	for _, c := range []struct {
		w    TeamOpenWork
		want string
	}{
		{TeamOpenWork{Requests: 2}, "2 requests"},
		{TeamOpenWork{Accepted: 1}, "1 accepted task"},
		{TeamOpenWork{Asks: 1}, "1 ask"},
		{TeamOpenWork{Gates: 3}, "3 gates"},
		{TeamOpenWork{Paused: true}, "a pause"},
		{TeamOpenWork{Swapping: []string{"coder"}}, "a swap or reload (coder)"},
		{TeamOpenWork{Obligations: []string{"foureyes"}}, "messages waiting for foureyes"},
		{TeamOpenWork{Handoffs: []string{"coder"}}, "a hand-off (coder)"},
		{TeamOpenWork{Missions: []string{"coder"}}, "a mission attempt (coder)"},
	} {
		in := stalledTeam()
		in.Open = c.w
		if out := EvaluateTeamStall(in); out.Status != OK || !strings.Contains(out.Detail, c.want) {
			t.Errorf("%+v: %s %q, want ok naming %q", c.w, out.Status, out.Detail, c.want)
		}
	}
	if (TeamOpenWork{}).Any() {
		t.Error("empty open work reads as open")
	}
}

func clearIn() ClearStallInput {
	return ClearStallInput{Now: stallNow, After: 15 * time.Minute, AutoClear: true,
		Handoffs: []ClearHandoff{{Agent: "coder", At: ago(22 * time.Minute), Decision: "the pane is focused"}}}
}

func TestClearStall(t *testing.T) {
	for _, c := range []struct {
		name   string
		mod    func(*ClearStallInput)
		want   Status
		reason string
		detail string
	}{
		{"no hand-off", func(in *ClearStallInput) { in.Handoffs = nil }, OK, "", ""},
		{"a void hand-off", func(in *ClearStallInput) { in.Handoffs[0].Void = true }, OK, "", ""},
		{"young", func(in *ClearStallInput) { in.Handoffs[0].At = ago(6 * time.Minute) }, OK, "", "coder handed off 6m ago; clear pending"},
		{"auto-clear off", func(in *ClearStallInput) { in.AutoClear = false }, Warn, "", "coder handed off 22m ago; auto-clear is off: clear the pane by hand"},
		{"focused pane", func(*ClearStallInput) {}, Warn, "pane-focused", "coder handed off 22m ago; not cleared: the pane is focused"},
		{"another reason", func(in *ClearStallInput) { in.Handoffs[0].Decision = "it has an open ask" }, Warn, "", "not cleared: it has an open ask"},
		{"no decision yet", func(in *ClearStallInput) { in.Handoffs[0].Decision = "" }, Warn, "", "not cleared: no decision yet"},
		{"off", func(in *ClearStallInput) { in.After = 0 }, Unknown, "", "off (clear_stall_after = 0)"},
		{"a read failed", func(in *ClearStallInput) { in.ReadErr = "hand-offs unavailable" }, Unknown, "", "hand-offs unavailable"},
		{"the boundary warns", func(in *ClearStallInput) { in.Handoffs[0].At = ago(15 * time.Minute) }, Warn, "pane-focused", "15m ago"},
	} {
		in := clearIn()
		c.mod(&in)
		out := EvaluateClearStall(in)
		if out.Status != c.want || out.Reason != c.reason || !strings.Contains(out.Detail, c.detail) {
			t.Errorf("%s: %s %q %q, want %s %q containing %q", c.name, out.Status, out.Reason, out.Detail, c.want, c.reason, c.detail)
		}
	}
}

func quietIn() QuietAgentInput {
	d := idAt(25*time.Minute, 0)
	return QuietAgentInput{Now: stallNow, After: 15 * time.Minute, Master: "master",
		Cmds: []QuietCmd{{ID: d, From: "master", Target: "coder", Type: "directive"}},
		Agents: []QuietAgent{{Name: "coder", State: "idle", LastDelivered: d,
			LastWrite: idAt(40*time.Minute, 0)}}}
}

func TestQuietAgent(t *testing.T) {
	d := idAt(25*time.Minute, 0)
	for _, c := range []struct {
		name   string
		mod    func(*QuietAgentInput)
		want   Status
		detail string
	}{
		{"delivered and silent", func(*QuietAgentInput) {}, Warn, "coder: directive from master 25m ago delivered, nothing written since"},
		{"pending", func(in *QuietAgentInput) { in.Agents[0].Pending = []string{d} }, OK, ""},
		{"never read", func(in *QuietAgentInput) { in.Agents[0].LastDelivered = idAt(30*time.Minute, 0) }, OK, ""},
		{"wake=no", func(in *QuietAgentInput) { in.Cmds[0].NoWake = true }, OK, ""},
		{"a reply is not work", func(in *QuietAgentInput) { in.Cmds[0].Type = "reply" }, OK, ""},
		{"a verdict is not work", func(in *QuietAgentInput) { in.Cmds[0].Type = "verdict" }, OK, ""},
		{"an escalation is work", func(in *QuietAgentInput) { in.Cmds[0].Type, in.Cmds[0].From = "escalation", "dispatch" }, Warn, "escalation from dispatch"},
		{"from the person", func(in *QuietAgentInput) { in.Cmds[0].From, in.Cmds[0].Human = "charlie", true }, Warn, "from charlie"},
		{"from a peer agent", func(in *QuietAgentInput) { in.Cmds[0].From = "foureyes" }, OK, ""},
		{"wrote since", func(in *QuietAgentInput) { in.Agents[0].LastWrite = idAt(10*time.Minute, 0) }, OK, ""},
		{"wrote in the same millisecond", func(in *QuietAgentInput) { in.Agents[0].LastWrite = idAt(25*time.Minute, 0) }, OK, ""},
		{"working", func(in *QuietAgentInput) { in.Agents[0].State = "working" }, OK, ""},
		{"waiting still warns, its state named", func(in *QuietAgentInput) { in.Agents[0].State = "waiting" }, Warn, "(waiting)"},
		{"held", func(in *QuietAgentInput) { in.Agents[0].Held = true }, OK, ""},
		{"its pane seen busy (foureyes)", func(in *QuietAgentInput) { in.Agents[0].Pane = PaneBusy }, OK, ""},
		{"too young", func(in *QuietAgentInput) {
			y := idAt(14*time.Minute, 0)
			in.Cmds[0].ID, in.Agents[0].LastDelivered = y, y
		}, OK, ""},
		{"expired request", func(in *QuietAgentInput) { in.Cmds[0].Expired = true }, OK, ""},
		{"request unreadable", func(in *QuietAgentInput) { in.Cmds[0].RequestUnknown = true }, Unknown, "request record unreadable"},
		{"a newer reply does not hide an older directive", func(in *QuietAgentInput) {
			in.Cmds = append(in.Cmds, QuietCmd{ID: idAt(20*time.Minute, 0), From: "master", Target: "coder", Type: "reply"})
			in.Agents[0].LastDelivered = idAt(20*time.Minute, 0)
		}, Warn, "directive from master 25m ago"},
		{"a newer expired directive does not hide an older unanswered one (reviewer-codex)", func(in *QuietAgentInput) {
			n := idAt(20*time.Minute, 0)
			in.Cmds = append(in.Cmds, QuietCmd{ID: n, From: "master", Target: "coder", Type: "directive", Expired: true})
			in.Agents[0].LastDelivered = n
		}, Warn, "directive from master 25m ago"},
		{"history trimmed past a silent agent (reviewer-codex)", func(in *QuietAgentInput) {
			in.Cmds = nil // its directive is gone from the retained stream
			in.OldestRetained = idAt(30*time.Minute, 0)
			in.Agents[0].LastWrite = idAt(3*time.Hour, 0)
		}, Unknown, "coder: silent since before the retained cmd history"},
		{"history trimmed, the agent wrote within it", func(in *QuietAgentInput) {
			in.Cmds = nil
			in.OldestRetained = idAt(30*time.Minute, 0)
			in.Agents[0].LastWrite = idAt(29*time.Minute, 0)
		}, OK, ""},
		{"history trimmed, never delivered anything", func(in *QuietAgentInput) {
			in.Cmds = nil
			in.OldestRetained = idAt(30*time.Minute, 0)
			in.Agents[0].LastWrite, in.Agents[0].LastDelivered = idAt(3*time.Hour, 0), ""
		}, OK, ""},
		{"a dormant agent is not judged", func(in *QuietAgentInput) {
			in.Cmds = nil
			in.OldestRetained = idAt(30*time.Minute, 0)
			in.Agents[0].LastWrite, in.Agents[0].Dormant = idAt(30*time.Hour, 0), true
		}, OK, ""},
		{"off", func(in *QuietAgentInput) { in.After = 0 }, Unknown, "off (quiet_after = 0)"},
		{"a read failed", func(in *QuietAgentInput) { in.ReadErr = "cmd history unavailable" }, Unknown, "cmd history unavailable"},
	} {
		in := quietIn()
		c.mod(&in)
		out := EvaluateQuietAgent(in)
		if out.Status != c.want || !strings.Contains(out.Detail, c.detail) {
			t.Errorf("%s: %s %q, want %s containing %q", c.name, out.Status, out.Detail, c.want, c.detail)
		}
		if !SafeText(out.Detail) {
			t.Errorf("%s: detail not safe: %q", c.name, out.Detail)
		}
	}
}

// Stream ids order by milliseconds, then sequence; a malformed id never
// orders after a real one.
func TestStreamIDOrder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"1-0", "2-0", true}, {"2-0", "1-0", false}, {"5-1", "5-2", true}, {"5-2", "5-2", false},
		{"10-0", "9-0", false}, {"", "1-0", true}, {"x", "1-0", true},
	} {
		if got := streamIDLess(c.a, c.b); got != c.less {
			t.Errorf("streamIDLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
