#!/usr/bin/env bash
# Shared validated resolver. Source this file; ROLES_TOML is inherited by children.
if [[ -z "${REPO:-}" ]]; then
  REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fi
ROLES_TOML="${ROLES_TOML:-$REPO/roles.toml}"
ROLES_TOML="$(python3 -c 'import pathlib,sys; print(pathlib.Path(sys.argv[1]).resolve())' "$ROLES_TOML")"
export ROLES_TOML
role_exists() { python3 "$REPO/scripts/lib/role_config.py" exists "$1"; }
role_field() { python3 "$REPO/scripts/lib/role_config.py" field "$1" "$2"; }
roles_by_tier() { python3 "$REPO/scripts/lib/role_config.py" tier "$1"; }
roles_validate() { python3 "$REPO/scripts/lib/role_config.py" validate; }
