package health

import (
	"strings"
	"testing"
	"time"
)

// H3 (holder hardening §4): a holder that does not answer is the person's,
// not sentinel's (sentinel died with it): its fail is in For you with the
// command that starts the current build's holder; its warn (holder_restarted)
// stays in hand, self-repair bringing the agents back.
func TestHolderFailIsThePersons(t *testing.T) {
	d, ok := ByID("holder")
	if !ok {
		t.Fatal("no holder check")
	}
	if got := d.Place(Fail); got != PlaceForYou {
		t.Errorf("fail placed %q", got)
	}
	if got := d.Place(Warn); got != PlaceInHand {
		t.Errorf("warn placed %q", got)
	}
	if d.Command != "agentbus --project <project> service start holder" {
		t.Errorf("command %q", d.Command)
	}
	if d.Handler == "sentinel" || strings.Contains(d.Remediation, "Sentinel starts") {
		t.Errorf("sentinel still handles a dead holder: %q / %q", d.Handler, d.Remediation)
	}
}

// H3: the warn says how many agents wait and how long self-repair's queue
// takes at its pace (one relaunch per pause, master first).
func TestHolderRestartedSaysTheQueue(t *testing.T) {
	cur := HolderBuild{Key: "k1", Raw: "v1", Current: true, Unit: true, Answers: true, Terms: 1, Since: 2 * time.Minute}
	var agents []HolderAgent
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "master"} {
		agents = append(agents, HolderAgent{Name: n, Key: "k1", Term: "t" + n, ProvedAgo: 10 * time.Minute})
	}
	e := HolderEvidence{Known: true, Builds: []HolderBuild{cur}, AgentsKnown: true, Agents: agents, RelaunchPace: 2 * time.Minute}
	got := EvaluateHolder(e)
	for _, want := range []string{"8 agent(s) lost", "8 waiting for self-repair", "one relaunch every 2m0s, master first", "the last in about 14m0s"} {
		if got.Status != Warn || !strings.Contains(got.Detail, want) {
			t.Errorf("want %q in %+v", want, got)
		}
	}
	e.Agents = agents[7:]
	if got := EvaluateHolder(e); !strings.Contains(got.Detail, "1 waiting for self-repair") || strings.Contains(got.Detail, "the last in") {
		t.Errorf("one agent: %+v", got)
	}
	e.RelaunchPace = 0 // self-repair's pace unknown: no queue claimed
	if got := EvaluateHolder(e); strings.Contains(got.Detail, "waiting for self-repair") {
		t.Errorf("no pace: %+v", got)
	}
}
