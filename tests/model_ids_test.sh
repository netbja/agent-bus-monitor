#!/usr/bin/env bash
# Model ids belong in roles.toml and nowhere else.
#
# `scripts/agent-launch` reads roles.toml and passes --model to claude, so that file is what
# actually runs. Every other mention is decoration that nothing verifies — and it rots: when
# the architect moved to a new model, roles.toml was updated and roles/architect.md was left
# claiming the old one, so the agent's own briefing told it a model it was not running on.
# Nobody noticed for weeks, because nothing checked. This is the check.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

# Any claude model id: claude-<family>-<version>, plus the bare family names that have been
# used as shorthand in prose ("Fable, Opus on fallback").
pattern='claude-(sonnet|opus|haiku|fable)-[0-9]|\b(Sonnet|Opus|Haiku|Fable)\b'

offenders=""
while IFS= read -r f; do
  if grep -qEn "$pattern" "$f" 2>/dev/null; then
    offenders+="  $f: $(grep -EnoH "$pattern" "$f" | head -3 | tr '\n' ' ')"$'\n'
  fi
done < <(find "$REPO/roles" "$REPO/skills" -name '*.md' 2>/dev/null | sort)

if [[ -n "$offenders" ]]; then
  _bad "model ids must live only in roles.toml; found them in prose:"$'\n'"$offenders"
else
  _ok "no model id in roles/*.md or skills/**/*.md"
fi

# The manifest itself must still carry one per role, or agent-launch has nothing to pass.
roles_with_model=$(grep -cE '^model = "' "$REPO/roles.toml" 2>/dev/null || echo 0)
roles_defined=$(grep -cE '^\[roles\.' "$REPO/roles.toml" 2>/dev/null || echo 0)
assert_eq "$roles_with_model" "$roles_defined" "every role in roles.toml declares a model"

finish
