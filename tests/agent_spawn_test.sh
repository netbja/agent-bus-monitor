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

# A10: a real spawn waits for the agent's first status; when none comes it names
# the pane to look at (its client may be waiting on a dialog). Fake herdr and a
# fake agentbus whose `agents wait` answers FAKE_WAIT_RC; everything else is the
# real build (the role checks).
fakes="$(mktemp -d)"
cat > "$fakes/herdr" <<'SH'
#!/usr/bin/env bash
case "$1 $2" in
  "tab create") echo '{"result":{"tab":{"tab_id":"w9:t1","root_pane":{"pane_id":"w9:p7"}}}}' ;;
  "pane run") echo "$*" >> "${FAKE_HERDR_LOG:-/dev/null}" ;;
esac
SH
cat > "$fakes/agentbus" <<SH
#!/usr/bin/env bash
for a in "\$@"; do [[ "\$a" == wait ]] && { echo "\$*" >> "\${FAKE_AB_LOG:-/dev/null}"; exit "\${FAKE_WAIT_RC:-0}"; }; done
exec "$AGENTBUS_BIN" "\$@"
SH
chmod +x "$fakes/herdr" "$fakes/agentbus"
log="$fakes/ab.log"
out="$(PATH="$fakes:$PATH" AGENTBUS_BIN="$fakes/agentbus" HERDR_ENV=1 FAKE_WAIT_RC=1 FAKE_AB_LOG="$log" AGENT_SPAWN_WAIT=5 "$S" architect demo 2>&1)"; rc=$?
assert_eq "$rc" "0" "a spawn whose agent has not reported still succeeds"
assert_contains "$out" "has not reported within 5s" "silent agent: the spawn says so"
assert_contains "$out" "pane w9:p7" "silent agent: the spawn names the pane"
assert_contains "$(cat "$log")" "agents wait architect --since" "the spawn waits for the agent by name"
assert_contains "$(cat "$log")" "--timeout 5s" "the wait uses AGENT_SPAWN_WAIT"
out="$(PATH="$fakes:$PATH" AGENTBUS_BIN="$fakes/agentbus" HERDR_ENV=1 FAKE_WAIT_RC=0 AGENT_SPAWN_WAIT=5 "$S" architect demo 2>&1)"
assert_contains "$out" "architect reported" "a reporting agent: the spawn says it is up"
: > "$log"
out="$(PATH="$fakes:$PATH" AGENTBUS_BIN="$fakes/agentbus" HERDR_ENV=1 FAKE_AB_LOG="$log" AGENT_SPAWN_WAIT=0 "$S" architect demo 2>&1)"
assert_eq "$(cat "$log")" "" "AGENT_SPAWN_WAIT=0 does not wait"
finish
