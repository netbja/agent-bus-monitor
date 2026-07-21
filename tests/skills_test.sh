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

m="$(cat "$REPO/skills/agent-bus-master/SKILL.md")"
assert_contains "$m" "Spawn a peer"                       "master skill has Spawn a peer section"
assert_contains "$m" "agent-spawn"                        "master skill documents agent-spawn"
finish
