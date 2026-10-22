#!/usr/bin/env bash
# The suites test this checkout, never what the calling shell carries: an agent's session exports
# AGENTBUS_BIN (the installed binary) and ROLES_TOML (the live manifest), and a suite that honoured
# them tested the installed release against the live manifest and stayed green on a broken branch.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export AGENTBUS_BIN=/nonexistent/agentbus ROLES_TOML=/nonexistent/roles.toml \
  AGENT_BUS_PROFILE=legacy AGENT_BUS_CLIENT=bogus AGENT_BUS_MODEL=bogus
source "$DIR/lib.sh"
assert_eq "$([[ "$AGENTBUS_BIN" == /nonexistent/* ]] && echo inherited || echo own)" "own" "an inherited AGENTBUS_BIN is not used"
assert_eq "$([[ -x "$AGENTBUS_BIN" ]] && echo built || echo missing)" "built" "the suite builds its own agentbus"
assert_eq "${ROLES_TOML-<unset>}" "<unset>" "an inherited ROLES_TOML is dropped"
assert_eq "${AGENT_BUS_PROFILE-<unset>}${AGENT_BUS_CLIENT-<unset>}${AGENT_BUS_MODEL-<unset>}" "<unset><unset><unset>" "the launcher's identity variables are dropped"
finish
