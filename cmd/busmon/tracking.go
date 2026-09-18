package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

// request is busmon's read-only view of one tracked request on the board.
//
// It mirrors Coordination v1's RequestInfo field for field. busmon parses the
// board hash JSON itself (see boardRequests) rather than waiting for the typed
// bus.Board() reader, so the follow-up view works against a broker as soon as
// an agent starts publishing requests. The contract's snake_case names are the
// interface; when bus.BoardEntry.Request reaches this tree, boardRequests is
// the one function that changes.
type request struct {
	Task    string
	Owner   string
	Branch  string
	State   string // requested | accepted | blocked | done (board state)
	Updated time.Time

	Tracked bool // false: an ordinary board task, with no request metadata
	ID      string
	Thread  string
	From    string
	Target  string

	Created   time.Time
	Expires   time.Time // zero = no deadline. NEVER defaulted to anything.
	Accepted  time.Time // zero = the target has not explicitly accepted
	Responded time.Time // zero = no explicit completion reply

	Delivery          string // queued | uncertain | output_written | expired | missing | "" unknown
	Availability      string // retained | missing | expired | "" unknown (derived read-side)
	Attempts          int64
	DuplicatePossible bool
	BlockedReason     string
	ResponseID        string
}

// boardEntryJSON is the wire shape of one {p}:board hash value. Unknown keys are
// ignored, so an entry written by an older agent still parses.
type boardEntryJSON struct {
	Owner   string `json:"owner"`
	State   string `json:"state"`
	Branch  string `json:"branch"`
	Updated int64  `json:"updated"` // seconds
	Request *struct {
		ID                string `json:"id"`
		Thread            string `json:"thread"`
		From              string `json:"from"`
		Target            string `json:"target"`
		CreatedAt         int64  `json:"created_at"` // ms
		ExpiresAt         int64  `json:"expires_at"`
		Delivery          string `json:"delivery"`
		OutputWrittenAt   int64  `json:"output_written_at"`
		Attempts          int64  `json:"attempts"`
		DuplicatePossible bool   `json:"duplicate_possible"`
		AcceptedAt        int64  `json:"accepted_at"`
		ResponseID        string `json:"response_id"`
		RespondedAt       int64  `json:"responded_at"`
		BlockedReason     string `json:"blocked_reason"`
		Availability      string `json:"availability"`
	} `json:"request"`
}

// msTime converts a Unix-millisecond stamp, keeping 0 as the zero time so
// "absent" never renders as 1970.
func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// boardRequests turns the raw board hash into view models. A corrupt value is
// skipped rather than guessed at, like bus.Board does.
func boardRequests(raw map[string]string) []request {
	out := make([]request, 0, len(raw))
	for task, v := range raw {
		var e boardEntryJSON
		if json.Unmarshal([]byte(v), &e) != nil {
			continue
		}
		r := request{
			Task: task, Owner: e.Owner, Branch: e.Branch, State: e.State,
			Updated: time.Unix(e.Updated, 0),
		}
		if e.Request != nil {
			q := e.Request
			r.Tracked = true
			r.ID, r.Thread, r.From, r.Target = q.ID, q.Thread, q.From, q.Target
			r.Created, r.Expires = msTime(q.CreatedAt), msTime(q.ExpiresAt)
			r.Accepted, r.Responded = msTime(q.AcceptedAt), msTime(q.RespondedAt)
			r.Delivery, r.Availability = q.Delivery, q.Availability
			r.Attempts, r.DuplicatePossible = q.Attempts, q.DuplicatePossible
			r.BlockedReason, r.ResponseID = q.BlockedReason, q.ResponseID
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return requestLess(out[i], out[j]) })
	return out
}

// waiting reports whether this request still needs somebody to act. Only the
// recorded state answers that — never the agent's presence, never a delivery
// flag, never the age of the entry.
func (r request) waiting() bool { return r.State != "done" }

// trackedOnly keeps the board entries that actually carry request metadata.
// Everything downstream counts and renders requests; a plain board task has no
// acceptance, no response and no deadline to report.
func trackedOnly(reqs []request) []request {
	out := make([]request, 0, len(reqs))
	for _, r := range reqs {
		if r.Tracked {
			out = append(out, r)
		}
	}
	return out
}

// requestRank orders the follow-up view by what needs attention: blocked (an
// agent has said it cannot proceed), then requested (nobody has taken it),
// then accepted (someone is on it), then done.
func requestRank(r request) int {
	switch r.State {
	case "blocked":
		return 0
	case "requested":
		return 1
	case "done":
		return 3
	}
	return 2
}

func requestLess(a, b request) bool {
	if ra, rb := requestRank(a), requestRank(b); ra != rb {
		return ra < rb
	}
	if !a.Updated.Equal(b.Updated) {
		return a.Updated.Before(b.Updated) // oldest first: it has waited longest
	}
	return a.Task < b.Task
}

