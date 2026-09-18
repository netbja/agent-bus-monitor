package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The exact shape the Coordination v1 publisher writes into {p}:board. If this
// stops parsing, the follow-up view has silently gone blind.
const requestedJSON = `{"owner":"foureyes","state":"requested","updated":1758142000,` +
	`"request":{"id":"1758141663000-0","thread":"1758141663000-0","from":"hermes",` +
	`"target":"foureyes","created_at":1758141663000,"expires_at":0,"delivery":"queued"}}`

func TestBoardRequestsParsesTheContractShape(t *testing.T) {
	got := boardRequests(map[string]string{"task-20": requestedJSON})
	if len(got) != 1 {
		t.Fatalf("parsed %d requests, want 1", len(got))
	}
	r := got[0]
	if !r.Tracked {
		t.Error("an entry carrying a request object must read as tracked")
	}
	for name, check := range map[string]bool{
		"task":     r.Task == "task-20",
		"target":   r.Target == "foureyes",
		"from":     r.From == "hermes",
		"state":    r.State == "requested",
		"thread":   r.Thread == "1758141663000-0",
		"id":       r.ID == "1758141663000-0",
		"delivery": r.Delivery == "queued",
		"created":  r.Created.Equal(time.UnixMilli(1758141663000)),
	} {
		if !check {
			t.Errorf("field %s did not survive the parse: %+v", name, r)
		}
	}
	// expires_at 0 means "no deadline", not "1970".
	if !r.Expires.IsZero() {
		t.Errorf("expires_at 0 must stay the zero time, got %v", r.Expires)
	}
}

// An ordinary board task carries no request object. It records no acceptance
// and no response, so it must never be counted or listed as a pending request —
// the BOARD pane is where ownership bookkeeping belongs.
func TestPlainBoardTasksAreNeverCountedAsRequests(t *testing.T) {
	now := time.Now()
	plain := `{"owner":"coder","state":"working","branch":"coder/task-21","updated":1758142000}`
	got := boardRequests(map[string]string{"task-21": plain})
	if len(got) != 1 || got[0].Tracked {
		t.Fatalf("a plain board task must read as untracked: %+v", got)
	}
	if panel := requestsPanel(got, nil, now, 80); strings.Contains(panel, "task-21") {
		t.Errorf("an untracked board task must not be listed as a request:\n%s", panel)
	}
	if line := waitingLine(got, nil, now); line != "" {
		t.Errorf("an untracked board task must not count as waiting, got %q", line)
	}
}

// A tracked request publishes a directive into the cmd stream. Without
// de-duplication it would surface twice — once as a request, once as an
// unanswered thread — and be counted as two things waiting.
func TestOpenThreadsSkipWhatATrackedRequestAlreadyCovers(t *testing.T) {
	reqs := boardRequests(map[string]string{"task-20": requestedJSON})
	cmds := []bus.Event{
		{ID: "1758141663000-0", Kind: "cmd", Type: bus.CmdDirective, From: "hermes", Target: "foureyes", Message: "review it"},
		{ID: "1758141999000-0", Kind: "cmd", Type: bus.CmdDirective, From: "hermes", Target: "coder", Message: "something untracked"},
	}
	got := openThreads(cmds, reqs)
	if len(got) != 1 || got[0].ID != "1758141999000-0" {
		t.Fatalf("a tracked request's own cmd must not reappear as an open thread: %+v", got)
	}
}

func TestBoardRequestsSkipsCorruptEntries(t *testing.T) {
	got := boardRequests(map[string]string{"bad": "{not json", "task-20": requestedJSON})
	if len(got) != 1 || got[0].Task != "task-20" {
		t.Errorf("a corrupt board value must be skipped, not guessed at: %+v", got)
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
		{"requested", request{State: "requested", Target: "foureyes"}, "not accepted yet"},
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

// Blocked work first, then what nobody has taken, oldest first — the order the
// operator has to act in.
func TestRequestsSortByWhatNeedsAttention(t *testing.T) {
	now := time.Now()
	raw := map[string]string{
		"a-done":      `{"owner":"x","state":"done","updated":1}`,
		"b-accepted":  `{"owner":"x","state":"accepted","updated":2}`,
		"c-requested": `{"owner":"x","state":"requested","updated":3}`,
		"d-blocked":   `{"owner":"x","state":"blocked","updated":4}`,
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
	reqs := boardRequests(map[string]string{"task-20": requestedJSON})
	threads := openThreads([]bus.Event{
		{ID: "200-0", Kind: "cmd", Type: bus.CmdDirective, From: "hermes", Target: "coder", Message: "look at the flake"},
	}, reqs)
	panel := requestsPanel(reqs, threads, now, 80)
	for _, want := range []string{
		"tracked requests",
		"task-20",
		"not accepted yet",
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
