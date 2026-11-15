#!/usr/bin/env bash
# Machine-cron poke for the sentinel's daily review. Runs OUTSIDE any agent session
# (installed via `bootstrap --cron`), so it exports the minimal env it needs. The review is a
# bus directive to sentinel: the dispatcher wakes it (herdr exit: nothing types into a pane).
# Usage: daily-review-trigger.sh   (the crontab line sets AGENT_BUS_PROJECT)
#   DAILY_REVIEW_DRYRUN=1 -> print the directive instead of sending it (test seam).
set -euo pipefail

export PATH="$HOME/.local/bin:$PATH"   # cron's env is minimal; agentbus lives here.

: "${AGENT_BUS_PROJECT:?set AGENT_BUS_PROJECT (the crontab line does this)}"
export AGENT_BUS_AGENT="${AGENT_BUS_AGENT:-anonymous}"

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

# Skip cleanly when the sentinel is not up (no terminal registered).
if ! pane="$(agentbus pane sentinel 2>/dev/null)" || [[ -z "$pane" ]]; then
  echo "daily-review-trigger: sentinel not up (no pane); skipping today's review" >&2
  exit 0
fi

if [[ "${DAILY_REVIEW_DRYRUN:-0}" == "1" ]]; then
  echo "would send: agentbus --project $AGENT_BUS_PROJECT cmd sentinel (sentinel in $pane)"
  echo "--- message ---"
  printf '%s\n' "$MSG"
  exit 0
fi

agentbus --project "$AGENT_BUS_PROJECT" cmd sentinel "$MSG" >/dev/null
