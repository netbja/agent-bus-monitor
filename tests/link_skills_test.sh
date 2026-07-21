#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
dest="$tmp/skills"
# Fake a Pocock collection with just the engineering skills roles.toml references.
pocock="$tmp/pocock"
for s in tdd implement code-review diagnosing-bugs codebase-design domain-modeling to-spec wayfinder to-tickets; do
  mkdir -p "$pocock/engineering/$s"; echo "# $s" > "$pocock/engineering/$s/SKILL.md"
done
# agent-bus-sentinel does not exist in the repo yet (created in Task 9); fake it so the
# resolver's repo-first branch has something to find.
mkdir -p "$tmp/reposkills/agent-bus-sentinel"; echo "# sentinel" > "$tmp/reposkills/agent-bus-sentinel/SKILL.md"
for s in agent-bus agent-bus-master; do
  mkdir -p "$tmp/reposkills/$s"; echo "# $s" > "$tmp/reposkills/$s/SKILL.md"
done

run() { SKILLS_DEST="$dest" REPO_SKILLS="$tmp/reposkills" POCOCK_SKILLS_ROOT="$pocock" \
        "$REPO/scripts/link-role-skills.sh" >/dev/null; }

run
assert_eq "$(readlink "$dest/tdd")"            "$pocock/engineering/tdd"        "tdd -> Pocock"
assert_eq "$(readlink "$dest/agent-bus")"      "$tmp/reposkills/agent-bus"      "agent-bus -> repo"
assert_eq "$(readlink "$dest/agent-bus-master")" "$tmp/reposkills/agent-bus-master" "master skill -> repo"
before="$(ls -l "$dest" | md5sum)"
run                                       # second run must be a no-op
after="$(ls -l "$dest" | md5sum)"
assert_eq "$after" "$before" "idempotent (identical after 2nd run)"

# refuse rather than silently rm -rf a real (non-symlink) dir a user placed at a skill target
dest3="$tmp/skills3"; mkdir -p "$dest3/tdd"; echo "user's own" > "$dest3/tdd/SKILL.md"
assert_exit 1 "refuses to overwrite a real dir at a skill target" -- \
  env SKILLS_DEST="$dest3" REPO_SKILLS="$tmp/reposkills" POCOCK_SKILLS_ROOT="$pocock" "$REPO/scripts/link-role-skills.sh"
if [[ -f "$dest3/tdd/SKILL.md" ]]; then _ok "user's real skill dir preserved (not deleted)"; else _bad "user's real skill dir was destroyed"; fi
finish
