#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
S="$REPO/scripts/agent-spawn"

# Refuses outside herdr.
assert_exit 1 "refuses without HERDR_ENV" -- env -u HERDR_ENV AGENT_SPAWN_DRYRUN=1 "$S" architect demo

# Dry-run prints the herdr command.
out="$(HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 "$S" architect demo)"
assert_contains "$out" "herdr agent start demo:architect"        "spawn label"
assert_contains "$out" "--cwd $REPO"                             "spawn cwd"
assert_contains "$out" "scripts/agent-launch architect demo"    "spawn runs the leaf"

assert_exit 1 "unknown role fails" -- env HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 "$S" bogus demo
finish
