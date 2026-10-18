#!/usr/bin/env bash
# Shared validated resolver. Source this file; ROLES_TOML is inherited by children.
# The resolver is `agentbus roles` (rolecfg/): AGENTBUS_BIN, else the repo's build.
if [[ -z "${REPO:-}" ]]; then
  REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fi
AGENTBUS_BIN="${AGENTBUS_BIN:-$REPO/agentbus}"
[[ -x "$AGENTBUS_BIN" ]] || { echo "agent roles: $AGENTBUS_BIN not built (run ./setup.sh install or: go build -o agentbus ./cmd/agentbus)" >&2; exit 1; }
export AGENTBUS_BIN AGENTBUS_REPO="$REPO"
ROLES_TOML="${ROLES_TOML:-$REPO/roles.toml}"
# Absolute, symlinks resolved (Python's Path.resolve()); the file may not exist yet.
ROLES_TOML="$(realpath -m -- "$ROLES_TOML" 2>/dev/null || { cd "$(dirname -- "$ROLES_TOML")" 2>/dev/null && printf '%s/%s\n' "$(pwd -P)" "$(basename -- "$ROLES_TOML")"; } || printf '%s\n' "$ROLES_TOML")"
export ROLES_TOML
role_exists() { "$AGENTBUS_BIN" roles exists "$1"; }
role_field() { "$AGENTBUS_BIN" roles field "$1" "$2"; }
roles_by_tier() { "$AGENTBUS_BIN" roles tier "$1"; }
roles_validate() { "$AGENTBUS_BIN" roles validate; }
