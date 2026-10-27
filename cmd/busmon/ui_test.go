package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// fakeWriter records sends; Thread blocks until release is closed (when set).
type fakeWriter struct {
	mu        sync.Mutex
	sent      []string
	threads   int
	release   chan struct{}
	fail      error           // returned by Cmd and Notify when set
	left      int             // questions left after an Answer
	answerID  string          // the ask id Answer reports (default: the one given)
	asks      []bus.Ask       // what Asks returns
	failFor   map[string]bool // HumanCmd fails for these targets
	resumeErr error           // returned by Resume when set
}

func (f *fakeWriter) Resume(_ context.Context, by, message string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "resume "+by)
	return f.resumeErr == nil, f.resumeErr
}

func (f *fakeWriter) HumanCmd(_ context.Context, from, target, command string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "cmd "+target+" "+command)
	if f.failFor[target] {
		return "", errors.New("refused")
	}
	return "1-0", f.fail
}

func (f *fakeWriter) HumanNotify(_ context.Context, from, message string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "notify "+message)
	return "1-0", f.fail
}

func (f *fakeWriter) HumanClose(_ context.Context, from, target, ref, message string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "close "+target+" "+ref)
	return "1-0", f.fail
}

func (f *fakeWriter) ForgetAgent(_ context.Context, agent string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "forget "+agent)
	return true, f.fail
}

func (f *fakeWriter) Thread(_ context.Context, id string) ([]bus.Event, error) {
	f.mu.Lock()
	f.threads++
	rel := f.release
	f.mu.Unlock()
	if rel != nil {
		<-rel
	}
	return []bus.Event{{ID: id, Kind: "cmd", Type: "directive", From: "master", Target: "codex", Message: "THREADROOT"}}, nil
}

func demoShared(now time.Time) *shared {
	return &shared{
		agents: map[string]*agentState{
			"coder": {state: "working", message: "refactoring the parser",
				stateAt: now.Add(-18 * time.Minute), lastSeen: now.Add(-18 * time.Minute), registered: true},
			"foureyes": {state: "blocked", message: "waiting for the API key", stateAt: now.Add(-30 * time.Second), gated: 1, registered: true},
			"sentinel": {lastSeen: now.Add(-90 * time.Second), registered: true},
		},
		budgets: map[string]bus.BudgetSnapshot{"anthropic": {SessionPct: 25, WeeklyPct: 44}},
		board:   map[string]bus.BoardEntry{"ui-theme": {Owner: "coder", State: "working", Updated: now.Unix()}},
		health:  health{ok: true, since: now},
		pilot:   "master",
	}
}

func feedEvents() []bus.Event {
	return []bus.Event{
		{ID: "1791649424000-0", Kind: "status", Agent: "coder", State: "working", Message: "parser"},
		{ID: "1791649425000-0", Kind: "cmd", Type: "directive", From: "master", Target: "codex", Message: "review render.go"},
		{ID: "1791649426000-0", Kind: "report", Agent: "kimi", RKind: "note", Message: "tests green"},
	}
}

// A silent agent keeps the state it declared; silence is shown as an age.
func TestScreenSilentAgentKeepsItsDeclaredState(t *testing.T) {
	screen := drawShell(t, demoShared(time.Now()), 160, 40)
	for _, want := range []string{"coder", "working", "18m", "foureyes", "blocked", "gate 1", "sentinel"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "offline") {
		t.Errorf("silence rendered as a guess:\n%s", screen)
	}
	// A report-only agent declared nothing: "—", never an invented state.
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "sentinel") && !strings.Contains(line, GlyphDash) {
			t.Errorf("sentinel row should show no declared state: %q", line)
		}
	}
}

func TestScreenReportsTheMonitorsOwnConnection(t *testing.T) {
	now := time.Now()
	st := demoShared(now)
	st.health = health{ok: false, since: now.Add(-12 * time.Second), err: "connection refused"}
	if screen := drawShell(t, st, 160, 40); !strings.Contains(screen, GlyphBlocked+" bus down 12s") {
		t.Errorf("a broken monitor link must be named:\n%s", screen)
	}
	if screen := drawShell(t, st, 64, 22); !strings.Contains(screen, GlyphBlocked+" bus 12s") {
		t.Errorf("narrow header must keep health:\n%s", screen)
	}
}

