#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
B="$REPO/scripts/bootstrap"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
stub="$tmp/bin"
make_stub "$stub" docker 0            # docker compose up -d -> ok
# herdr stub: logs every call, so the real herdr is never touched and a test can prove that
# bootstrap no longer registers any plugin (setup.sh links agent-bus once, globally).
make_stub "$stub" herdr 0
# link-role-skills.sh is called by `new`; point its dest at a temp dir + fake sources.
export SKILLS_DEST="$tmp/skills" POCOCK_SKILLS_ROOT="$tmp/pocock" REPO_SKILLS="$tmp/reposkills"
for s in tdd implement code-review diagnosing-bugs codebase-design domain-modeling to-spec wayfinder to-tickets; do
  mkdir -p "$tmp/pocock/engineering/$s"; echo x > "$tmp/pocock/engineering/$s/SKILL.md"; done
for s in agent-bus agent-bus-master agent-bus-sentinel agent-bus-security agent-bus-docs; do
  mkdir -p "$tmp/reposkills/$s"; echo x > "$tmp/reposkills/$s/SKILL.md"; done

proj="$tmp/projects"
run() { PATH="$stub:$PATH" AGENTBUS_PROJECTS_DIR="$proj" "$B" "$@"; }

# invalid name rejected
assert_exit 1 "rejects invalid project name" -- bash -c "PATH='$stub:$PATH' AGENTBUS_PROJECTS_DIR='$proj' '$B' new 1bad"

# new writes a valid template; an explicit project path -> working_dir
projdir="$(mkdir -p "$tmp/proj" && cd "$tmp/proj" && pwd)"
run new demo "$projdir" >/dev/null
tpl="$proj/demo.toml"
assert_exit 0 "template is valid TOML" -- "$AGENTBUS_BIN" roles check-toml "$tpl"
body="$(cat "$tpl")"
assert_contains "$body" 'name = "demo"'                          "template name"
assert_contains "$body" "working_dir = \"$projdir\""             "working_dir = the given project path"
assert_contains "$body" "agent-launch master demo"              "master tab"
assert_contains "$body" "agent-launch sentinel demo"            "sentinel tab"
assert_contains "$body" "busmon --project demo"                 "busmon tab"
# boot roles present, architect (pop) absent
assert_contains "$body" "agent-launch foureyes demo"           "foureyes tab"
if [[ "$body" != *"agent-launch architect demo"* ]]; then _ok "architect not booted"; else _bad "architect leaked into template"; fi
# v0.8: a dispatch tab runs the wake dispatcher, built like the busmon tab (repo binary)
assert_contains "$body" 'name = "dispatch"'                      "dispatch tab"
assert_contains "$body" "$REPO/agentbus --project demo dispatch" "dispatch tab runs agentbus dispatch"

# A missing external skill (the download failed) warns; bootstrap still writes the template
mv "$tmp/pocock/engineering/wayfinder" "$tmp/wayfinder.away"
miss_out="$(run new demomiss "$projdir" 2>&1)"; rc=$?
assert_eq "$rc" "0" "a missing skill does not stop bootstrap"
assert_contains "$miss_out" "warning: skill wayfinder not found" "and it is named"
assert_eq "$([[ -f "$proj/demomiss.toml" ]] && echo written || echo missing)" "written" "the template is still written"
mv "$tmp/wayfinder.away" "$tmp/pocock/engineering/wayfinder"

# --no-dispatch omits the dispatch tab (busmon stays)
run new demonod "$projdir" --no-dispatch >/dev/null
body_nod="$(cat "$proj/demonod.toml")"
assert_exit 0 "--no-dispatch template is valid TOML" -- "$AGENTBUS_BIN" roles check-toml "$proj/demonod.toml"
if [[ "$body_nod" != *'name = "dispatch"'* && "$body_nod" != *"--project demonod dispatch"* ]]; then _ok "--no-dispatch omits the dispatch tab"; else _bad "--no-dispatch still wrote a dispatch tab"; fi
assert_contains "$body_nod" "busmon --project demonod"          "--no-dispatch keeps the busmon tab"

# v0.10: a missions tab runs the conductor on the project's repository
assert_contains "$body" 'name = "missions"'                                       "missions tab"
assert_contains "$body" "$REPO/agentbus --project demo missions --repo $projdir"  "missions tab runs the conductor on the project dir"
run new demonom "$projdir" --no-missions >/dev/null
body_nom="$(cat "$proj/demonom.toml")"
assert_exit 0 "--no-missions template is valid TOML" -- "$AGENTBUS_BIN" roles check-toml "$proj/demonom.toml"
if [[ "$body_nom" != *'name = "missions"'* ]]; then _ok "--no-missions omits the missions tab"; else _bad "--no-missions still wrote a missions tab"; fi
assert_contains "$body_nom" 'name = "dispatch"'                 "--no-missions keeps the dispatch tab"

# no path -> working_dir defaults to $PWD
projdir2="$(mkdir -p "$tmp/proj2" && cd "$tmp/proj2" && pwd)"
( cd "$projdir2" && PATH="$stub:$PATH" AGENTBUS_PROJECTS_DIR="$proj" "$B" new demo2 >/dev/null )
assert_contains "$(cat "$proj/demo2.toml")" "working_dir = \"$projdir2\"" 'working_dir defaults to $PWD'

# a non-directory path is rejected
assert_exit 1 "rejects a non-directory path" -- bash -c "PATH='$stub:$PATH' AGENTBUS_PROJECTS_DIR='$proj' '$B' new demo3 /no/such/dir"

# bootstrap registers no plugin: setup.sh linked agent-bus once, in herdr's global registry
: > "$stub/herdr.calls"
run new demos "$projdir" --session mysess >/dev/null
run recall demos >/dev/null
assert_eq "$(grep -c 'plugin' "$stub/herdr.calls")" "0" "bootstrap calls no herdr plugin command (new and recall)"
rec_out="$(run recall demo 2>&1)"
assert_contains "$rec_out" "agentbus up demo" "recall names agentbus up <project>"
# run by agentbus up (stranger-test): the hint to run agentbus up is not printed to someone who just did
up_out="$(AGENTBUS_UP=1 run recall demo 2>&1)"
assert_contains "$up_out" "bootstrap: recall demo done" "recall from up still completes"
assert_eq "$([[ "$up_out" == *"agentbus up demo"* ]] && echo hinted || echo quiet)" "quiet" "recall from up prints no 'agentbus up' hint"

# auto verb -> recall when template exists (no crash, broker still ensured)
assert_exit 0 "auto recalls existing project" -- bash -c "PATH='$stub:$PATH' AGENTBUS_PROJECTS_DIR='$proj' '$B' demo"

# busmon is the first tab (herdr focuses it when the workspace opens); --as puts the person's name on it
first_tab="$(grep -m1 -A1 '^\[\[tabs\]\]' "$tpl" | tail -1)"
assert_eq "$first_tab" 'name = "busmon"' "busmon is the first tab"
run new demoas "$projdir" --as charlie >/dev/null
assert_contains "$(cat "$proj/demoas.toml")" "busmon --project demoas --as charlie" "--as reaches the busmon tab"
assert_exit 1 "an invalid --as is refused" -- bash -c "PATH='$stub:$PATH' AGENTBUS_PROJECTS_DIR='$proj' '$B' new demoas2 '$projdir' --as Charlie"
finish
