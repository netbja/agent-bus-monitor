#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
B="$REPO/scripts/bootstrap"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
stub="$tmp/bin"
make_stub "$stub" docker 0
make_stub "$stub" codegraph 0        # codegraph init -> ok, logs to codegraph.calls
export SKILLS_DEST="$tmp/skills" POCOCK_SKILLS_ROOT="$tmp/pocock" REPO_SKILLS="$tmp/reposkills"
for s in tdd implement code-review diagnosing-bugs codebase-design domain-modeling to-spec wayfinder to-tickets; do mkdir -p "$tmp/pocock/engineering/$s"; echo x >"$tmp/pocock/engineering/$s/SKILL.md"; done
for s in agent-bus agent-bus-master agent-bus-sentinel; do mkdir -p "$tmp/reposkills/$s"; echo x >"$tmp/reposkills/$s/SKILL.md"; done

# The preflight targets the PROJECT dir (2nd positional), not the tooling repo — so codegraph
# init runs there and the marker lands there (never in $REPO).
projdir="$(mkdir -p "$tmp/proj" && cd "$tmp/proj" && pwd)"   # fresh dir, no .codegraph -> init should run
PATH="$stub:$PATH" HERDR_PLUS_PROJECTS_DIR="$tmp/projects" "$B" new demo "$projdir" --index >/dev/null
assert_contains "$(cat "$stub/codegraph.calls" 2>/dev/null)" "init" "codegraph init called when .codegraph absent"
if [[ -f "$projdir/.agent-bus/index-requested" ]]; then _ok "index marker written in the project dir"; else _bad "no index marker in project dir"; fi
if [[ ! -e "$REPO/.agent-bus" ]]; then _ok "tooling repo left untouched (no \$REPO/.agent-bus)"; else _bad "leaked a marker into the tooling repo"; fi

# Non-fatal when codegraph fails.
make_stub "$stub" codegraph 1
rm -rf "$projdir/.agent-bus"
assert_exit 0 "bootstrap --index stays non-fatal on codegraph failure" -- \
  bash -c "PATH='$stub:$PATH' HERDR_PLUS_PROJECTS_DIR='$tmp/projects' SKILLS_DEST='$tmp/skills' POCOCK_SKILLS_ROOT='$tmp/pocock' REPO_SKILLS='$tmp/reposkills' '$B' new demo '$projdir' --index"
finish
