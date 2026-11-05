package bus

// stale-waiting (spec docs/superpowers/specs/2026-11-06-stale-waiting-design.md):
// a `waiting` the person has answered, with no open question left, is read as
// idle from the answer's time by the stall guards and the notifications, and
// as "answered" by busmon. Nothing is written: the declaration stays the
// agent's.

// WaitAnswered reports when a person answered agent's waiting declared at
// declaredMs (Unix ms), or 0 when nothing proves it. open is the agent's open
// questions (OpenQuestionsOf); any makes it still waiting. The proofs, each
// strictly after the declaration (same-millisecond entries are not ordered
// across streams), the earliest wins:
//   - a retained human cmd addressed to the agent, or a human reply on a
//     thread the agent rooted (busmon's rule since v0.18; any such message
//     counts, an unrelated one too: waking too early is the safe side);
//   - an answered ask of the agent, at its latest answer not withdrawn
//     (kept for askRetention after the reply leaves the cmd stream).
func WaitAnswered(agent string, declaredMs int64, open int, asks []Ask, cmds []Event) int64 {
	if agent == "" || declaredMs <= 0 || open > 0 {
		return 0
	}
	var first int64
	take := func(at int64) {
		if at > declaredMs && (first == 0 || at < first) {
			first = at
		}
	}
	roots := make(map[string]bool)
	for _, e := range cmds {
		if e.Kind == "cmd" && e.From == agent {
			if e.Ref != "" {
				roots[e.Ref] = true
			} else if e.ID != "" {
				roots[e.ID] = true
			}
		}
	}
	for _, e := range cmds {
		if e.Kind != "cmd" || !e.Human {
			continue
		}
		if e.Target != agent && (e.Type != CmdReply || e.Ref == "" || !roots[e.Ref]) {
			continue
		}
		if ms, _ := splitID(e.ID); ms > 0 {
			take(ms)
		}
	}
	for _, a := range asks {
		if a.From != agent || a.State != "answered" {
			continue
		}
		var last int64
		for _, q := range a.Questions {
			if q.Answer != nil && !q.Answer.Withdrawn && q.Answer.At > last {
				last = q.Answer.At
			}
		}
		if last > 0 {
			take(last)
		}
	}
	return first
}

// OpenQuestionsOf counts agent's open questions across its open asks.
func OpenQuestionsOf(agent string, asks []Ask) int {
	n := 0
	for _, a := range asks {
		if a.From == agent {
			n += a.Open()
		}
	}
	return n
}
