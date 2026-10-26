#!/usr/bin/env bash
# End-to-end checks of the role scripts (agent-launch, agent-spawn, bootstrap,
# link-role-skills.sh) over `agentbus roles`, with fake clients: never a model, never the bus.
# Ported from the former tests/test_role_config.py; the unit cases live in rolecfg/.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
root="$T/agent profiles"
mkdir -p "$root/skills/test-skill" "$root/bin"
echo '# Test skill' > "$root/skills/test-skill/SKILL.md"
printf 'ROLE INSTRUCTIONS: review $(touch MUST_NOT_EXIST) `literal`\n' > "$root/coder.md"
for c in claude codex kimi; do
  cat > "$root/bin/$c" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == --help ]]; then echo '--settings --model --config --sandbox --ask-for-approval --append-system-prompt --name --effort --permission-mode --fallback-model --agent-file --yolo --auto --plan'; exit 0; fi
echo "ARGS $*"
env | grep -E '^(AGENT_BUS_[A-Z_]+|ROLES_TOML|ANTHROPIC_API_KEY)=' | sort
exit "${FAKE_EXIT:-0}"
EOF
  chmod +x "$root/bin/$c"
done
manifest="$root/roles.toml"
config() { # <client> [extra roles toml]
  printf 'schema_version = 2\n[defaults]\nprofile = "main"\n[profiles.main]\nclient = "%s"\nprovider = "test-provider"\nmodel = "test-model"\n[roles.coder]\ntier = "boot"\nskills = ["test-skill"]\n%s\n' "$1" "${2:-}" > "$manifest"
}
export ROLES_TOML="$manifest" ROLES_DIR="$root" REPO_SKILLS="$root/skills" POCOCK_SKILLS_ROOT="$root/pocock" \
  AGENT_LAUNCH_CACHE="$root/cache" SKILLS_DEST="$root/installed" PATH="$root/bin:$PATH"
L="$REPO/scripts/agent-launch"

# Billing and secret guard: ANTHROPIC_API_KEY reaches no client (v0.11, all clients); the
# client's exit code propagates (exec)
for c in codex kimi-cli claude-code; do
  config "$c"
  out="$(ANTHROPIC_API_KEY=key "$L" coder demo 2>&1)"
  assert_eq "$([[ "$out" == *ANTHROPIC_API_KEY=* ]] && echo kept || echo dropped)" "dropped" "$c: API key dropped"
done
assert_exit 7 "the client's exit code propagates" -- env FAKE_EXIT=7 "$L" coder demo
out="$(ANTHROPIC_API_KEY=key AGENT_LAUNCH_KEEP_API_KEY=1 "$L" coder demo 2>&1)"
assert_contains "$out" "ANTHROPIC_API_KEY=key" "AGENT_LAUNCH_KEEP_API_KEY=1 keeps it"
assert_contains "$out" "ROLES_TOML=$manifest" "the client gets the resolved manifest"
assert_eq "$([[ -e "$root/MUST_NOT_EXIST" ]] && echo ran || echo inert)" "inert" "the role prompt is never run by a shell"

# A bad option shape fails cleanly
config codex '[profiles.main.options]
sandbox = {}'
err="$("$L" coder demo 2>&1)"; rc=$?
assert_eq "$rc" "1" "a table where a string belongs exits 1"
assert_contains "$err" "agent roles: codex option sandbox must be a non-empty string" "and says why"

# agent-spawn quotes the manifest and refuses an injected project name
config claude-code
out="$(cd "$root" && HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 "$REPO/scripts/agent-spawn" coder demo 2>&1)"; rc=$?
assert_eq "$rc" "0" "agent-spawn dry run exits 0"
assert_contains "$out" "$(printf '%q' "ROLES_TOML=$manifest")" "the manifest path is shell-quoted"
assert_exit 1 "an injected project name is refused" -- env HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 bash -c "cd '$root' && '$REPO/scripts/agent-spawn' coder 'demo;touch injected'"
assert_eq "$([[ -e "$root/injected" ]] && echo yes || echo no)" "no" "nothing was injected"

