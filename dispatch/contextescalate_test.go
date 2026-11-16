package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/quota"
)

// escalateWorld is a dispatcher with the lease, the context ladder on
// (alert 80, urgent 90, deadline 20m), and coder's fill set by the test.
type escalateWorld struct {
	t    *testing.T
	b    *bus.Bus
	r    *redis.Client
	ctx  context.Context
	l    *loop
	desk *fakeDesktop
	mu   sync.Mutex
	pct  map[string]float64
}

func newEscalateWorld(t *testing.T, urgent float64, deadline time.Duration) *escalateWorld {
	t.Helper()
	b, r := dialRun(t)
	ctx := context.Background()
	if ok, err := b.AcquireDispatch(ctx, "esc", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	w := &escalateWorld{t: t, b: b, r: r, ctx: ctx, desk: &fakeDesktop{}, pct: map[string]float64{}}
	read := func(s string) (quota.ContextReading, bool) {
		w.mu.Lock()
		defer w.mu.Unlock()
		p, ok := w.pct[s]
		return quota.ContextReading{Pct: p, ObservedAt: time.Now().UnixMilli(), Session: s}, ok
	}
	w.l = &loop{b: b, token: "esc", last: map[string]string{}, cfg: Config{EscalateTo: "master", Log: io.Discard,
		Context: ContextConfig{Read: read, Alert: 80, Urgent: urgent, Deadline: deadline, MaxAge: 30 * time.Minute, Notify: w.desk.Notify}}}
	registerSession(t, b, "coder", "p-coder", "s1")
	return w
}

func (w *escalateWorld) pass(pct float64) {
	w.t.Helper()
	w.mu.Lock()
	w.pct["s1"] = pct
	w.mu.Unlock()
	snaps := map[string]bus.AgentSnapshot{"coder": {PID: testPID, PIDStart: testStart, IdentSession: "s1"}}
	if err := w.l.contextPass(w.ctx, snaps); err != nil {
		w.t.Fatal(err)
	}
	w.l.contextAt = time.Time{} // every pass publishes
	if err := w.l.contextAlerts(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

// count is the cmd entries to target whose command contains text, by wake.
func (w *escalateWorld) count(target, text string, wake bool) int {
	n := 0
	for _, m := range entries(w.t, w.r, w.b, "cmd") {
		cmd, _ := m.Values["command"].(string)
		woke := m.Values["wake"] != "no"
		if m.Values["target"] == target && strings.Contains(cmd, text) && woke == wake {
			n++
		}
	}
	return n
}

func (w *escalateWorld) personNotices(text string) int {
	n := 0
	for _, m := range entries(w.t, w.r, w.b, "notify") {
		msg, _ := m.Values["message"].(string)
		if m.Values["for"] == "person" && strings.Contains(msg, text) {
			n++
		}
	}
	return n
}

// ageAlert moves the alert's time (context:coder:at) back by d.
func (w *escalateWorld) ageAlert(d time.Duration) {
	w.t.Helper()
	marks, _ := w.b.QuotaMarks(w.ctx)
	at := bus.MarkAt(marks, "context:coder")
	if at == 0 {
		w.t.Fatal("no alert time recorded")
	}
	id := strconv.FormatInt(at-d.Milliseconds(), 10) + "-0"
	if err := w.r.HSet(w.ctx, bus.QuotaKey(w.b.Project()), "context:coder"+bus.MarkAtSuffix, id).Err(); err != nil {
		w.t.Fatal(err)
	}
}

// context-escalation §3.1, §3.2: the alert keeps its time; at
// context_urgent master gets one directive that wakes it, the person one
// notice and one desktop notification; nothing repeats in the episode.
func TestContextEscalatesAtUrgent(t *testing.T) {
	w := newEscalateWorld(t, 90, 20*time.Minute)
	w.pass(85)
	if marks, _ := w.b.QuotaMarks(w.ctx); bus.MarkAt(marks, "context:coder") == 0 {
		t.Fatal("the alert kept no time")
	}
	if w.count("master", "no hand-off declared", true) != 0 {
		t.Fatal("escalated at the alert")
	}
	w.pass(91)
	w.pass(93)
	if n := w.count("master", "context: coder at 91%, alerted", true); n != 1 {
		t.Fatalf("%d waking directives to master, want 1", n)
	}
	if n := w.personNotices("coder context 91%, no hand-off"); n != 1 {
		t.Fatalf("%d person notices, want 1", n)
	}
	if s := w.desk.Sent(); len(s) != 2 {
		t.Fatalf("desktop %q, want the alert's and the escalation's", s)
	}
}

// §3.2: the deadline escalates below context_urgent: the 2026-11-15 case,
// 80% at 21:30, 87% at 21:50.
func TestContextEscalatesAtTheDeadline(t *testing.T) {
	w := newEscalateWorld(t, 90, 20*time.Minute)
	w.pass(80)
	w.ageAlert(19 * time.Minute)
	w.pass(86)
	if w.count("master", "no hand-off declared", true) != 0 {
		t.Fatal("escalated before the deadline")
	}
	w.ageAlert(2 * time.Minute) // 21 min since the alert
	w.pass(87)
	if n := w.count("master", "context: coder at 87%, alerted 21 min ago at 80%", true); n != 1 {
		t.Fatalf("%d deadline escalations, want 1", n)
	}
}

// §2: a hand-off standing cancels the escalation; a void one (the agent
// working since) does not.
func TestContextHandoffStandsCancelsTheEscalation(t *testing.T) {
	w := newEscalateWorld(t, 90, 20*time.Minute)
	w.pass(85)
	h, _ := json.Marshal(bus.Handoff{Report: "1-1", Session: "s1", PID: testPID, PIDStart: testStart, At: time.Now().UnixMilli() + 60000})
	if err := w.r.HSet(w.ctx, bus.HandoffKey(w.b.Project()), "coder", string(h)).Err(); err != nil {
		t.Fatal(err)
	}
	w.pass(95)
	if w.count("master", "no hand-off declared", true) != 0 || w.personNotices("no hand-off") != 0 {
		t.Fatal("escalated though a hand-off stands")
	}
	h, _ = json.Marshal(bus.Handoff{Report: "1-1", Session: "s1", PID: testPID, PIDStart: testStart, At: 1})
	w.r.HSet(w.ctx, bus.HandoffKey(w.b.Project()), "coder", string(h))
	if _, err := w.b.Status(w.ctx, "coder", "working", "", bus.AgentIdent{Pane: "p-coder", PID: testPID, PIDStart: testStart, Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	w.pass(95)
	if n := w.count("master", "no hand-off declared", true); n != 1 {
		t.Fatalf("a void hand-off: %d escalations, want 1", n)
	}
}

// §3.3: the end of an episode deletes every mark, so the next rise alerts
// and escalates again; urgent at or below the alert is off (the deadline
// still applies); 0 deadline is off.
func TestContextEscalationEpisodeAndSettings(t *testing.T) {
	w := newEscalateWorld(t, 90, 20*time.Minute)
	w.pass(85)
	w.pass(92)
	w.pass(50) // the episode ends
	marks, _ := w.b.QuotaMarks(w.ctx)
	for k := range marks {
		if strings.Contains(k, "coder") {
			t.Fatalf("mark %q kept after the episode", k)
		}
	}
	w.pass(85)
	w.pass(92)
	if n := w.count("master", "no hand-off declared", true); n != 2 {
		t.Fatalf("%d escalations over two episodes, want 2", n)
	}

	off := newEscalateWorld(t, 80, 0) // urgent not above the alert: off; no deadline
	off.pass(85)
	off.pass(99)
	if off.count("master", "no hand-off declared", true) != 0 {
		t.Fatal("escalated with urgent off and no deadline")
	}
}

// §3.2: the escalation target alerted about itself gets no directive about
// itself; the person is still told.
func TestContextEscalationOfTheTarget(t *testing.T) {
	w := newEscalateWorld(t, 90, 20*time.Minute)
	w.l.cfg.EscalateTo = "coder"
	w.pass(85)
	w.pass(95)
	if w.count("coder", "no hand-off declared", true) != 0 {
		t.Fatal("the target was told about itself")
	}
	if n := w.personNotices("coder context 95%"); n != 1 {
		t.Fatalf("%d person notices, want 1", n)
	}
}

// context-escalation §3.5: an episode ended in the same session by an
// automatic compaction after the alert, no hand-off standing: one wake=no
// notice to master; no boundary (a window change), a hand-off standing, or
// a new session: none.
func TestContextCompactedNotice(t *testing.T) {
	w := newEscalateWorld(t, 90, 20*time.Minute)
	var compacted bool
	var asked []int64
	w.l.cfg.Context.Compacted = func(_, session string, afterMs int64) (int64, bool) {
		asked = append(asked, afterMs)
		return 412345, compacted && session == "s1" && afterMs > 0
	}
	notices := func() int { return w.count("master", "was compacted by its client at 412345 tokens", false) }

	w.pass(85)
	w.pass(40) // no boundary: a window change, silent
	if notices() != 0 || len(asked) != 1 || asked[0] == 0 {
		t.Fatalf("no boundary: %d notices, asked %v", notices(), asked)
	}
	compacted = true
	w.pass(85)
	w.pass(30)
	w.pass(20) // the episode is over: nothing asked again
	if n := notices(); n != 1 {
		t.Fatalf("%d compaction notices, want 1", n)
	}
	if marks, _ := w.b.QuotaMarks(w.ctx); marks["context-compacted:coder"] == "" {
		t.Fatal("the notice's mark is not kept")
	}
	// a hand-off standing: the episode ends as planned, no notice
	w.pass(85)
	h, _ := json.Marshal(bus.Handoff{Report: "1-1", Session: "s1", PID: testPID, PIDStart: testStart, At: time.Now().UnixMilli() + 60000})
	w.r.HSet(w.ctx, bus.HandoffKey(w.b.Project()), "coder", string(h))
	w.pass(30)
	if n := notices(); n != 1 {
		t.Fatalf("a hand-off stood: %d notices, want 1", n)
	}
}
