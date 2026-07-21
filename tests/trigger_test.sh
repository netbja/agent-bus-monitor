#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
T="$REPO/scripts/daily-review-trigger.sh"
B="$REPO/scripts/bootstrap"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
stub="$tmp/bin"

# (a) dry-run prints pane + message when the sentinel is up.
make_stub "$stub" agentbus 0 "w3:p2"        # `agentbus pane sentinel` -> w3:p2
out="$(PATH="$stub:$PATH" AGENT_BUS_PROJECT=demo HERDR_SESSION=s DAILY_REVIEW_DRYRUN=1 "$T")"
assert_contains "$out" "pane=w3:p2"                 "trigger resolved pane"
assert_contains "$out" "agent-bus-sentinel skill"   "trigger message references the skill"

# (b) sentinel down -> skip, exit 0, do not call herdr.
make_stub "$stub" agentbus 1                # pane lookup fails
make_stub "$stub" herdr 0
assert_exit 0 "skips cleanly when sentinel down" -- \
  bash -c "PATH='$stub:$PATH' AGENT_BUS_PROJECT=demo HERDR_SESSION=s '$T'"
if [[ ! -f "$stub/herdr.calls" ]]; then _ok "herdr never called when sentinel down"; else _bad "herdr called despite no pane"; fi

# (c) crontab install is idempotent.
make_stub "$stub" docker 0
cronfile="$tmp/cron.txt"; : > "$cronfile"
cat > "$stub/crontab" <<EOF
#!/usr/bin/env bash
if [[ "\${1:-}" == "-l" ]]; then cat "$cronfile"; else cat > "$cronfile"; fi
EOF
chmod +x "$stub/crontab"
export SKILLS_DEST="$tmp/skills" POCOCK_SKILLS_ROOT="$tmp/pocock" REPO_SKILLS="$tmp/reposkills"
for s in tdd implement code-review diagnosing-bugs codebase-design domain-modeling to-spec wayfinder to-tickets; do mkdir -p "$tmp/pocock/engineering/$s"; echo x >"$tmp/pocock/engineering/$s/SKILL.md"; done
for s in agent-bus agent-bus-master agent-bus-sentinel; do mkdir -p "$tmp/reposkills/$s"; echo x >"$tmp/reposkills/$s/SKILL.md"; done
run_cron() { PATH="$stub:$PATH" HERDR_PLUS_PROJECTS_DIR="$tmp/projects" "$B" new demo --cron >/dev/null; }
run_cron; run_cron
assert_eq "$(grep -c 'daily-review: demo' "$cronfile")" "1" "cron line installed exactly once"

# (d) prefix collision: "dem" is a substring of "demo" — whole-line tag match must still install it
#     (regression guard for grep -qxF vs -qF).
PATH="$stub:$PATH" HERDR_PLUS_PROJECTS_DIR="$tmp/projects" "$B" new dem --cron >/dev/null
assert_eq "$(grep -c 'daily-review: dem$' "$cronfile")"  "1" "prefix project 'dem' installs despite 'demo' present"
assert_eq "$(grep -c 'daily-review: demo$' "$cronfile")" "1" "'demo' cron still present after 'dem' install"
finish