# v0.15: a role launched on another profile, a fallback list, and swap mode's fatal bind
config claude-code '[profiles.alt]
client = "codex"
provider = "other-provider"
model = "alt-model"'
sed -i 's/^skills = \["test-skill"\]$/skills = ["test-skill"]\nfallback_profiles = ["alt"]/' "$manifest"
out="$(env -u HERDR_ENV "$L" coder demo --profile alt 2>&1)"; rc=$?
assert_eq "$rc" "0" "agent-launch --profile exits 0"
assert_contains "$out" "AGENT_BUS_PROVIDER=other-provider" "and runs the other profile's provider"
assert_contains "$out" "AGENT_BUS_PROFILE=alt" "under that profile's name"
out="$(cd "$root" && HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 AGENT_LAUNCH_REQUIRE_BIND=1 "$REPO/scripts/agent-spawn" coder demo --profile alt 2>&1)"
assert_contains "$out" "--profile alt" "agent-spawn passes the profile on"
assert_contains "$out" "AGENT_LAUNCH_REQUIRE_BIND=1" "and swap mode"
assert_exit 1 "an injected profile name is refused" -- env HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 bash -c "cd '$root' && '$REPO/scripts/agent-spawn' coder demo --profile 'alt;touch injected'"
assert_exit 1 "swap mode outside a herdr pane fails" -- env -u HERDR_ENV AGENT_LAUNCH_REQUIRE_BIND=1 "$L" coder demo --profile alt
out="$(cd "$root" && env -u HERDR_ENV AGENT_SPAWN_DRYRUN=1 "$REPO/scripts/agent-spawn" coder demo 2>&1)"
assert_contains "$out" "agentbus up demo" "agent-spawn outside herdr points at agentbus up <project>"
out="$("$AGENTBUS_BIN" roles fallbacks coder 2>&1)"
assert_contains "$out" "fallback alt" "roles fallbacks lists the fallback"
config claude-code

# bootstrap writes a template whose tab command runs the launcher with the selected manifest
printf '#!/bin/sh\nexit 0\n' > "$root/bin/docker"; printf '#!/bin/sh\nexit 0\n' > "$root/bin/herdr"; chmod +x "$root/bin/docker" "$root/bin/herdr"
project="$root/project \"quoted\" \$literal 🧪"; mkdir -p "$project"
out="$(AGENTBUS_PROJECTS_DIR="$root/templates" "$REPO/scripts/bootstrap" new demo "$project" 2>&1)"; rc=$?
assert_eq "$rc" "0" "bootstrap with a hostile project path exits 0"
tpl="$root/templates/demo.toml"
assert_exit 0 "the template is valid TOML" -- "$AGENTBUS_BIN" roles check-toml "$tpl"
# An agent tab is `env ROLES_TOML=… …/agent-launch`; match the env form, not any line naming agent-launch
# (a checkout path may contain the word, as .worktrees/fix-agent-launch-test does).
cmd="$(sed -n 's/^command = "\(env .*agent-launch.*\)"$/\1/p' "$tpl" | head -1 | sed 's/\\"/"/g; s/\\\\/\\/g')"
assert_contains "$cmd" "$(printf '%s' "ROLES_TOML=$manifest" | sed "s/'/'\"'\"'/g")" "the tab command carries the manifest"
out="$(cd "$project" && bash -c "$cmd" 2>&1)"; rc=$?
assert_eq "$rc" "0" "the generated tab command runs"
assert_contains "$out" "ROLES_TOML=$manifest" "and launches with the selected manifest"

