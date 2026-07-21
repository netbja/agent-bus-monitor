#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
L="$REPO/scripts/agent-launch"

out="$(AGENT_LAUNCH_DRYRUN=1 "$L" master demo)"
assert_contains "$out" "--model claude-sonnet-5"            "master model flag"
assert_contains "$out" "--permission-mode acceptEdits"     "master permission flag"
assert_contains "$out" "--name demo:master"                "master session name"
assert_contains "$out" "roles/master.md"                   "master prompt file"

out="$(AGENT_LAUNCH_DRYRUN=1 "$L" coder demo)"
assert_contains "$out" "--permission-mode bypassPermissions" "coder permission flag"

out="$(AGENT_LAUNCH_DRYRUN=1 "$L" architect demo)"
assert_contains "$out" "--model claude-fable-5"            "architect model"
assert_contains "$out" "--fallback-model claude-opus-4-8"  "architect fallback"

assert_exit 1 "unknown role fails"     -- env AGENT_LAUNCH_DRYRUN=1 "$L" bogus demo
assert_exit 1 "invalid project fails"  -- env AGENT_LAUNCH_DRYRUN=1 "$L" master 1bad
finish
