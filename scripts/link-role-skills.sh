#!/usr/bin/env bash
# Symlink exactly the skills referenced in roles.toml into ~/.claude/skills, from two roots:
#   - this repo's skills/        (bus skills: agent-bus, agent-bus-master, agent-bus-sentinel)
#   - the Matt Pocock collection (engineering: tdd, code-review, ...)
# Idempotent: two runs leave identical symlinks.
# Env seams: SKILLS_DEST, REPO_SKILLS, POCOCK_SKILLS_ROOT.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/lib/roles.sh"

DEST="${SKILLS_DEST:-$HOME/.claude/skills}"
REPO_SKILLS="${REPO_SKILLS:-$REPO/skills}"
POCOCK_ROOT="${POCOCK_SKILLS_ROOT:-$HOME/Tools/herdr-plugins/skills/skills}"

die() { echo "link-role-skills: $*" >&2; exit 1; }

# Gather the unique set of skills across every role.
mapfile -t roles < <(python3 - "$ROLES_TOML" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f: data = tomllib.load(f)
for name in data.get("roles", {}): print(name)
PY
)
wanted=()
for r in ${roles[@]+"${roles[@]}"}; do
  while IFS= read -r s; do [[ -n "$s" ]] && wanted+=("$s"); done < <(role_field "$r" skills)
done
# Dedup only when non-empty: printf with no args would emit a phantom blank "skill",
# and "${wanted[@]}" on an empty array is unbound-var unsafe on bash < 4.4.
if ((${#wanted[@]})); then
  mapfile -t wanted < <(printf '%s\n' "${wanted[@]}" | sort -u)
fi

mkdir -p "$DEST"
for skill in ${wanted[@]+"${wanted[@]}"}; do
  if [[ -f "$REPO_SKILLS/$skill/SKILL.md" ]]; then
    src="$REPO_SKILLS/$skill"
  elif [[ -f "$POCOCK_ROOT/engineering/$skill/SKILL.md" ]]; then
    src="$POCOCK_ROOT/engineering/$skill"
  else
    die "skill not found in repo or Pocock collection: $skill"
  fi
  target="$DEST/$skill"
  if [[ -e "$target" && ! -L "$target" ]]; then rm -rf "$target"; fi
  ln -sfn "$src" "$target"
  echo "linked $skill -> $src"
done