# Skill links: preflight before any write, never over a real directory, idempotent, per client
export SKILLS_DEST="$root/fresh-dest"; rm -f "$root/skills/test-skill/user-file"
config claude-code '[roles.reviewer]
tier = "pop"
skills = ["missing-skill"]'
assert_exit 1 "a missing skill stops the install" -- "$REPO/scripts/link-role-skills.sh"
assert_eq "$([[ -e "$SKILLS_DEST" ]] && echo yes || echo no)" "no" "and nothing was linked"
config claude-code
mkdir -p "$SKILLS_DEST/test-skill"; echo preserve > "$SKILLS_DEST/test-skill/user-file"
assert_exit 1 "a real directory in the way stops the install" -- "$REPO/scripts/link-role-skills.sh"
assert_eq "$(cat "$SKILLS_DEST/test-skill/user-file")" "preserve" "and it is left alone"
config claude-code '[profiles.review]
client = "kimi-cli"
provider = "moonshot"
model = "alias"
[roles.reviewer]
profile = "review"
tier = "pop"
skills = ["test-skill"]'
home="$root/home"; mkdir -p "$home"
env -u SKILLS_DEST HOME="$home" "$REPO/scripts/link-role-skills.sh" >/dev/null 2>&1; rc=$?
assert_eq "$rc" "0" "install for two clients exits 0"
ino() { stat -c %i "$1" 2>/dev/null || stat -f %i "$1"; }
before="$(ino "$home/.claude/skills/test-skill") $(ino "$home/.agents/skills/test-skill")"
env -u SKILLS_DEST HOME="$home" "$REPO/scripts/link-role-skills.sh" >/dev/null 2>&1
after="$(ino "$home/.claude/skills/test-skill") $(ino "$home/.agents/skills/test-skill")"
assert_eq "$after" "$before" "a second install rewrites nothing"
for d in .claude/skills .agents/skills; do
  assert_eq "$(cd "$home/$d/test-skill" && pwd -P)" "$(cd "$root/skills/test-skill" && pwd -P)" "$d/test-skill points at the skill"
done
# v0.17: a role that pops others gets its spawn rules in one --settings flag
config claude-code 'pops = true
[roles.deploy]
tier = "pop"
approval = "person"
[roles.architect]
tier = "pop"
[tool_rules]
deny = ["Read(**/.env)", "Edit(**/.env)"]'
out="$(AGENT_LAUNCH_DRYRUN=1 "$L" coder demo 2>&1)"
assert_contains "$out" "--settings" "a popping role launches with --settings"
assert_contains "$out" '"allow": ["Bash(agent-spawn architect *)"]' "an ordinary role is allowed"
assert_contains "$out" '"ask": ["Bash(agent-spawn deploy *)"]' "an approval role is asked"
assert_contains "$out" '"deny": ["Read(**/.env)", "Edit(**/.env)"]' "shared rules reach the dry-run launch"

# v0.21.1: every Claude launch carries the SessionStart hook that reports its resume command
config claude-code
out="$(AGENT_LAUNCH_DRYRUN=1 "$L" coder demo 2>&1)"
assert_contains "$out" "report-resume $REPO/scripts/agent-launch" "a Claude launch carries the resume hook"
# The hook returns at once and prints nothing; a detached sender reports to a fake herdr.
printf '#!/usr/bin/env bash\nsleep 2\necho "$*" > "%s/herdr.calls"\necho %s\n' "$T" "'{\"result\":{}}'" > "$T/herdr"
chmod +x "$T/herdr"
start=$(date +%s%N)
hook_out="$(echo '{"session_id":"5f0c-11","hook_event_name":"SessionStart","source":"startup"}' | HERDR_ENV=1 HERDR_PANE_ID=w1:p2 \
  HERDR_BIN_PATH="$T/herdr" AGENT_BUS_AGENT=coder AGENT_BUS_PROJECT=demo XDG_STATE_HOME="$T/state" "$AGENTBUS_BIN" roles report-resume "$L")"
elapsed=$(( ($(date +%s%N) - start) / 1000000 ))
assert_eq "$hook_out" "" "the hook prints nothing on stdout"
assert_eq "$([[ $elapsed -lt 1500 ]] && echo fast || echo "slow ${elapsed}ms")" "fast" "the hook returns before herdr answers"
for _ in $(seq 50); do [[ -s "$T/state/agentbus/demo/resume.log" ]] && break; sleep 0.2; done
calls="$(cat "$T/herdr.calls" 2>/dev/null)"
assert_contains "$calls" "pane report-agent-session w1:p2 --source agentbus --agent claude --seq" "the sender reports to herdr"
assert_contains "$calls" "-- env ROLES_TOML=$manifest $L coder demo --resume 5f0c-11" "with the launcher's resume command"
assert_contains "$(cat "$T/state/agentbus/demo/resume.log" 2>/dev/null)" "accepted" "and logs the outcome"
finish
