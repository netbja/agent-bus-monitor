package health

import (
	"strings"
	"testing"
	"time"
)

// stale-waiting §4: an agent whose waiting the person answered enters the
// team as idle since the answer (AnsweredWait), and the details name it.
func TestTeamStallAnsweredWait(t *testing.T) {
	answer := func(since time.Duration) StallAgent {
		return StallAgent{Name: "master", State: "idle", DeclaredAt: ago(since), Pane: PaneIdle, AnsweredWait: true}
	}
	in := stalledTeam()
	in.Agents[0] = answer(20 * time.Minute)
	out := EvaluateTeamStall(in)
	want := "master's wait answered " + ago(20*time.Minute).Format("15:04")
	if out.Status != Fail || !strings.Contains(out.Detail, want) {
		t.Fatalf("answered 20m ago: %s %q, want fail naming %q", out.Status, out.Detail, want)
	}
	in.Agents[0] = answer(5 * time.Minute) // the idle clock starts at the answer
	if out := EvaluateTeamStall(in); out.Status != OK || !strings.Contains(out.Detail, "idle only 5m") {
		t.Fatalf("answered 5m ago: %s %q, want ok", out.Status, out.Detail)
	}
	in = stalledTeam()
	in.AgentReady = nil
	in.Agents[0] = answer(20 * time.Minute)
	if out := EvaluateTeamStall(in); out.Status != Warn || !strings.Contains(out.Detail, want) {
		t.Fatalf("person's task: %s %q, want waiting-person naming the answered wait", out.Status, out.Detail)
	}
	in = stalledTeam()
	if out := EvaluateTeamStall(in); strings.Contains(out.Detail, "answered") {
		t.Fatalf("no answered wait, detail %q", out.Detail)
	}
}

func TestReviewQueueAnsweredWait(t *testing.T) {
	in := rqIdle()
	in.Agents[0].AnsweredWait = true
	in.Gates = []ReviewGate{{Reviewer: "reviewer-codex", Ref: "r1", Subject: "x", At: rqNow.Add(-40 * time.Minute), Delivered: true}}
	out := EvaluateReviewQueue(in)
	want := "master's wait answered " + rqNow.Add(-time.Hour).Format("15:04")
	if out.Status != Warn || !strings.Contains(out.Detail, want) {
		t.Fatalf("%s %q, want warn naming %q", out.Status, out.Detail, want)
	}
}
