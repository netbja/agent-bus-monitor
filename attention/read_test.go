package attention

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

var _ Reader = (*bus.Bus)(nil)

// fakeReader answers each read from its fields; a name in fail makes that
// read fail.
type fakeReader struct {
	asks     []bus.Ask
	agents   map[string]bus.AgentSnapshot
	observed map[string]bus.Observation
	gates    map[string]map[string]string
	health   map[string]health.Record
	clock    time.Time
	merge    bus.MergeQueueRecord
	alive    bool
	recs     map[string]bus.ServiceRecord
	cmds     []bus.Event
	fail     map[string]bool
}

var errRead = errors.New("read failed")

func (f fakeReader) err(name string) error {
	if f.fail[name] {
		return errRead
	}
	return nil
}
func (f fakeReader) Asks(context.Context) ([]bus.Ask, error) { return f.asks, f.err("asks") }
func (f fakeReader) Agents(context.Context) (map[string]bus.AgentSnapshot, error) {
	return f.agents, f.err("agents")
}
func (f fakeReader) Observed(context.Context) (map[string]bus.Observation, error) {
	return f.observed, f.err("observed")
}
func (f fakeReader) OpenChallenges(_ context.Context, a string) (map[string]string, error) {
	return f.gates[a], f.err("gates")
}
func (f fakeReader) Health(context.Context) (map[string]health.Record, error) {
	return f.health, f.err("health")
}
func (f fakeReader) ServerTime(context.Context) (time.Time, error) { return f.clock, f.err("clock") }
func (f fakeReader) MergeQueue(context.Context) (bus.MergeQueueRecord, error) {
	return f.merge, f.err("merge-queue")
}
func (f fakeReader) DispatchAlive(context.Context) (bool, error) { return f.alive, f.err("dispatch") }
func (f fakeReader) ServiceStates(context.Context) (*bus.ServicesInstall, map[string]bus.ServiceRecord, error) {
	return nil, f.recs, f.err("services")
}
func (f fakeReader) Recent(context.Context, []string, int) ([]bus.Event, map[string]string, error) {
	return f.cmds, nil, f.err("cmd")
}

// Read fills Inputs as busmon's poll does: an agent's state and its time
// from its snapshot (none before its first status), its open ask, its
// observation for its own pid, its gates; the person's gates when the person
// is no agent; the dispatch state from the lease and the service record.
func TestReadFillsInputs(t *testing.T) {
	ask := bus.Ask{ID: "1-0", From: "coder", Title: "which?", State: "open", CreatedAt: at(1).UnixMilli(), Questions: []bus.AskQuestionState{q(false)}}
	f := fakeReader{
		asks: []bus.Ask{ask},
		agents: map[string]bus.AgentSnapshot{
			"coder": {State: "waiting", Message: "which?", TS: at(2).UnixMilli(), PID: 7},
			"fresh": {State: "idle", TS: 0},
		},
		observed: map[string]bus.Observation{"coder": {State: "blocked", PID: 7, At: at(3).UnixMilli(), Since: at(3).UnixMilli()}},
		gates:    map[string]map[string]string{"coder": {"g1": "x"}, "charlie": {"g2": "y"}},
		health:   map[string]health.Record{"stall": {ID: "stall"}},
		clock:    at(5),
		alive:    true,
	}
	in, failed := Read(context.Background(), f, ReadOpts{Person: "charlie", StuckAfter: time.Minute})
	if len(failed) != 0 {
		t.Fatalf("failed %v", failed)
	}
	c := in.Agents["coder"]
	if c.State != "waiting" || c.Message != "which?" || !c.StateAt.Equal(at(2)) || c.OpenAsk != 1 || c.StuckAfter != time.Minute ||
		c.Obs.State != "blocked" || !c.Obs.OwnPID || !reflect.DeepEqual(c.Gates, map[string]string{"g1": "x"}) {
		t.Fatalf("coder %+v", c)
	}
	if fr := in.Agents["fresh"]; fr.State != "" || !fr.StateAt.IsZero() {
		t.Fatalf("an agent with no status yet: %+v", fr)
	}
	if !reflect.DeepEqual(in.PersonGates, map[string]string{"g2": "y"}) || in.Person != "charlie" {
		t.Fatalf("person gates %v", in.PersonGates)
	}
	if len(in.Health) != 1 || !in.HealthNow.Equal(at(5)) || in.Dispatch != "up" || !in.MergeRead || in.MergeErr != "" || len(in.Asks) != 1 {
		t.Fatalf("inputs %+v", in)
	}
	// the person is also an agent: its gates ride on its own row, none apart
	f.agents["charlie"] = bus.AgentSnapshot{State: "idle", TS: at(1).UnixMilli()}
	if in, _ = Read(context.Background(), f, ReadOpts{Person: "charlie"}); in.PersonGates != nil || in.Agents["charlie"].Gates["g2"] != "y" {
		t.Fatalf("person as agent: %v %+v", in.PersonGates, in.Agents["charlie"])
	}
}

// A waiting agent answered by a later cmd entry to it is answered.
func TestReadAnsweredWait(t *testing.T) {
	f := fakeReader{
		agents: map[string]bus.AgentSnapshot{"coder": {State: "waiting", TS: at(2).UnixMilli()}},
		cmds:   []bus.Event{{ID: itoa(at(4).UnixMilli()) + "-0", Kind: "cmd", From: "charlie", Target: "coder", Type: "directive", Human: true}},
	}
	in, _ := Read(context.Background(), f, ReadOpts{})
	if !in.Agents["coder"].AnsweredAt.Equal(at(4)) {
		t.Fatalf("answered at %v", in.Agents["coder"].AnsweredAt)
	}
}

// Each failed read is named; what it fed stays empty; the merge queue says
// why it is unknown.
func TestReadFailures(t *testing.T) {
	f := fakeReader{agents: map[string]bus.AgentSnapshot{"coder": {State: "idle", TS: at(1).UnixMilli()}},
		fail: map[string]bool{"asks": true, "health": true, "merge-queue": true, "services": true, "gates": true}}
	in, failed := Read(context.Background(), f, ReadOpts{})
	for _, k := range []string{"asks", "health", "merge-queue", "dispatch", "gates"} {
		if failed[k] == nil {
			t.Errorf("%s not named failed: %v", k, failed)
		}
	}
	if in.Asks != nil || in.Health != nil || in.MergeErr == "" || in.Dispatch != "" {
		t.Fatalf("inputs %+v", in)
	}
}
