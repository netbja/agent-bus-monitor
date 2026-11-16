- **Codex's question menus read blocked (codex-rate-limit-prompt).** A Codex menu whose question
  ends in "?" right above its `› 1.` selector (the rate-limit menu "Switch to <model> for lower
  credit usage?", which held reviewer-codex 15 minutes on 2026-11-16) is now the dispatcher's
  `blocked`: master gets `blocked: <agent> (codex) since … screen: "<question>"` and busmon shows the
  agent waiting with a blocked flag, instead of "idle" and a bare "agent unknown" escalation. A
  menu without a question mark still reads unknown. F12, attach's default prefix, is now recorded
  free for Codex (probe `agentbus term record codex f12-at-input`, codex-cli 0.169.0), so attach to
  a Codex agent no longer needs `--prefix`.
