#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
S="$REPO/scripts/agent-spawn"

# Refuses outside herdr.
assert_exit 1 "refuses without HERDR_ENV" -- env -u HERDR_ENV AGENT_SPAWN_DRYRUN=1 "$S" architect demo

# Dry-run prints the new-tab spawn commands (tab create + pane run agent-launch), not a split.
out="$(HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 "$S" architect demo)"
assert_contains "$out" "herdr tab create --label architect"     "spawn creates a new tab (not a split)"
assert_contains "$out" "--cwd $REPO"                            "spawn tab cwd"
assert_contains "$out" "herdr pane run"                         "spawn runs the leaf in the new tab"
assert_contains "$out" "agent-launch architect demo"           "spawn runs the launch leaf"

assert_exit 1 "unknown role fails" -- env HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 "$S" bogus demo
finish
