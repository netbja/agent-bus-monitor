#!/usr/bin/env bash
# Shared read-only resolver over roles.toml. SOURCE this file; do not exec it.
# Env seam: ROLES_TOML (default <repo>/roles.toml).

if [[ -z "${REPO:-}" ]]; then
  REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fi
ROLES_TOML="${ROLES_TOML:-$REPO/roles.toml}"

# role_exists <role> -> exit 0 if defined, 3 otherwise.
role_exists() {
  python3 - "$ROLES_TOML" "$1" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f:
    data = tomllib.load(f)
sys.exit(0 if sys.argv[2] in data.get("roles", {}) else 3)
PY
}

# role_field <role> <field> -> print scalar, or list one-per-line.
#   exit 3 if the role is unknown; empty output (exit 0) if the field is absent.
role_field() {
  python3 - "$ROLES_TOML" "$1" "$2" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f:
    data = tomllib.load(f)
r = data.get("roles", {}).get(sys.argv[2])
if r is None:
    sys.exit(3)
v = r.get(sys.argv[3])
if v is None:
    sys.exit(0)
print("\n".join(map(str, v)) if isinstance(v, list) else v)
PY
}

# roles_by_tier <tier> -> print role names of that tier, one per line, file order.
roles_by_tier() {
  python3 - "$ROLES_TOML" "$1" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f:
    data = tomllib.load(f)
for name, r in data.get("roles", {}).items():
    if r.get("tier") == sys.argv[2]:
        print(name)
PY
}