// deliveryWords spells out a delivery disposition. The wording is deliberate
// and was agreed with the protocol owner: writing bytes towards a subscriber is
// not the same as an agent reading them, and neither is acceptance.
func deliveryWords(d string) string {
	switch d {
	case "queued":
		// Deliberately about the record, not the world: a writer that crashed
		// after emitting but before recording leaves a request queued, so
		// "nothing was written" would be a stronger claim than the data
		// supports. Acceptance is a separate axis — a request can be accepted
		// and still read queued here.
		return "queued — no subscriber output recorded"
	case "output_written":
		return "written to subscriber output (not proof it was read)"
	case "uncertain":
		return "delivery uncertain — may be redelivered"
	case "expired":
		return "expired before it was delivered"
	case "missing":
		return "body missing from the cmd stream"
	case "":
		return "delivery unknown"
	}
	return d
}

// nextAction is the line that answers "so what now?". Every branch states a
// recorded fact; none of them infers acceptance, completion or a deadline.
func nextAction(r request, now time.Time) (text, tone string) {
	switch r.State {
	case "requested":
		return fmt.Sprintf("not accepted yet — %s has not taken it", r.Target), "yellow"
	case "blocked":
		reason := r.BlockedReason
		if reason == "" {
			reason = "no reason recorded"
		}
		return "blocked by " + r.Target + ": " + reason, "red"
	case "accepted":
		if r.Accepted.IsZero() {
			return "marked accepted, but no acceptance time was recorded", "yellow"
		}
		return fmt.Sprintf("accepted %s ago by %s — no response recorded yet",
			humanAge(now.Sub(r.Accepted)), r.Target), "aqua"
	case "done":
		if r.Responded.IsZero() {
			return "marked done, but no response entry was recorded", "yellow"
		}
		return fmt.Sprintf("answered %s ago (%s)", humanAge(now.Sub(r.Responded)), r.ResponseID), "green"
	}
	return "state " + r.State + " — not a tracked request state", "gray"
}

// deadlineWords renders a deadline ONLY when the request carries one. A request
// with expires_at == 0 has no deadline, and busmon says exactly that rather
// than inventing a default window.
func deadlineWords(r request, now time.Time) (text, tone string) {
	if r.Expires.IsZero() {
		return "no deadline set", "gray"
	}
	if d := r.Expires.Sub(now); d >= 0 {
		return "deadline in " + humanAge(d), "yellow"
	}
	return "deadline passed " + humanAge(now.Sub(r.Expires)) + " ago", "red"
}

// requestBlock renders one request as two lines: who owes what, then the
// evidence. Blocks rather than a table because the follow-up view has to stay
// readable in a narrow pane, where columns would collapse into noise.
func requestBlock(r request, now time.Time, width int) string {
	if width < 20 {
		width = 20
	}
	var sb strings.Builder
	head := fmt.Sprintf("%s %s %s %s",
		tag("white", clip(r.Task, 24)),
		tag("gray", "→"),
		tag("aqua", clip(target(r), 14)),
		tag(stateColor(boardStateColorKey(r.State)), r.State))
	age := "updated " + humanAge(now.Sub(r.Updated)) + " ago"
	if r.Tracked && !r.Created.IsZero() {
		age = "asked " + humanAge(now.Sub(r.Created)) + " ago"
	}
	fmt.Fprintf(&sb, "%s  %s\n", head, tag("gray", age))

	next, tone := nextAction(r, now)
	fmt.Fprintf(&sb, "   %s\n", tag(tone, clip(next, width-4)))

	facts := []string{}
	if d, _ := deadlineWords(r, now); d != "" {
		facts = append(facts, d)
	}
	facts = append(facts, deliveryWords(r.Delivery))
	if r.Attempts > 1 {
		facts = append(facts, fmt.Sprintf("%d delivery attempts", r.Attempts))
	}
	if r.DuplicatePossible {
		facts = append(facts, "may have been delivered twice")
	}
	if av := availabilityWords(r.Availability); av != "" {
		facts = append(facts, av)
	}
	fmt.Fprintf(&sb, "   %s\n", tag("gray", clip(strings.Join(facts, " · "), width-4)))
	return sb.String()
}

// availabilityWords describes whether the request body can still be read back.
// An unknown availability says so: it is derived at read time by the typed
// board reader, and busmon's own hash read cannot compute it.
func availabilityWords(a string) string {
	switch a {
	case "retained":
		return "body still on the bus"
	case "missing":
		return "body no longer on the bus (this never means done)"
	case "expired":
		return "body expired"
	case "":
		return "availability unknown"
	}
	return a
}

// boardStateColorKey maps request states onto the palette the agent chips use,
// so one colour means one thing across the whole screen.
func boardStateColorKey(state string) string {
	switch state {
	case "requested":
		return "idle" // yellow: waiting on someone
	case "accepted":
		return "working"
	case "blocked":
		return "blocked"
	case "done":
		return "done"
	}
	return state
}

func target(r request) string {
	if r.Target != "" {
		return r.Target
	}
	return r.Owner
}

