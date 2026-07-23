#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
B="$REPO/scripts/bootstrap"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
stub="$tmp/bin"
make_stub "$stub" docker 0            # docker compose up -d -> ok
# herdr stub: capture link_plugin's `plugin link` call + the HERDR_SESSION it targets, so the real
# herdr is never touched. (make_stub logs only args, not env, so we hand-roll this one.)
cat > "$stub/herdr" <<HSTUB
#!/usr/bin/env bash
echo "HERDR_SESSION=\${HERDR_SESSION:-} \$*" >> "$stub/herdr.calls"
exit 0
HSTUB
chmod +x "$stub/herdr"
export HERDR_PLUS_PATH="$tmp/hplus"; mkdir -p "$HERDR_PLUS_PATH"   # fake checkout so link_plugin's -d guard passes
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

# new writes a valid template; an explicit project path -> working_dir
projdir="$(mkdir -p "$tmp/proj" && cd "$tmp/proj" && pwd)"
run new demo "$projdir" >/dev/null
tpl="$proj/demo.toml"
assert_exit 0 "template is valid TOML" -- python3 -c "import tomllib;tomllib.load(open('$tpl','rb'))"
body="$(cat "$tpl")"
assert_contains "$body" 'name = "demo"'                          "template name"
assert_contains "$body" "working_dir = \"$projdir\""             "working_dir = the given project path"
assert_contains "$body" "agent-launch master demo"              "master tab"
assert_contains "$body" "agent-launch sentinel demo"            "sentinel tab"
assert_contains "$body" "busmon --project demo"                 "busmon tab"
# boot roles present, architect (pop) absent
assert_contains "$body" "agent-launch foureyes demo"           "foureyes tab"
if [[ "$body" != *"agent-launch architect demo"* ]]; then _ok "architect not booted"; else _bad "architect leaked into template"; fi

# no path -> working_dir defaults to $PWD
projdir2="$(mkdir -p "$tmp/proj2" && cd "$tmp/proj2" && pwd)"
( cd "$projdir2" && PATH="$stub:$PATH" HERDR_PLUS_PROJECTS_DIR="$proj" "$B" new demo2 >/dev/null )
assert_contains "$(cat "$proj/demo2.toml")" "working_dir = \"$projdir2\"" 'working_dir defaults to $PWD'

# a non-directory path is rejected
assert_exit 1 "rejects a non-directory path" -- bash -c "PATH='$stub:$PATH' HERDR_PLUS_PROJECTS_DIR='$proj' '$B' new demo3 /no/such/dir"

# brique 1.2: --session <name> -> herdr-plus linked into that session (herdr registers plugins per-session)
: > "$stub/herdr.calls"
run new demos "$projdir" --session mysess >/dev/null
assert_contains "$(cat "$stub/herdr.calls")" "HERDR_SESSION=mysess plugin link $HERDR_PLUS_PATH" "herdr-plus linked into --session"

# no --session and not inside a herdr session -> link target defaults to the project name
: > "$stub/herdr.calls"
env -u HERDR_SESSION PATH="$stub:$PATH" HERDR_PLUS_PROJECTS_DIR="$proj" "$B" new demosess "$projdir" >/dev/null
assert_contains "$(cat "$stub/herdr.calls")" "HERDR_SESSION=demosess plugin link" "link session defaults to the project name"

# auto verb -> recall when template exists (no crash, broker still ensured)
assert_exit 0 "auto recalls existing project" -- bash -c "PATH='$stub:$PATH' HERDR_PLUS_PROJECTS_DIR='$proj' '$B' demo"

# --- herdr-plus build-if-missing -------------------------------------------------------------
# `herdr plugin link` registers but does NOT compile; the picker spawns ./bin/herdr-plus. bootstrap
# builds it when it's absent so the gap isn't discovered at the first pane-open.
hp="$HERDR_PLUS_PATH"
touch "$hp/Makefile"
cat > "$stub/make" <<MSTUB
#!/usr/bin/env bash
echo "\$*" >> "$stub/make.calls"
mkdir -p "$hp/bin" && : > "$hp/bin/herdr-plus" && chmod +x "$hp/bin/herdr-plus"
MSTUB
chmod +x "$stub/make"

out="$(run new demobuild "$projdir" 2>&1)"
assert_contains "$(cat "$stub/make.calls" 2>/dev/null)" "build"  "builds herdr-plus when bin/ is missing"
assert_contains "$out" "herdr-plus: built"                       "reports the build"

# already built -> no rebuild on the next bootstrap
: > "$stub/make.calls"
run new demobuilt "$projdir" >/dev/null 2>&1
assert_eq "$(cat "$stub/make.calls")" ""                         "skips the build when bin/herdr-plus exists"

# the plugin manifest's own [[build]] script wins over the Makefile
rm -f "$hp/bin/herdr-plus"
mkdir -p "$hp/scripts"
printf 'mkdir -p bin && : > bin/herdr-plus && chmod +x bin/herdr-plus\n' > "$hp/scripts/build.sh"
: > "$stub/make.calls"
out="$(run new demoscript "$projdir" 2>&1)"
assert_contains "$out" "sh scripts/build.sh"                     "prefers the plugin's own build script"
assert_eq "$(cat "$stub/make.calls")" ""                         "make unused when build.sh exists"

# no build recipe at all -> warn, don't fail the bootstrap
rm -f "$hp/bin/herdr-plus" "$hp/Makefile" "$hp/scripts/build.sh"
out="$(run new demonobuild "$projdir" 2>&1)"
assert_contains "$out" "no bin/herdr-plus and no build script"   "warns when there is nothing to build"
assert_exit 0 "bootstrap still succeeds without a build recipe" -- bash -c "PATH='$stub:$PATH' HERDR_PLUS_PROJECTS_DIR='$proj' '$B' new demonobuild2 '$projdir'"
finish
