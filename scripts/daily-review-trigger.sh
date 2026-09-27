#!/usr/bin/env bash
# Machine-cron poke for the sentinel's daily review. Runs OUTSIDE any agent session
# (installed via `bootstrap --cron`), so it exports the minimal env it needs. Adapted from
# the sibling project's daily_journal_trigger.sh — but resolves the pane LIVE via the bus
# pane-bridge instead of a hard-coded pane id.
# Usage: daily-review-trigger.sh   (crontab line sets AGENT_BUS_PROJECT + HERDR_SESSION)
#   DAILY_REVIEW_DRYRUN=1 -> print the resolved pane + message instead of sending (test seam).
set -euo pipefail

export PATH="$HOME/.local/bin:$PATH"   # cron's env is minimal; herdr/agentbus live here.

: "${AGENT_BUS_PROJECT:?set AGENT_BUS_PROJECT (the crontab line does this)}"
export AGENT_BUS_AGENT="${AGENT_BUS_AGENT:-hermes}"
[[ -n "${HERDR_SESSION:-}" ]] || echo "daily-review-trigger: warning: HERDR_SESSION unset" >&2

read -r -d '' MSG <<'EOF' || true
[cron review] Follow your agent-bus-sentinel skill for one wake.
1) Read the shared memory index and relevant sources; publish a review checkpoint only
   when something was verified, following the skill's memory protocol.
2) Refresh usage and provider budgets. Missing metrics are unavailable, not zero.
   If master's own context or provider budget needs attention, notify master to write
   a hand-off and use its client's verified session reset procedure. Never reset it yourself.
3) Drain addressed commands using your persisted cursor as the skill describes, then
   stop. Do not idle armed or start a polling loop.
EOF

# Resolve the sentinel's pane live (no hard-coded pane id). Skip cleanly if it isn't up.
if ! pane="$(agentbus pane sentinel 2>/dev/null)" || [[ -z "$pane" ]]; then
  echo "daily-review-trigger: sentinel not up (no pane); skipping today's review" >&2
  exit 0
fi

if [[ "${DAILY_REVIEW_DRYRUN:-0}" == "1" ]]; then
  echo "pane=$pane"
  echo "--- message ---"
  printf '%s\n' "$MSG"
  exit 0
fi

herdr pane send-text "$pane" "$MSG"
sleep 2
herdr pane send-keys "$pane" Enter