// fakeReader counts every read; fail makes PilotDriver fail (broker down).
type fakeReader struct {
	calls map[string]int
	fail  bool

	conductor  bool     // ConductorAlive
	changes    []string // ChangeNames
	svcInst    *bus.ServicesInstall
	svcRecs    map[string]bus.ServiceRecord
	snaps      map[string]bus.AgentSnapshot // Agents, when set
	pause      *bus.PauseRecord
	marks      map[string]string
	observed   map[string]bus.Observation
	swaps      map[string]bool
	fallbacks  map[string]bus.FallbackRecord
	verdicts   map[string]bus.ModelVerdict
	asks       []bus.Ask
	pauseErr   error // returned by Pause when set
	contexts   map[string]bus.ContextFill
	contextErr error                        // returned by Context when set
	gates      map[string]map[string]string // OpenChallenges by gated name, when set
}

func (f *fakeReader) n(name string) { f.calls[name]++ }
func (f *fakeReader) PilotDriver(context.Context) (string, error) {
	f.n("PilotDriver")
	if f.fail {
		return "", errors.New("connection refused")
	}
	return "master", nil
}
func (f *fakeReader) ArmedAgents(context.Context) (map[string]string, error) {
	f.n("ArmedAgents")
	return map[string]string{}, nil
}
func (f *fakeReader) CmdLag(context.Context) (map[string]int64, error) {
	f.n("CmdLag")
	return nil, nil
}
func (f *fakeReader) Agents(context.Context) (map[string]bus.AgentSnapshot, error) {
	f.n("Agents")
	if f.snaps != nil {
		return f.snaps, nil
	}
	return map[string]bus.AgentSnapshot{"coder": {State: "working", TS: 1, PID: 42}, "kimi": {State: "idle", TS: 1}}, nil
}
func (f *fakeReader) Usage(context.Context) (map[string]bus.UsageSnapshot, error) {
	f.n("Usage")
	return map[string]bus.UsageSnapshot{"coder": {Ctx: "120k ctx"}}, nil
}
func (f *fakeReader) Budgets(context.Context) (map[string]bus.BudgetSnapshot, error) {
	f.n("Budgets")
	return map[string]bus.BudgetSnapshot{"anthropic": {SessionPct: 25}}, nil
}
func (f *fakeReader) Board(context.Context) (map[string]bus.BoardEntry, error) {
	f.n("Board")
	return map[string]bus.BoardEntry{"t": {Owner: "coder", State: "working"}}, nil
}
func (f *fakeReader) Recent(context.Context, []string, int) ([]bus.Event, map[string]string, error) {
	f.n("Recent")
	return nil, nil, nil
}
func (f *fakeReader) ServiceStates(context.Context) (*bus.ServicesInstall, map[string]bus.ServiceRecord, error) {
	f.n("ServiceStates")
	return f.svcInst, f.svcRecs, nil
}
func (f *fakeReader) DispatchAlive(context.Context) (bool, error) {
	f.n("DispatchAlive")
	return true, nil
}
func (f *fakeReader) Obligation(context.Context, string, time.Duration) (bus.Obligation, error) {
	f.n("Obligation")
	return bus.Obligation{Unread: 2}, nil
}
func (f *fakeReader) Wake(context.Context, string) (bus.WakeRecord, bool, error) {
	f.n("Wake")
	return bus.WakeRecord{}, false, nil
}
func (f *fakeReader) OpenChallenges(_ context.Context, agent string) (map[string]string, error) {
	f.n("OpenChallenges")
	return f.gates[agent], nil
}

// The person's own gate is read when the person is no agent, and kept as the
// snapshot's personGates; the default identity "pilot" is never read.
func TestPollReadsThePersonsGate(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, gates: map[string]map[string]string{
		"bernard": {"1793000000001-0": "master|sign off"}, "coder": {"1793000000002-0": "master|review"}}}
	st := &shared{person: "bernard", agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}}
	pollOnce(context.Background(), f, st, 1) // the agents arrive
	pollOnce(context.Background(), f, st, 2)
	if len(st.personGates) != 1 || st.personGates["1793000000001-0"] == "" {
		t.Fatalf("personGates = %v", st.personGates)
	}
	items := forYou(takeSnapshot(st, time.Now()), nil)
	if len(items) != 1 || items[0].Agent != "bernard" || items[0].By != "master" {
		t.Fatalf("for you = %+v", items)
	}
	pilot := &shared{person: defaultHuman, agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}}
	f.gates[defaultHuman] = map[string]string{"x": "master|y"}
	pollOnce(context.Background(), f, pilot, 1)
	pollOnce(context.Background(), f, pilot, 2)
	if len(pilot.personGates) != 0 {
		t.Fatalf("the default identity's gate was read: %v", pilot.personGates)
	}
}

