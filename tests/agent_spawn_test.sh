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

# v0.17 §5.3: an agent pops an approval role only with a person's approval, spent by
# `agentbus approval use`; a person (no AGENT_BUS_AGENT) and an ordinary role are not gated.
# The stub records the approval calls and hands every other command to the real binary.
T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
printf '%s\n' '#!/usr/bin/env bash' \
  'if [[ " $* " == *" approval use "* ]]; then echo "$*" >> "$STUB_CALLS"; [[ -n "${STUB_ERR:-}" ]] && echo "$STUB_ERR" >&2; exit "${STUB_EXIT:-0}"; fi' \
  'exec "$REAL_AGENTBUS" "$@"' > "$T/agentbus"
chmod +x "$T/agentbus"
: > "$T/calls"
gate() { env -u AGENT_BUS_AGENT -u AGENT_SPAWN_APPROVAL HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 AGENTBUS_BIN="$T/agentbus" \
  REAL_AGENTBUS="$REPO/agentbus" STUB_CALLS="$T/calls" "$@"; }
assert_exit 1 "deploy without approval is refused" -- gate AGENT_BUS_AGENT=master STUB_EXIT=3 "$S" deploy demo
out="$(gate AGENT_BUS_AGENT=master STUB_EXIT=3 STUB_ERR="approval: no usable approval: not answered" "$S" deploy demo 2>&1)"
assert_contains "$out" "no usable approval: not answered" "and says why"
assert_contains "$(cat "$T/calls")" "--project demo approval use deploy" "the gate spends through agentbus"
out="$(gate AGENT_BUS_AGENT=master "$S" deploy demo)"
assert_contains "$out" "agent-launch deploy demo" "deploy with an approval is popped"
: > "$T/calls"
gate AGENT_BUS_AGENT=master AGENT_SPAWN_APPROVAL=1-1 "$S" deploy demo >/dev/null
assert_contains "$(cat "$T/calls")" "approval use deploy --by-swap 1-1" "a swap hands its spent approval over"
: > "$T/calls"
out="$(gate STUB_EXIT=3 "$S" deploy demo)"
assert_contains "$out" "agent-launch deploy demo" "a person is not gated"
gate AGENT_BUS_AGENT=master STUB_EXIT=3 "$S" coder demo >/dev/null
assert_eq "$(cat "$T/calls")" "" "an ordinary role is not gated"
finish
