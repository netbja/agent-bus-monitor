package dispatchsvc

import (
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// rebootWorld is the act world on boot B1 with {p}:reboot as rec (stored
// under the lease, so the chore's fenced delete is the real one).
func rebootWorld(t *testing.T, rec *bus.RebootRecord) *actWorld {
	t.Helper()
	w := newActWorld(t)
	ctx := context.Background()
	if rec != nil {
		if err := w.b.FencedPutReboot(ctx, "tok", *rec); err != nil {
			t.Fatal(err)
		}
	}
	read := w.c.read
	w.c.read = func(ctx context.Context) (selfRepairReads, error) {
		r, err := read(ctx)
		if err != nil {
			return r, err
		}
		r.Boot = "B1"
		if got, ok, err := w.b.Reboot(ctx); err != nil {
			return r, err
		} else if ok {
			r.Reboot = &got
		}
		return r, nil
	}
	return w
}

// reboot-resume §5: this boot's record with any verdict but restarting
// holds every relaunch, its kind and reason said; nothing is relaunched.
func TestSelfRepairHoldsAfterAReboot(t *testing.T) {
	for verdict, want := range map[string]string{
		bus.RebootIntentional: "clean restart", bus.RebootSuspended: "suspended",
		bus.RebootUnplanned: "not restarted (holder not answering)", bus.RebootStopped: "stopped",
	} {
		rec := &bus.RebootRecord{Boot: "B1", Verdict: verdict}
		if verdict == bus.RebootUnplanned {
			rec.Why = "holder not answering"
		}
		w := rebootWorld(t, rec)
		w.passes(4)
		if len(w.relaunch) != 0 {
			t.Fatalf("%s: relaunched %q", verdict, w.relaunch)
		}
		out, _ := w.c.check(context.Background())
		if !strings.Contains(out.Detail, want) {
			t.Fatalf("%s: check %+v", verdict, out)
		}
		if _, ok, _ := w.b.Reboot(context.Background()); !ok {
			t.Fatalf("%s: the record went while the team is dead", verdict)
		}
	}
}

// reboot-resume §3.1: restarting relaunches the dead agents, each a reboot
// death, never counted in its own budget (R5); once every required agent
// is alive the record is deleted, under the lease.
func TestSelfRepairRestartsAfterAnUnplannedReboot(t *testing.T) {
	w := rebootWorld(t, &bus.RebootRecord{Boot: "B1", Verdict: bus.RebootRestarting, N: 1, Max: 3, Window: 86400000})
	w.passes(4)
	if len(w.relaunch) != 1 || !strings.HasPrefix(w.relaunch[0], "coder ") {
		t.Fatalf("relaunched %q", w.relaunch)
	}
	past, _ := w.b.SelfRepairPast(context.Background())
	if c := past["coder"]; len(c.Attempts) != 0 || len(c.RebootAttempts) != 1 {
		t.Fatalf("attempts %+v: a reboot death counted in the role's budget", c)
	}
	w.passes(1)
	if _, ok, _ := w.b.Reboot(context.Background()); ok {
		t.Fatal("the record outlived the team's return")
	}
}

// A record of another boot holds nothing and is deleted.
func TestSelfRepairDropsAnotherBootsRecord(t *testing.T) {
	w := rebootWorld(t, &bus.RebootRecord{Boot: "B0", Verdict: bus.RebootIntentional})
	w.passes(4)
	if len(w.relaunch) != 1 {
		t.Fatalf("relaunched %q", w.relaunch)
	}
	if past, _ := w.b.SelfRepairPast(context.Background()); len(past["coder"].Attempts) != 1 {
		t.Fatalf("an ordinary death not counted: %+v", past["coder"])
	}
	if _, ok, _ := w.b.Reboot(context.Background()); ok {
		t.Fatal("another boot's record kept")
	}
}
