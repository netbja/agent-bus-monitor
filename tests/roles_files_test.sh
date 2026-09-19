#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
source "$REPO/scripts/lib/roles.sh"

# Every role in roles.toml must have a non-empty prompt file.
while IFS= read -r role; do
  f="$REPO/roles/$role.md"
  if [[ -s "$f" ]]; then _ok "prompt file for $role"; else _bad "missing/empty roles/$role.md"; fi
  # The role must name ITSELF in its subscribe command — a briefing that subscribes as the
  # wrong agent is the bug this catches. Flags may sit in between (the sentinel drains with
  # `subscribe --since <cursor> sentinel`), so match the command and the name, not adjacency.
  if grep -qE "agentbus subscribe.*\b$role\b" "$f" 2>/dev/null; then
    _ok "$role names itself in its subscribe command"
  else
    _bad "$role: no 'agentbus subscribe … $role' in roles/$role.md"
  fi
  # `report` is `agentbus report <agent> [--auto] <message>` — a prompt that writes
  # `report note "…"` publishes under an agent literally named "note", not under the role.
  if grep -qn 'agentbus report note' "$f" 2>/dev/null; then
    _bad "$role: 'agentbus report note' — report takes the AGENT first (use 'report $role')"
  else
    _ok "$role reports under its own name"
  fi
done < <(cat <(roles_by_tier boot) <(roles_by_tier pop))
finish
