package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The entry a Coordination v1 publisher records for a fresh request. If this
// mapping stops holding, the follow-up view has silently gone blind.
func requestedEntry() bus.BoardEntry {
	return bus.BoardEntry{
		Owner: "foureyes", State: "requested", Updated: 1758142000,
		Request: &bus.RequestInfo{
			ID: "1758141663000-0", Thread: "1758141663000-0",
			From: "hermes", Target: "foureyes",
			CreatedAt: 1758141663000, ExpiresAt: 0,
			Delivery: "queued", Availability: "retained",
		},
	}
}

func TestBoardRequestsMapsEveryContractField(t *testing.T) {
	got := boardRequests(map[string]bus.BoardEntry{"task-20": requestedEntry()})
	if len(got) != 1 {
		t.Fatalf("read %d requests, want 1", len(got))
	}
	r := got[0]
	if !r.Tracked {
		t.Error("an entry carrying a request must read as tracked")
	}
	for name, ok := range map[string]bool{
		"task":         r.Task == "task-20",
		"target":       r.Target == "foureyes",
		"from":         r.From == "hermes",
		"state":        r.State == "requested",
		"thread":       r.Thread == "1758141663000-0",
		"id":           r.ID == "1758141663000-0",
		"delivery":     r.Delivery == "queued",
		"availability": r.Availability == "retained",
		"created":      r.Created.Equal(time.UnixMilli(1758141663000)),
	} {
		if !ok {
			t.Errorf("field %s did not survive the mapping: %+v", name, r)
		}
	}
	// expires_at 0 means "no deadline", not "1970".
	if !r.Expires.IsZero() {
		t.Errorf("expires_at 0 must stay the zero time, got %v", r.Expires)
	}
}

// Availability is derived by the typed reader at read time. busmon must render
// what it says and, when it says nothing, admit the gap rather than assume.
func TestAvailabilityIsRenderedFromTheTypedReader(t *testing.T) {
	now := time.Now()
	e := requestedEntry()
	e.Request.Availability = "missing"
	block := requestBlock(boardRequests(map[string]bus.BoardEntry{"task-20": e})[0], now)
	if !strings.Contains(block, "cause unknown") {
		t.Errorf("a missing body must name no cause:\n%s", block)
	}
	if strings.Contains(block, "trimmed") || strings.Contains(block, "aged out") {
		t.Errorf("a missing body must not be blamed on trimming:\n%s", block)
	}
	e.Request.Availability = ""
	block = requestBlock(boardRequests(map[string]bus.BoardEntry{"task-20": e})[0], now)
	if !strings.Contains(block, "availability unknown") {
		t.Errorf("an underived availability must read as unknown:\n%s", block)
	}
}

// The heart of the whole view: bytes written towards a subscriber are not a
// receipt, and never an acceptance.
func TestDeliveryWordsNeverClaimReceiptOrAcceptance(t *testing.T) {
	for _, d := range []string{"queued", "output_written", "uncertain", "expired", "missing", ""} {
		w := strings.ToLower(deliveryWords(d))
		for _, forbidden := range []string{"accepted", "acknowledged", "received", "read by"} {
			if strings.Contains(w, forbidden) {
				t.Errorf("deliveryWords(%q) = %q, must not claim %q", d, w, forbidden)
			}
		}
	}
	if w := deliveryWords("output_written"); !strings.Contains(w, "not proof it was read") {
		t.Errorf("output_written must carry its caveat, got %q", w)
	}
}

// A request with no deadline gets no deadline. Ever.
func TestDeadlineWordsInventNothing(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	none, _ := deadlineWords(request{}, now)
	if !strings.Contains(none, "no deadline") {
		t.Errorf("a request without expires_at must say it has no deadline, got %q", none)
	}
	soon, _ := deadlineWords(request{Expires: now.Add(18 * time.Minute)}, now)
	if !strings.Contains(soon, "18m") {
		t.Errorf("a live deadline should count down, got %q", soon)
	}
	late, tone := deadlineWords(request{Expires: now.Add(-5 * time.Minute)}, now)
	if !strings.Contains(late, "passed") || tone != "red" {
		t.Errorf("a passed deadline should say so loudly, got %q/%q", late, tone)
	}
}

// "missing" is about the body, never about the work.
func TestAvailabilityMissingNeverReadsAsDone(t *testing.T) {
	w := availabilityWords("missing")
	if !strings.Contains(w, "never means done") {
		t.Errorf("availability missing must refuse the done reading, got %q", w)
	}
	if availabilityWords("") != "availability unknown" {
		t.Errorf("an underived availability must read as unknown, got %q", availabilityWords(""))
	}
}