// openThread is a cmd exchange with no recorded answer, reconstructed from the
// retained cmd stream. These are NOT tracked requests: nothing in the protocol
// records whether the target accepted them, so they are listed apart and say so.
type openThread struct {
	ID      string
	Type    string
	From    string
	Target  string
	Message string
	At      time.Time
	Replies int
}

// openThreads finds directives and challenges in the retained cmd history that
// carry no reply or verdict. "No reply in the retained history" is the only
// claim made — an answer that has been trimmed away is indistinguishable from
// one that never came, and the rendering says so.
//
// tracked filters out the cmd entries that a tracked request already accounts
// for: a request publishes a directive, so without this every tracked request
// would also appear here and be counted as waiting a second time.
func openThreads(cmds []bus.Event, tracked []request) []openThread {
	known := make(map[string]bool, 2*len(tracked))
	for _, r := range tracked {
		if r.Thread != "" {
			known[r.Thread] = true
		}
		if r.ID != "" {
			known[r.ID] = true
		}
	}
	roots := map[string]bus.Event{}
	replies := map[string]int{}
	for _, e := range cmds {
		if e.Kind != "cmd" {
			continue
		}
		id := threadID(e)
		if known[id] || known[e.ID] {
			continue
		}
		switch e.Type {
		case bus.CmdDirective, bus.CmdChallenge:
			if _, seen := roots[id]; !seen {
				roots[id] = e
			}
		case bus.CmdReply, bus.CmdVerdict:
			replies[id]++
		}
	}
	out := make([]openThread, 0, len(roots))
	for id, e := range roots {
		if replies[id] > 0 {
			continue
		}
		out = append(out, openThread{
			ID: id, Type: e.Type, From: e.From, Target: e.Target,
			Message: oneLine(messageBody(e)), At: entryTime(e.ID),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// requestsPanel renders the whole follow-up view: tracked requests first, then
// the untracked cmd threads, each section labelled with what it can and cannot
// tell the reader.
func requestsPanel(reqs []request, threads []openThread, now time.Time, width int) string {
	var sb strings.Builder
	// Only tracked requests belong here. An ordinary board task records no
	// acceptance and no response, so listing it as a request would invent a
	// pending ask out of a line of ownership bookkeeping; the BOARD pane is
	// where those live.
	tracked := trackedOnly(reqs)
	waiting := 0
	for _, r := range tracked {
		if r.waiting() {
			waiting++
		}
	}
	sb.WriteString(tag("gray", fmt.Sprintf("── tracked requests (%d waiting of %d) ──", waiting, len(tracked))) + "\n")
	if len(tracked) == 0 {
		sb.WriteString(tag("gray", "   no tracked request on the board. One is created by `agentbus request send`;\n"+
			"   an ordinary cmd directive is not one and cannot record acceptance.") + "\n")
	}
	for _, r := range tracked {
		sb.WriteString(requestBlock(r, now, width))
	}
	sb.WriteString("\n" + tag("gray", fmt.Sprintf("── cmd threads with no answer in the retained history (%d) ──", len(threads))) + "\n")
	sb.WriteString(tag("gray", "   untracked: the protocol records no acceptance for these, and an answer\n"+
		"   that has been trimmed from the stream looks the same as one that never came.") + "\n")
	for _, t := range threads {
		fmt.Fprintf(&sb, "%s %s %s %s  %s\n   %s\n",
			tag("fuchsia", pad(t.Type, 9)),
			tview.Escape(t.From), tag("gray", "→"), tag("aqua", t.Target),
			tag("gray", "asked "+humanAge(now.Sub(t.At))+" ago · thread "+t.ID),
			tview.Escape(clip(t.Message, width-4)))
	}
	return sb.String()
}

// waitingLine is the status-bar summary: how many requests are waiting on
// someone and how long the oldest has waited. Empty when nothing waits, so the
// bar stays quiet when there is nothing to chase.
func waitingLine(reqs []request, threads []openThread, now time.Time) string {
	waiting, oldest := 0, time.Duration(0)
	consider := func(t time.Time) {
		if t.IsZero() {
			return
		}
		if d := now.Sub(t); d > oldest {
			oldest = d
		}
	}
	for _, r := range trackedOnly(reqs) {
		if !r.waiting() {
			continue
		}
		waiting++
		if r.Created.IsZero() {
			consider(r.Updated)
		} else {
			consider(r.Created)
		}
	}
	for _, t := range threads {
		waiting++
		consider(t.At)
	}
	if waiting == 0 {
		return ""
	}
	word := "requests"
	if waiting == 1 {
		word = "request"
	}
	return tag("yellow", fmt.Sprintf("%d %s waiting · oldest %s", waiting, word, humanAge(oldest)))
}

// requestsTitle names the overlay and its keys, like the message detail.
func requestsTitle(waiting int) string {
	return fmt.Sprintf(" REQUESTS · %d waiting  [gray][↑↓/jk scroll · Esc back][-] ", waiting)
}
