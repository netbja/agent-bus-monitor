#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
B="$REPO/scripts/bootstrap"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
stub="$tmp/bin"
make_stub "$stub" docker 0            # docker compose up -d -> ok
# link-role-skills.sh is called by `new`; point its dest at a temp dir + fake sources.
export SKILLS_DEST="$tmp/skills" POCOCK_SKILLS_ROOT="$tmp/pocock" REPO_SKILLS="$tmp/reposkills"
for s in tdd implement code-review diagnosing-bugs codebase-design domain-modeling to-spec wayfinder to-tickets; do
  mkdir -p "$tmp/pocock/engineering/$s"; echo x > "$tmp/pocock/engineering/$s/SKILL.md"; done
for s in agent-bus agent-bus-master agent-bus-sentinel; do
  mkdir -p "$tmp/reposkills/$s"; echo x > "$tmp/reposkills/$s/SKILL.md"; done

proj="$tmp/projects"
run() { PATH="$stub:$PATH" HERDR_PLUS_PROJECTS_DIR="$proj" "$B" "$@"; }

# invalid name rejected
assert_exit 1 "rejects invalid project name" -- bash -c "PATH='$stub:$PATH' HERDR_PLUS_PROJECTS_DIR='$proj' '$B' new 1bad"

# new writes a valid template
run new demo >/dev/null
tpl="$proj/demo.toml"
assert_exit 0 "template is valid TOML" -- python3 -c "import tomllib;tomllib.load(open('$tpl','rb'))"
body="$(cat "$tpl")"
assert_contains "$body" 'name = "demo"'                          "template name"
assert_contains "$body" "agent-launch master demo"              "master tab"
assert_contains "$body" "agent-launch sentinel demo"            "sentinel tab"
assert_contains "$body" "busmon --project demo"                 "busmon tab"
# boot roles present, architect (pop) absent
assert_contains "$body" "agent-launch foureyes demo"           "foureyes tab"
if [[ "$body" != *"agent-launch architect demo"* ]]; then _ok "architect not booted"; else _bad "architect leaked into template"; fi

# auto verb -> recall when template exists (no crash, broker still ensured)
assert_exit 0 "auto recalls existing project" -- bash -c "PATH='$stub:$PATH' HERDR_PLUS_PROJECTS_DIR='$proj' '$B' demo"
finish
