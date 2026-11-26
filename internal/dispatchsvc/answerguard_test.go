package dispatchsvc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
)

// personNotices are the notify entries for the person from the dispatcher
// that open with prefix.
func personNotices(t *testing.T, b *bus.Bus, prefix string) []string {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{"notify"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.From == "dispatch" && strings.HasPrefix(e.Message, prefix) {
			out = append(out, e.Message)
		}
	}
	return out
}

// board-drift §8 against Redis: the person's typed directive, unanswered,
// wakes its agent once and, past twice answer_after, tells the person once;
// a lost lease writes nothing; an answer clears the check.
func TestAnswerGuardWritesOnce(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	w.env[config.EnvName("identity.as")] = "charlie"
	w.s.now = func() time.Time { return time.Now().Add(25 * time.Minute) }
	if _, err := w.b.TypedCmd(ctx, "charlie", "coder", "please rebase"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.HumanCmd(ctx, "charlie", "master", "composed in the person's name"); err != nil { // not typed: not judged
		t.Fatal(err)
	}
	out := w.run(w.s.personUnanswered)
	if out.Status != health.Warn || !strings.Contains(out.Detail, "coder: the person's directive") || strings.Contains(out.Detail, "master") {
		t.Fatalf("person-unanswered: %+v", out)
	}
	if err := w.s.stallWake(ctx, "not-the-holder", "person-unanswered"); err == nil {
		t.Fatal("a dispatcher without the lease wrote")
	}
	for i := 0; i < 2; i++ {
		if err := w.s.stallWake(ctx, "stall-holder", "person-unanswered"); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.DispatchDirectives("coder"); len(got) != 1 || !strings.HasPrefix(got[0], "Answer guard: the person's directive") || !strings.Contains(got[0], "agentbus report coder --to charlie") {
		t.Fatalf("coder woken %q", got)
	}
	if got := personNotices(t, w.b, "Answer guard: "); len(got) != 1 || !strings.HasPrefix(got[0], "Answer guard: coder has not answered your directive") || !strings.Contains(got[0], `("please rebase")`) {
		t.Fatalf("the person told %q", got)
	}
	if _, err := w.b.Cmd(ctx, "coder", "charlie", bus.CmdReply, "", "rebased"); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.personUnanswered); out.Status != health.OK {
		t.Fatalf("answered: %+v", out)
	}
}

// Off (answer_after = 0) or no person recorded: never judged, nothing written.
func TestAnswerGuardOff(t *testing.T) {
	w := newStallWorld(t)
	ctx := context.Background()
	w.s.now = func() time.Time { return time.Now().Add(25 * time.Minute) }
	if _, err := w.b.TypedCmd(ctx, "charlie", "coder", "please rebase"); err != nil {
		t.Fatal(err)
	}
	if out := w.run(w.s.personUnanswered); out.Status != health.Unknown {
		t.Fatalf("no person: %+v", out)
	}
	w.env[config.EnvName("identity.as")] = "charlie"
	w.env[config.EnvName("dispatch.answer_after")] = "0"
	if out := w.run(w.s.personUnanswered); out.Status != health.Unknown || out.Detail != "off (answer_after = 0)" {
		t.Fatalf("off: %+v", out)
	}
	if err := w.s.stallWake(ctx, "stall-holder", "person-unanswered"); err != nil {
		t.Fatal(err)
	}
	if got := w.DispatchDirectives("coder"); len(got) != 0 {
		t.Fatalf("woken while off %q", got)
	}
}

// §8, one ring: the answer guard's notice is one blocked event; the check's
// own warn is none (no Person statuses, no Reasons: In hand).
func TestNotifyEventsAnswerGuard(t *testing.T) {
	warn := map[string]health.Record{"person-unanswered": {ID: "person-unanswered", Status: health.Warn, Since: nfMS(-time.Hour), ObservedAt: nfMS(0)}}
	if evs := notifyEvents(notifyFacts{Health: warn}, nil, nfNow); len(evs) != 0 {
		t.Fatalf("the warn rang: %v", evs)
	}
	notice := []bus.RawEntry{{ID: "1793710000000-0", Fields: map[string]string{"from": "dispatch", "for": "person",
		"message": `Answer guard: coder has not answered your directive 1793700000000-0 ("x") for 20m (idle). Look at its pane in busmon.`}}}
	if evs := notifyEvents(notifyFacts{Health: warn, Notices: notice}, nil, nfNow); len(evs) != 1 || evs[0].Kind != "blocked" {
		t.Fatalf("the notice: %v", evs)
	}
}
