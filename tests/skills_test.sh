#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

s="$REPO/skills/agent-bus-sentinel/SKILL.md"
if [[ -s "$s" ]]; then _ok "sentinel skill exists"; else _bad "missing agent-bus-sentinel/SKILL.md"; fi
body="$(cat "$s" 2>/dev/null)"
assert_contains "$body" "name: agent-bus-sentinel"        "sentinel skill frontmatter name"
assert_contains "$body" "index_repository"                "sentinel documents index warm-up"
assert_contains "$body" ".agent-bus/index-requested"      "sentinel checks the index marker"
assert_contains "$body" "cmd master"                      "sentinel documents the notify-only nudge"
assert_contains "$body" "agentbus refresh"                "sentinel refreshes budget+usage on wake"
r="$(cat "$REPO/roles/sentinel.md")"
assert_contains "$r" "agentbus refresh"                   "sentinel role prompt runs refresh"

m="$(cat "$REPO/skills/agent-bus-master/SKILL.md")"
assert_contains "$m" "Spawn a peer"                       "master skill has Spawn a peer section"
assert_contains "$m" "agent-spawn"                        "master skill documents agent-spawn"
assert_contains "$m" "agentbus budget"                    "master skill reads the account budget"
# the tee is gone: a prompt that still tells agents to publish usage is stale
if grep -q 'agentbus usage "' "$REPO/skills/agent-bus-master/SKILL.md" "$s" 2>/dev/null; then
  _bad "a skill still documents the removed status-line usage tee"
else
  _ok "no skill documents the removed usage tee"
fi
finish