func TestBrokerDownKeepsValues(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	pollOnce(context.Background(), f, st, 6)
	f.fail = true
	pollOnce(context.Background(), f, st, 7)
	if st.health.ok || len(st.budgets) == 0 || len(st.board) == 0 || st.pilot != "master" {
		t.Fatalf("broker down blanked the model: %+v", st)
	}
	if c := queueCell(st.agents["coder"], TierWide); c.Text != "2?" {
		t.Fatalf("queue after failure %q, want 2?", c.Text)
	}
	if s := drawShell(t, st, 160, 45); !strings.Contains(s, "bus down") || !strings.Contains(s, "2?") {
		t.Fatalf("screen:\n%s", s)
	}
}

// A free-text ref can neither inject tview tags nor add rows (final review #3).
func TestDetailRefIsSanitised(t *testing.T) {
	now := time.Now()
	base := bus.Event{ID: "1791649884000-0", Kind: "cmd", From: "a", Target: "b", Type: "directive", Message: "m", Ref: "pr-42"}
	hostile := base
	hostile.Ref = "[red]pr-42\ntext  complete"
	clean := detailText(base, now, now, threadState{Loaded: true})
	got := detailText(hostile, now, now, threadState{Loaded: true})
	if strings.Count(plainOf(got), "\n") != strings.Count(plainOf(clean), "\n") {
		t.Fatalf("a ref newline added rows:\n%s", plainOf(got))
	}
	if !strings.Contains(drawnText(got), "[red]pr-42") {
		t.Fatalf("ref tag not escaped:\n%s", drawnText(got))
	}
}

func (f *fakeReader) ConductorAlive(context.Context) (bool, error) {
	f.n("ConductorAlive")
	return f.conductor, nil
}
func (f *fakeReader) ChangeNames(context.Context) ([]string, error) {
	f.n("ChangeNames")
	return f.changes, nil
}

func TestPollMissionsState(t *testing.T) {
	for _, c := range []struct {
		alive   bool
		changes []string
		want    string
	}{{true, nil, "up"}, {false, []string{"chg"}, "down"}, {false, nil, ""}} {
		f := &fakeReader{calls: map[string]int{}, conductor: c.alive, changes: c.changes}
		st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
		pollOnce(context.Background(), f, st, 1)
		if st.missions != c.want {
			t.Errorf("alive=%v changes=%v: missions %q, want %q", c.alive, c.changes, st.missions, c.want)
		}
	}
}

func (f *fakeReader) Pause(context.Context) (*bus.PauseRecord, error) {
	f.n("Pause")
	if f.pauseErr != nil {
		return nil, f.pauseErr
	}
	return f.pause, nil
}

func (f *fakeReader) Context(context.Context) (map[string]bus.ContextFill, error) {
	f.n("Context")
	return f.contexts, f.contextErr
}
func (f *fakeReader) Observed(context.Context) (map[string]bus.Observation, error) {
	f.n("Observed")
	return f.observed, nil
}
func (f *fakeReader) Swaps(context.Context) (map[string]bool, error) {
	f.n("Swaps")
	return f.swaps, nil
}
func (f *fakeReader) Fallbacks(context.Context) (map[string]bus.FallbackRecord, error) {
	f.n("Fallbacks")
	return f.fallbacks, nil
}
func (f *fakeReader) ModelVerdicts(context.Context) (map[string]bus.ModelVerdict, error) {
	f.n("ModelVerdicts")
	return f.verdicts, nil
}
func (f *fakeReader) QuotaMarks(context.Context) (map[string]string, error) {
	f.n("QuotaMarks")
	return f.marks, nil
}

func (f *fakeWriter) Answer(_ context.Context, askID, qid, by, choice, text string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, "answer "+askID+" "+qid+" "+choice+text)
	if f.answerID != "" {
		askID = f.answerID
	}
	return askID, f.left, f.fail
}

func (f *fakeReader) Asks(context.Context) ([]bus.Ask, error) {
	f.n("Asks")
	return f.asks, nil
}

func (f *fakeWriter) Asks(context.Context) ([]bus.Ask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asks, nil
}
