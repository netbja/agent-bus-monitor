package bus

// EffectiveState is the state a reader acts on: the stored one, except a
// turn flip's working (Auto) that a fresh observation has seen ready (herdr
// says idle or done, the pane not busy: unknown proves nothing) for staleAfter or more (a turn that ended without its hook: Esc,
// an API error, a killed client). That reads idle, since the first ready
// observation, bounded below by the flip's time. A declared state is never
// read otherwise, and nothing is rewritten. staleAfter 0 turns it off.
func EffectiveState(snap AgentSnapshot, obs *Observation, nowMs, staleAfter int64) (state string, since int64) {
	ready := obs != nil && !obs.Busy && (obs.State == "idle" || obs.State == "done") // proved ready, never unknown (reviewer-codex)
	if staleAfter <= 0 || snap.Auto != AutoTurn || snap.State != "working" || !ready ||
		!obs.Fresh(nowMs) || obs.Since <= 0 || nowMs-obs.Since < staleAfter {
		return snap.State, snap.TS
	}
	return "idle", max(snap.TS, obs.Since)
}
