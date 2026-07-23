#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
source "$REPO/scripts/lib/roles.sh"

# Every role in roles.toml must have a non-empty prompt file.
while IFS= read -r role; do
  f="$REPO/roles/$role.md"
  if [[ -s "$f" ]]; then _ok "prompt file for $role"; else _bad "missing/empty roles/$role.md"; fi
  assert_contains "$(cat "$f" 2>/dev/null)" "agentbus subscribe $role" "$role arms subscribe"
  # `report` is `agentbus report <agent> [--auto] <message>` — a prompt that writes
  # `report note "…"` publishes under an agent literally named "note", not under the role.
  if grep -qn 'agentbus report note' "$f" 2>/dev/null; then
    _bad "$role: 'agentbus report note' — report takes the AGENT first (use 'report $role')"
  else
    _ok "$role reports under its own name"
  fi
done < <(cat <(roles_by_tier boot) <(roles_by_tier pop))
finish
