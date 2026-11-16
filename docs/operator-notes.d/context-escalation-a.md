- **The context alert escalates (context-escalation A, spec 2026-11-15-context-escalation-design.md §2, §3.1-3.3,
  §3.5, §5).** The alert's directive keeps its time (`FencedCmdOnceAt`, `context:<agent>:at`). An alerted agent
  with no hand-off standing (`bus.Handoff.VoidFor` against `{p}:agents`: same pid and start, same proved
  session, no `working` since) escalates once per episode when its known fill reaches `dispatch.context_urgent`
  (90, used only above `context_alert`; at or below it, off and logged once) or the alert is
  `dispatch.context_handoff_deadline` old (20m; 0 = off; at least 1m), on the publication's Redis time: master
  gets a directive that wakes it (`context-escalate:<agent>`, none when the agent is master), the person a notice
  (`context-person:<agent>`) and the desktop notification. The mark names are `bus.ContextMark` and kin
  (`bus/context.go`); an episode's end deletes them all, the alert's time included. The `context` check reads
  them: an escalation of the fill's session with no hand-off standing is a warn with reason `escalated`, For you
  (`escalated: coder 91% (no hand-off 27 min)`); the check never judges a threshold nor writes, and the
  notification relay skips it (the dispatcher's notices are its only rings). An episode ending in the session
  it alerted, no hand-off standing, with an automatic `compact_boundary` in the session's transcript after the
  alert (`tokens.AutoCompactAfter`, under the agent's `CLAUDE_CONFIG_DIR`, else the service's, else
  `~/.claude`) tells master once, `wake=no` (`context-compacted:<agent>`, episode `<session>|<alert at>`, kept
  apart). Not in A: the mid-turn hook (`{p}:nudge`) and the compaction ceiling (PR B).