func TestNextActionStatesRecordedFactsOnly(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		r    request
		want string
	}{
		{"requested", request{State: "requested", Target: "foureyes"}, "no acceptance recorded"},
		{"accepted", request{State: "accepted", Target: "coder", Accepted: now.Add(-8 * time.Minute)}, "accepted 8m ago"},
		{"blocked", request{State: "blocked", Target: "coder", BlockedReason: "waiting on an API key"}, "waiting on an API key"},
		{"done", request{State: "done", Responded: now.Add(-time.Hour), ResponseID: "9-0"}, "answered 1h ago"},
	}
	for _, tc := range cases {
		got, _ := nextAction(tc.r, now)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: nextAction = %q, want it to contain %q", tc.name, got, tc.want)
		}
	}
	// A state with no recorded time must admit that rather than fill in a plausible one.
	got, _ := nextAction(request{State: "accepted", Target: "coder"}, now)
	if !strings.Contains(got, "no acceptance time was recorded") {
		t.Errorf("a missing acceptance time must be reported, got %q", got)
	}
}

// Blocked work first, then what has no acceptance recorded, oldest first — the order the
// operator has to act in.
func TestRequestsSortByWhatNeedsAttention(t *testing.T) {
	now := time.Now()
	raw := map[string]bus.BoardEntry{
		"a-done":      {Owner: "x", State: "done", Updated: 1},
		"b-accepted":  {Owner: "x", State: "accepted", Updated: 2},
		"c-requested": {Owner: "x", State: "requested", Updated: 3},
		"d-blocked":   {Owner: "x", State: "blocked", Updated: 4},
	}
	got := boardRequests(raw)
	var order []string
	for _, r := range got {
		order = append(order, r.State)
	}
	want := []string{"blocked", "requested", "accepted", "done"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v (now=%v)", order, want, now)
		}
	}
}

func TestWaitingLineCountsOnlyUnfinishedWork(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	reqs := []request{
		{State: "requested", Created: now.Add(-42 * time.Minute), Tracked: true},
		{State: "done", Created: now.Add(-3 * time.Hour), Tracked: true},
	}
	got := waitingLine(reqs, nil, now)
	if !strings.Contains(got, "1 request waiting") || !strings.Contains(got, "42m") {
		t.Errorf("waitingLine = %q, want one request waiting for 42m", got)
	}
	if waitingLine([]request{{State: "done"}}, nil, now) != "" {
		t.Error("nothing waiting must render nothing at all")
	}
}

// A directive that has been answered is not waiting; one that has not is —
// and the claim is only ever about the retained history.
func TestOpenThreadsExcludeAnsweredExchanges(t *testing.T) {
	cmds := []bus.Event{
		{ID: "100-0", Kind: "cmd", Type: bus.CmdDirective, From: "hermes", Target: "coder", Message: "rebase"},
		{ID: "150-0", Kind: "cmd", Type: bus.CmdReply, Ref: "100-0", From: "coder", Target: "hermes", Message: "done"},
		{ID: "200-0", Kind: "cmd", Type: bus.CmdChallenge, From: "foureyes", Target: "coder", Message: "explain this diff"},
	}
	got := openThreads(cmds, nil)
	if len(got) != 1 {
		t.Fatalf("got %d open threads, want only the unanswered challenge: %+v", len(got), got)
	}
	if got[0].ID != "200-0" {
		t.Errorf("open thread = %q, want the unanswered 200-0", got[0].ID)
	}
}

func TestRequestsPanelSeparatesTrackedFromUntracked(t *testing.T) {
	now := time.Now()
	reqs := boardRequests(map[string]bus.BoardEntry{"task-20": requestedEntry()})
	threads := openThreads([]bus.Event{
		{ID: "200-0", Kind: "cmd", Type: bus.CmdDirective, From: "hermes", Target: "coder", Message: "look at the flake"},
	}, reqs)
	panel := requestsPanel(reqs, threads, now, 80)
	for _, want := range []string{
		"tracked requests",
		"task-20",
		"no acceptance recorded",
		"no answer in the retained history",
		"records no acceptance",
		"look at the flake",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel missing %q:\n%s", want, panel)
		}
	}
}

// The empty case has to teach, not just say "0": an operator staring at an
// empty follow-up view needs to know a cmd directive is not a tracked request.
func TestRequestsPanelExplainsAnEmptyBoard(t *testing.T) {
	panel := requestsPanel(nil, nil, time.Now(), 80)
	if !strings.Contains(panel, "agentbus request send") {
		t.Errorf("the empty view should say how a tracked request is created:\n%s", panel)
	}
}
