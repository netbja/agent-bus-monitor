#!/usr/bin/env bash
# Test setup.sh: flags, a dry run that changes nothing, a real install
# into a copy of the repository (stale binaries replaced, version stamped, idempotent herdr
# config edit, agent-bus linked as the herdr plugin netbja.agentbus, external skills cloned),
# status, uninstall and the Go floor. Hermetic: a fake herdr, a local skills repository, a
# throwaway HOME; it never touches this checkout, the live BIN_DIR, herdr, docker or the real skills.
# SETUP_SCRIPT picks the installer under test: setup.sh (default), or the Go installer's thin
# scripts/setup-go.sh (installer_go_test.sh), which must pass the same asserts plus the broker's
# ownership ones (docs/superpowers/specs/2026-10-28-go-installer-design.md, Rollout).
SETUP_SCRIPT="${SETUP_SCRIPT:-setup.sh}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
# Keep Go's caches where they are: a throwaway HOME must not mean a cold build.
export GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOPATH="$(go env GOPATH)"
export AGENTBUS_SETUP_NO_MODELS=1 AGENTBUS_SETUP_NO_IDENTITY=1 NO_COLOR=1 HOME="$T/home" XDG_CONFIG_HOME="$T/cfg" XDG_DATA_HOME="$T/data" XDG_STATE_HOME="$T/state"
mkdir -p "$HOME"
PLUGIN_RECORD="$T/state/agentbus/plugin.path"

# A fake herdr: logs every call and keeps its plugin registry in a file, one JSON object per
# plugin, served by `plugin list --json` the way herdr 0.9.3 prints it (result.plugins[]).
mkdir -p "$T/fakebin"
export HERDR_LOG="$T/herdr.log" HERDR_PLUGINS="$T/herdr-plugins.jsonl"
cat > "$T/fakebin/herdr" <<'EOF'
#!/bin/sh
echo "$*" >> "$HERDR_LOG"
case "$*" in
  "plugin list --json")
    printf '{"id":"cli:plugin","result":{"plugins":['
    [ -s "$HERDR_PLUGINS" ] && paste -sd, "$HERDR_PLUGINS" | tr -d '\n'
    printf ']}}\n' ;;
  "plugin link "*)
    [ -n "${HERDR_FAIL_LINK:-}" ] && { echo "error: cannot link $3" >&2; exit 1; }
    printf '{"plugin_id":"netbja.agentbus","name":"agent-bus","enabled":true,"version":"0.20.0","plugin_root":"%s","source":{"kind":"local","path":"%s"}}\n' "$3" "$3" >> "$HERDR_PLUGINS" ;;
  "plugin unlink "*)
    grep -v "\"plugin_id\":\"$3\"" "$HERDR_PLUGINS" > "$HERDR_PLUGINS.tmp"; mv "$HERDR_PLUGINS.tmp" "$HERDR_PLUGINS" ;;
  "plugin action list")
    if [ -n "${HERDR_NO_SERVER:-}" ]; then echo '{"id":"cli:plugin","error":{"code":"server_not_running","message":"no herdr server is running"}}'; exit 0; fi
    grep -q '"plugin_id":"netbja.agentbus"' "$HERDR_PLUGINS" 2>/dev/null &&
      echo '{"result":{"actions":[{"action_id":"projects","command":["./agentbus","plugin","projects"],"plugin_id":"netbja.agentbus"}]}}' ;;
  "server reload-config") [ -n "${HERDR_NO_SERVER:-}" ] && exit 1 ;;
  "--version") echo "herdr 0.0-test" ;;
esac
exit 0
EOF
chmod +x "$T/fakebin/herdr"
# set_registry ROOT [ENABLED]: herdr lists netbja.agentbus from ROOT, next to a herdr-plus entry
# whose nested object carries decoy keys (a reader must take only the plugin's own fields).
set_registry() {
  printf '{"name":"Herdr Plus","source":{"kind":"local","plugin_id":"netbja.agentbus","enabled":false,"plugin_root":"/decoy"},"plugin_id":"cloudmanic.herdr-plus","enabled":true,"version":"0.1.16","plugin_root":"/x/herdr-plus"}\n' > "$HERDR_PLUGINS"
  printf '{"plugin_id":"netbja.agentbus","enabled":%s,"version":"0.20.0","plugin_root":"%s","source":{"kind":"local"}}\n' "${2:-true}" "$1" >> "$HERDR_PLUGINS"
}
# A fake docker: the real one runs the live broker (agent-bus-redis) and must never be touched.
# An earlier version of this test stopped it (uninstall --yes stops Redis by default).
# Its container carries the compose label of the checkout that made it, DOCKER_OWNER (the copy
# below unless a test says otherwise): the Go installer composes only for its own.
export DOCKER_LOG="$T/docker.log" DOCKER_STATE="$T/redis.state" DOCKER_OWNER="$T/repo"
echo running > "$DOCKER_STATE"
cat > "$T/fakebin/docker" <<'EOF'
#!/bin/sh
echo "$*" >> "$DOCKER_LOG"
if [ "$1" = inspect ]; then
  [ -s "$DOCKER_STATE" ] || { echo "Error: No such object: agent-bus-redis" >&2; exit 1; }
  echo "$DOCKER_OWNER"; exit 0
fi
case "$1 ${2:-}" in
  "ps -a") cat "$DOCKER_STATE" ;;
  "compose version") echo "Docker Compose version test" ;;
  "compose up") echo running > "$DOCKER_STATE" ;;
  "compose stop") echo exited > "$DOCKER_STATE" ;;
  "compose down") : > "$DOCKER_STATE" ;;
  "info ") [ -n "${DOCKER_INFO_OK:-}" ] || exit 1 ;;
esac
exit 0
EOF
chmod +x "$T/fakebin/docker"
# A fake systemctl (and launchctl): the real user manager runs live project services and must
# never be touched. It answers show-environment, reports units as active, logs everything else.
export SYSTEMCTL_LOG="$T/systemctl.log"
cat > "$T/fakebin/systemctl" <<'EOS'
#!/bin/sh
echo "$*" >> "$SYSTEMCTL_LOG"
case "$*" in
  "--user show-environment") echo "HOME=/x" ;;
  "--user stop "*) [ -n "${SYSTEMCTL_FAIL_STOP:-}" ] && exit 1 ;;
  "--user show "*) printf 'LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=4242\n' ;;
esac
exit 0
EOS
cat > "$T/fakebin/launchctl" <<'EOS'
#!/bin/sh
echo "launchctl $*" >> "$SYSTEMCTL_LOG"
exit 1
EOS
chmod +x "$T/fakebin/systemctl" "$T/fakebin/launchctl"
export PATH="$T/fakebin:$PATH"

# A local mattpocock/skills stand-in, tagged like the pinned release.
src="$T/skills-src"
for s in wayfinder to-tickets to-spec tdd implement code-review diagnosing-bugs codebase-design domain-modeling; do
  mkdir -p "$src/skills/engineering/$s" && echo "# $s" > "$src/skills/engineering/$s/SKILL.md"
done
git -C "$src" init -q && git -C "$src" add -A && git -C "$src" -c user.email=t@t -c user.name=t commit -qm skills && git -C "$src" tag v1.3.1
export SKILLS_REPO="$src"

# A copy of the repository: setup never runs against this checkout.
mkdir -p "$T/repo" "$T/bin"
git -C "$REPO" ls-files -z | (cd "$REPO" && xargs -0 tar cf -) | tar xf - -C "$T/repo"
cp "$REPO/setup.sh" "$T/repo/"
git -C "$T/repo" init -q && git -C "$T/repo" add -A && git -C "$T/repo" -c user.email=t@t -c user.name=t commit -qm copy && git -C "$T/repo" tag v9.9.9
SETUP="$T/repo/$SETUP_SCRIPT"

# Flags
out="$("$SETUP" --help 2>&1 || true)"
assert_contains "$out" "Agent Bus Monitor — setup" "help displays the title"
assert_contains "$out" "./setup.sh uninstall" "help lists uninstall"
assert_contains "$out" "--install-herdr" "help lists --install-herdr"
assert_eq "$([[ "$out" == *HERDR_PLUS* || "$out" == *"herdr-plus"* ]] && echo named || echo gone)" "gone" "help no longer mentions herdr-plus"
assert_exit 0 "help exits 0" -- "$SETUP" --help
assert_exit 1 "unknown flag exits with code 1" -- "$SETUP" install --unknown-flag-test

# The herdr-plus binding an older setup wrote, byte for byte, between two sections of the user's.
OLD_BLOCK='
# herdr-plus Projects picker: prefix (ctrl+b) then Up
[[keys.command]]
key = "prefix+up"
type = "plugin_action"
command = "cloudmanic.herdr-plus.projects"
description = "herdr-plus: projects"'
NEW_BLOCK='
# agentbus Projects picker: prefix (ctrl+b) then Up
[[keys.command]]
key = "prefix+up"
type = "plugin_action"
command = "netbja.agentbus.projects"
description = "agent-bus: projects"'

# A dry run reports and changes nothing
mkdir -p "$T/cfg/herdr"
printf '[ui]\ntheme = "dark"\n' > "$T/cfg/herdr/config.toml"
cp "$T/cfg/herdr/config.toml" "$T/config.orig"
dry_out="$("$SETUP" install --dry-run --bin-dir "$T/dry-bin" 2>&1)"; rc=$?
assert_eq "$rc" "0" "dry-run exits 0"
assert_contains "$dry_out" "would compile ./cmd/agentbus -> $T/dry-bin/agentbus" "dry-run names the target"
assert_contains "$dry_out" "Dry run complete" "dry-run says it is a dry run"
assert_eq "$([[ -e "$T/dry-bin" ]] && echo exists || echo absent)" "absent" "dry-run creates no bin dir"
assert_eq "$(cmp -s "$T/config.orig" "$T/cfg/herdr/config.toml" && echo same)" "same" "dry-run leaves herdr config alone"
assert_contains "$dry_out" "would run: herdr plugin link $T/repo" "dry-run names the plugin link"
assert_eq "$(grep -c 'plugin link' "$HERDR_LOG")" "0" "dry-run links nothing"
assert_eq "$([[ -e "$PLUGIN_RECORD" ]] && echo recorded || echo none)" "none" "dry-run records nothing"

# A real install: binaries rebuilt and stamped, the checkout linked as the herdr plugin, skills cloned
printf '#!/bin/sh\necho stale\n' > "$T/bin/agentbus"; chmod +x "$T/bin/agentbus"
run_setup() { "$SETUP" "$@" --bin-dir "$T/bin" --no-docker 2>&1; }
: > "$HERDR_LOG"
inst_out="$(run_setup install --install-herdr)"; rc=$?
assert_eq "$rc" "0" "install exits 0"
assert_contains "$("$T/bin/agentbus" version 2>&1)" "agentbus version v9.9.9" "stale agentbus rebuilt and stamped with the release"
assert_contains "$("$T/bin/busmon" --version 2>&1)" "busmon version v9.9.9" "busmon stamped too"
assert_contains "$("$T/repo/agentbus" version 2>&1)" "agentbus protocol" "agentbus also built in the repo (the herdr tabs use it)"
assert_contains "$(cat "$HERDR_LOG")" "plugin link $T/repo" "the checkout is linked as the herdr plugin"
assert_eq "$(cat "$PLUGIN_RECORD" 2>/dev/null)" "$T/repo" "the linked checkout is recorded"
assert_eq "$(grep -c 'plugin install' "$HERDR_LOG")" "0" "--install-herdr no longer installs herdr-plus"
assert_contains "$inst_out" "Projects picker action available" "the picker action is checked"
assert_eq "$(readlink "$T/bin/agent-launch" 2>/dev/null)" "$T/repo/scripts/agent-launch" "agent-launch symlink installed into BIN_DIR"
assert_eq "$(readlink "$T/bin/agent-spawn" 2>/dev/null)" "$T/repo/scripts/agent-spawn" "agent-spawn symlink installed into BIN_DIR"
launch_out="$(PATH="$T/bin:$PATH" ROLES_TOML="$T/repo/roles.toml" AGENT_LAUNCH_DRYRUN=1 agent-launch master myproject 2>&1)"
assert_contains "$launch_out" "claude --model" "agent-launch works by its bare name through the installed symlink"
spawn_out="$(PATH="$T/bin:$PATH" ROLES_TOML="$T/repo/roles.toml" HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 agent-spawn coder myproject 2>&1)"
assert_contains "$spawn_out" "$T/repo/scripts/agent-launch coder myproject" "agent-spawn works by its bare name through the installed symlink"
assert_eq "$(cat "$T/cfg/herdr/config.toml")" "$(printf '[ui]\ntheme = "dark"\n%s' "$NEW_BLOCK")" "herdr keybinding added: our block, after the user's config"
assert_eq "$(ls "$T/cfg/herdr" | grep -c '^config.toml.bak.')" "1" "herdr config backed up before the edit"
assert_contains "$(cat "$HERDR_LOG")" "server reload-config" "a running herdr is told to reload its config"
assert_eq "$(git -C "$T/data/agentbus/skills/mattpocock" describe --tags 2>/dev/null)" "v1.3.1" "external skills cloned at the pinned tag"
assert_eq "$([[ -f "$T/data/agentbus/skills/mattpocock/.git/agentbus-setup" ]] && echo ours || echo unmarked)" "ours" "the clone is marked as setup's own"
assert_eq "$(readlink "$HOME/.claude/skills/wayfinder")" "$T/data/agentbus/skills/mattpocock/skills/engineering/wayfinder" "role skills linked to the clone"
assert_contains "$inst_out" "agentbus up myproject" "next steps point at agentbus up"

cp "$T/cfg/herdr/config.toml" "$T/config.after"
: > "$HERDR_LOG"
second_out="$(run_setup install)"; rc=$?
# This step failed once under load in a change suite, unexplained: keep the evidence next time.
[[ "$rc" == 0 ]] || { echo "  second install output (rc=$rc):"; printf "%s\n" "$second_out" | tail -30 | sed "s/^/    /"; }
assert_eq "$rc" "0" "second install exits 0"
assert_eq "$(cmp -s "$T/config.after" "$T/cfg/herdr/config.toml" && echo same)" "same" "second install leaves herdr config alone"
assert_eq "$(grep -c 'plugin link\|plugin unlink' "$HERDR_LOG")" "0" "a plugin already linked here is not linked again"
assert_contains "$second_out" "external skills: v1.3.1" "the skills clone is refreshed in place"

# Registration owned, disabled, moved, foreign: setup touches only what it recorded
set_registry "$T/repo" false; : > "$HERDR_LOG"
dis_out="$(run_setup install --no-skills)"
assert_contains "$dis_out" "disabled" "a plugin linked here but disabled is reported"
assert_eq "$(grep -c 'plugin link\|plugin unlink' "$HERDR_LOG")" "0" "and left disabled: no link, no unlink"
set_registry "$T/old-repo"; printf '%s\n' "$T/old-repo" > "$PLUGIN_RECORD"; : > "$HERDR_LOG"
run_setup install --no-skills >/dev/null
assert_eq "$(grep 'plugin link\|plugin unlink' "$HERDR_LOG" | tr '\n' '|')" "plugin unlink netbja.agentbus|plugin link $T/repo|" "a moved checkout (the recorded path) is unlinked, then this one linked"
assert_eq "$(cat "$PLUGIN_RECORD")" "$T/repo" "and the record follows it"
set_registry "$T/elsewhere"; : > "$HERDR_LOG"
for_out="$(run_setup install --no-skills)"
assert_contains "$for_out" "$T/elsewhere" "a plugin linked from another checkout is reported"
assert_eq "$(grep -c 'plugin link\|plugin unlink' "$HERDR_LOG")" "0" "and never unlinked nor relinked"
assert_eq "$(cat "$PLUGIN_RECORD")" "$T/repo" "the record is not rewritten"

# Unlink succeeds but the relink fails: the warning says the plugin is now not linked
set_registry "$T/old-repo"; printf '%s\n' "$T/old-repo" > "$PLUGIN_RECORD"; : > "$HERDR_LOG"
relink_out="$(HERDR_FAIL_LINK=1 run_setup install --no-skills)"
assert_contains "$relink_out" "is now NOT linked" "a failed relink says the plugin is now not linked"
assert_contains "$relink_out" "re-run ./setup.sh install" "and how to fix it"

# A record that cannot be written is a visible warning, not a silent or fatal one
: > "$HERDR_PLUGINS"; rm -f "$PLUGIN_RECORD"
rec_dir="$(dirname "$PLUGIN_RECORD")"; rm -rf "$rec_dir"; : > "$rec_dir"
rec_out="$(run_setup install --no-skills)"; rec_rc=$?
assert_eq "$rec_rc" "0" "an unwritable record does not fail the install"
assert_contains "$rec_out" "could not record the plugin path" "and is reported"
rm -f "$rec_dir"

# A failed link leaves the key binding unwritten (and records nothing)
: > "$HERDR_PLUGINS"; rm -f "$PLUGIN_RECORD"
printf '[ui]\ntheme = "dark"\n' > "$T/cfg/herdr/config.toml"; cp "$T/cfg/herdr/config.toml" "$T/config.nolink"
nolink_out="$(HERDR_FAIL_LINK=1 run_setup install --no-skills)"
assert_contains "$nolink_out" "herdr plugin link $T/repo failed" "a failed link is reported"
assert_eq "$(cmp -s "$T/config.nolink" "$T/cfg/herdr/config.toml" && echo same)" "same" "and the key binding is not written"
assert_eq "$([[ -e "$PLUGIN_RECORD" ]] && echo recorded || echo none)" "none" "and nothing is recorded"

# The herdr-plus binding an older setup wrote is replaced by ours, byte for byte, with a backup
printf '[ui]\ntheme = "dark"\n%s\n[ui.extra]\nmode = "x"\n' "$OLD_BLOCK" > "$T/cfg/herdr/config.toml"
cp "$T/cfg/herdr/config.toml" "$T/config.old"; rm -f "$T/cfg/herdr/config.toml.bak."*
mig_out="$(run_setup install --no-skills)"
assert_eq "$(cat "$T/cfg/herdr/config.toml")" "$(printf '[ui]\ntheme = "dark"\n%s\n[ui.extra]\nmode = "x"' "$NEW_BLOCK")" "the old herdr-plus block is replaced by ours, the rest kept"
assert_eq "$(cmp -s "$T/config.old" "$T/cfg/herdr/config.toml".bak.* && echo same)" "same" "a backup of the old config is kept"
assert_contains "$mig_out" "herdr-plus" "the migration is reported"
# ... an old block edited by one character is the user's: warned, left as it is
printf '[ui]\ntheme = "dark"\n%s\n' "${OLD_BLOCK/projects\"/project\"}" > "$T/cfg/herdr/config.toml"
cp "$T/cfg/herdr/config.toml" "$T/config.edited"
ed_out="$(run_setup install --no-skills)"
assert_eq "$(cmp -s "$T/config.edited" "$T/cfg/herdr/config.toml" && echo same)" "same" "an edited herdr-plus block is left as it is"
assert_contains "$ed_out" "edited" "and reported"

# A key already bound to prefix+up is not overridden
printf '[[keys.command]]\nkey = "prefix+up"\ntype = "split"\n' > "$T/cfg/herdr/config.toml"
cp "$T/cfg/herdr/config.toml" "$T/config.taken"
taken_out="$(run_setup install --no-skills)"
assert_eq "$(cmp -s "$T/config.taken" "$T/cfg/herdr/config.toml" && echo same)" "same" "taken prefix+up is not overridden"
assert_contains "$taken_out" "prefix+up is already bound" "taken prefix+up is reported"
cp "$T/config.after" "$T/cfg/herdr/config.toml"

# Project services: install says which ones run the previous binary; uninstall removes them first
mkdir -p "$T/cfg/systemd/user" && printf '[Service]\nExecStart="%s/agentbus"\n' "$T/bin" > "$T/cfg/systemd/user/agentbus-demo-dispatch.service"
svc_out="$(run_setup install --no-skills)"
assert_contains "$svc_out" "demo dispatch" "install lists the project services that run the previous binary"
assert_contains "$svc_out" "service restart" "and how to restart them"

# A machine where herdr never ran has no config yet: the binding is still added, in a new file
fresh_cfg="$T/fresh-cfg/herdr/config.toml"; : > "$HERDR_LOG"
HERDR_CONFIG_PATH="$fresh_cfg" run_setup install --no-skills >/dev/null
assert_contains "$(cat "$fresh_cfg" 2>/dev/null)" "netbja.agentbus.projects" "no herdr config yet: one is created with the binding (HERDR_CONFIG_PATH honoured)"
assert_contains "$(cat "$HERDR_LOG")" "server reload-config" "a running herdr is told to reload its config"

# status changes nothing and reports what is there
mkdir -p "$T/state/agentbus" && echo v0.1.16 > "$T/state/agentbus/herdr-plus.ref"; : > "$HERDR_LOG"
status_out="$("$SETUP" status --bin-dir "$T/bin" 2>&1)"; rc=$?
assert_eq "$rc" "0" "status exits 0"
assert_contains "$status_out" "agentbus v9.9.9: $T/bin/agentbus" "status shows the installed version"
assert_contains "$status_out" "agent-launch: $T/bin/agent-launch -> $T/repo/scripts/agent-launch" "status reports agent-launch"
assert_contains "$status_out" "agent-spawn: $T/bin/agent-spawn -> $T/repo/scripts/agent-spawn" "status reports agent-spawn"
assert_contains "$status_out" "external skills: v1.3.1" "status shows the external skills"
assert_contains "$status_out" "netbja.agentbus: linked from $T/repo" "status shows the plugin registration"
assert_contains "$status_out" "Projects picker available" "status shows the picker action"
assert_contains "$status_out" "herdr-plus: installed by an older setup, no longer needed: herdr plugin uninstall cloudmanic.herdr-plus" "a herdr-plus setup installed is reported as no longer needed"
assert_eq "$(grep -c 'plugin uninstall' "$HERDR_LOG")" "0" "and never removed"
# Outside herdr there is no server to list the actions: not a missing action, and no warning
nosrv_out="$(HERDR_NO_SERVER=1 "$SETUP" status --bin-dir "$T/bin" 2>&1)"
assert_contains "$nosrv_out" "Projects picker not checked (no herdr server running here)" "no herdr server: the picker is reported unchecked"
assert_eq "$([[ "$nosrv_out" == *"projects action is missing"* ]] && echo warned || echo quiet)" "quiet" "no herdr server: no false warning"

# uninstall has no dry run: it refuses one rather than remove things while "dry"
assert_exit 1 "uninstall --dry-run is refused" -- "$SETUP" uninstall --yes --dry-run --bin-dir "$T/bin"
assert_eq "$([[ -x "$T/bin/agentbus" ]] && echo kept || echo gone)" "kept" "and removes nothing"
# a SKILLS_DEST with a space is one directory
mkdir -p "$T/My Skills" && ln -s "$T/repo/skills/agent-bus" "$T/My Skills/agent-bus"
# A service that will not stop halts the uninstall: nothing else is removed
: > "$HERDR_LOG"
fail_out="$(SYSTEMCTL_FAIL_STOP=1 $(command -v setsid || true) "$SETUP" uninstall --yes --bin-dir "$T/bin" < /dev/null 2>&1)"; rc=$?
assert_eq "$rc" "1" "uninstall stops when a project service cannot be stopped"
assert_eq "$([[ -x "$T/bin/agentbus" && -e "$T/cfg/systemd/user/agentbus-demo-dispatch.service" ]] && echo kept || echo removed)" "kept" "and keeps the binaries and the unit"
assert_eq "$(grep -c 'plugin unlink' "$HERDR_LOG"):$(grep -c 'netbja.agentbus.projects' "$T/cfg/herdr/config.toml")" "0:1" "and keeps the plugin and its key binding (services first)"
# uninstall without a terminal and without --yes refuses to guess
command -v setsid >/dev/null && assert_exit 1 "uninstall with no terminal and no --yes stops" -- setsid bash -c "'$SETUP' uninstall --bin-dir '$T/bin' < /dev/null"
# uninstall --yes takes the cautious defaults: ours removed, the user's kept
mkdir -p "$HOME/.claude/skills/real-dir"
rm -f "$T/bin/agent-spawn"
ln -s "$T/fakebin/herdr" "$T/bin/agent-spawn" # a user's replacement is not setup's link
: > "$HERDR_LOG"
un_out="$(SKILLS_DEST="$T/My Skills" $(command -v setsid || true) "$SETUP" uninstall --yes --bin-dir "$T/bin" < /dev/null 2>&1)"; rc=$?
assert_eq "$rc" "0" "uninstall --yes exits 0"
assert_eq "$([[ -e "$T/bin/agentbus" || -e "$T/bin/busmon" ]] && echo left || echo gone)" "gone" "our binaries removed"
assert_eq "$([[ -e "$T/bin/agent-launch" || -L "$T/bin/agent-launch" ]] && echo left || echo gone)" "gone" "our agent-launch symlink removed"
assert_eq "$(readlink "$T/bin/agent-spawn" 2>/dev/null)" "$T/fakebin/herdr" "a foreign agent-spawn symlink is preserved"
assert_eq "$([[ -L "$HOME/.claude/skills/wayfinder" ]] && echo left || echo gone)" "gone" "our skill links removed"
assert_eq "$([[ -d "$HOME/.claude/skills/real-dir" ]] && echo kept || echo lost)" "kept" "a real directory is never removed"
assert_eq "$(cat "$T/cfg/herdr/config.toml")" "$(printf '[ui]\ntheme = "dark"')" "the key binding block is removed, the rest of the herdr config kept"
assert_contains "$un_out" "prefix+up is now unbound" "and the summary says prefix+up is unbound"
assert_contains "$(cat "$HERDR_LOG")" "plugin unlink netbja.agentbus" "the plugin setup linked is unlinked"
assert_eq "$([[ -e "$PLUGIN_RECORD" ]] && echo kept || echo removed)" "removed" "and its record removed"
assert_eq "$(grep -c 'plugin uninstall' "$HERDR_LOG")" "0" "herdr-plus is never uninstalled"
assert_eq "$([[ -d "$T/data/agentbus/skills/mattpocock" ]] && echo left || echo gone)" "gone" "the external skills folder removed"
assert_eq "$([[ -x "$T/repo/agentbus" ]] && echo kept || echo lost)" "kept" "the repository's own build is left"
assert_eq "$([[ -e "$T/cfg/systemd/user/agentbus-demo-dispatch.service" ]] && echo left || echo gone)" "gone" "uninstall --yes removes the project services first"
assert_contains "$(cat "$SYSTEMCTL_LOG")" "--user stop agentbus-demo-dispatch.service" "through the (fake) user manager"
assert_contains "$(cat "$DOCKER_LOG")" "compose stop" "uninstall --yes stops Redis (through the fake docker)"
assert_eq "$(grep -c "compose down" "$DOCKER_LOG")" "0" "and never removes its data without --purge"
assert_eq "$([[ -L "$T/My Skills/agent-bus" ]] && echo left || echo gone)" "gone" "a link in a SKILLS_DEST with a space is removed"

# The broker belongs to the checkout whose compose label it carries (Go installer): another
# checkout's is never started, stopped nor purged from here. The install runs in a network
# namespace (unshare -rn), where 127.0.0.1:6380 is unreachable, so it would start the broker;
# the same run with this checkout's label is the control that it does.
if [[ "$SETUP_SCRIPT" != setup.sh ]]; then
  if unshare -rn true 2>/dev/null; then
    : > "$DOCKER_LOG"; : > "$DOCKER_STATE"
    ctl_out="$(DOCKER_INFO_OK=1 unshare -rn "$SETUP" install --bin-dir "$T/bin" --no-skills 2>&1)"
    assert_contains "$(cat "$DOCKER_LOG")" "compose up -d" "control: an unreachable broker with no container is started from this checkout"
    echo running > "$DOCKER_STATE"; : > "$DOCKER_LOG"
    own_out="$(DOCKER_OWNER="$T/other-checkout" DOCKER_INFO_OK=1 unshare -rn "$SETUP" install --bin-dir "$T/bin" --no-skills 2>&1)"
    assert_eq "$(grep -c 'compose up' "$DOCKER_LOG")" "0" "install never starts a broker another checkout owns"
    assert_contains "$own_out" "the broker belongs to $T/other-checkout" "and says whose it is"
  else
    echo "  skip: unshare -rn unavailable (the install ownership asserts need an unreachable 6380)"
  fi
  echo running > "$DOCKER_STATE"; : > "$DOCKER_LOG"
  DOCKER_OWNER="$T/other-checkout" $(command -v setsid || true) "$SETUP" uninstall --yes --purge --bin-dir "$T/bin" < /dev/null >/dev/null 2>&1
  assert_eq "$(grep -c 'compose stop\|compose down' "$DOCKER_LOG")" "0" "uninstall --purge never stops nor purges a broker another checkout owns"
  assert_eq "$(cat "$DOCKER_STATE")" "running" "and leaves it running"
fi

# A plugin linked from a path setup did not record is never unlinked
set_registry "$T/elsewhere"; printf '%s\n' "$T/repo" > "$PLUGIN_RECORD"; : > "$HERDR_LOG"
$(command -v setsid || true) "$SETUP" uninstall --yes --bin-dir "$T/bin" < /dev/null >/dev/null 2>&1
assert_eq "$(grep -c 'plugin unlink' "$HERDR_LOG")" "0" "a foreign registration is not unlinked at uninstall"

# A binding block the user edited is left as it is, and what follows it is never eaten
printf '[ui]\ntheme = "dark"\n%s\n[ui.extra]\nmode = "x"\n' "${NEW_BLOCK/projects\"/project\"}" > "$T/cfg/herdr/config.toml"
ed_out="$($(command -v setsid || true) "$SETUP" uninstall --yes --bin-dir "$T/bin" < /dev/null 2>&1)"
assert_contains "$(cat "$T/cfg/herdr/config.toml")" "[ui.extra]" "an edited binding block never takes the next section with it"
assert_contains "$ed_out" "edited" "and the edited block is reported, not removed"

# A skills folder setup did not create is never removed, nor fetched into
mkdir -p "$T/data/agentbus/skills/mattpocock" && git -C "$T/data/agentbus/skills/mattpocock" init -q && echo mine > "$T/data/agentbus/skills/mattpocock/notes"
own_out="$(run_setup install --no-docker 2>&1)"
assert_contains "$own_out" "not created by setup" "a foreign skills folder is reported at install"
$(command -v setsid || true) "$SETUP" uninstall --yes --bin-dir "$T/bin" < /dev/null >/dev/null 2>&1
assert_eq "$(cat "$T/data/agentbus/skills/mattpocock/notes" 2>/dev/null)" "mine" "and kept at uninstall"
rm -rf "$T/data/agentbus/skills/mattpocock"

# update refuses a dirty tree, then fast-forwards and reinstalls
echo "x" >> "$T/repo/README.md"
assert_exit 1 "update refuses uncommitted changes" -- "$SETUP" update --bin-dir "$T/bin" --no-docker --no-skills
git -C "$T/repo" checkout -q README.md
up_out="$("$SETUP" update --bin-dir "$T/bin" --no-docker --no-skills 2>&1)"; rc=$?
assert_eq "$rc" "1" "update without an upstream fails clearly (no remote in the copy)"
assert_contains "$up_out" "git pull --ff-only failed" "and says why"

# update fast-forwards to a newer release and reinstalls it
# mark_pulled_installer: the pulled tree's installer prints one more line, so the update must run it.
mark_pulled_installer() {
  if [[ "$SETUP_SCRIPT" == setup.sh ]]; then
    sed -i "s|^log_step \"1/5 Checking system platform & toolchain\"$|&\nlog_info \"the pulled installer is the one running\"|" "$T/dev/setup.sh" && grep -q "pulled installer" "$T/dev/setup.sh"
  else
    sed -i 's|^\to.Step("1/5 Checking system platform & toolchain")$|&; o.Info("the pulled installer is the one running")|' "$T/dev/setup/preflight.go" && grep -q "pulled installer" "$T/dev/setup/preflight.go"
  fi
}
git clone -q --bare "$T/repo" "$T/upstream.git" && git -C "$T/repo" remote add origin "$T/upstream.git" && git -C "$T/repo" fetch -q origin && git -C "$T/repo" branch -q -u origin/$(git -C "$T/repo" rev-parse --abbrev-ref HEAD)
git clone -q "$T/upstream.git" "$T/dev" && echo more >> "$T/dev/CHANGELOG.md" && echo "- one more rule" >> "$T/dev/skills/agent-bus-sentinel/SKILL.md" && mark_pulled_installer && git -C "$T/dev" add -A && git -C "$T/dev" -c user.email=t@t -c user.name=t commit -qm next && git -C "$T/dev" tag v9.9.10 && git -C "$T/dev" push -q origin HEAD --tags
up_out="$("$SETUP" update --bin-dir "$T/bin" --no-docker --no-skills 2>&1)"; rc=$?
[[ "$rc" == 0 ]] || printf "%s\n" "$up_out" | tail -15 | sed "s/^/    /"
assert_eq "$rc" "0" "update exits 0"
assert_contains "$up_out" "updated v9.9.9 -> v9.9.10" "update reports vA -> vB"
assert_contains "$up_out" "skills changed: agent-bus-sentinel" "update names the skills that changed"
assert_contains "$up_out" "agentbus roles skill-users agent-bus-sentinel" "and how master finds the agents to tell"
assert_contains "$("$T/bin/agentbus" version)" "agentbus version v9.9.10" "and installs the new release"
assert_contains "$up_out" "the pulled installer is the one running" "update runs the installer it pulled, not the one in memory"
# Go older than go.mod, with no automatic toolchain switch: refused
mkdir -p "$T/oldgo"
cat > "$T/oldgo/go" <<'EOF'
#!/bin/sh
case "$*" in
  "env GOVERSION") echo go1.20.1 ;;
  "env GOTOOLCHAIN") echo local ;;
  *) echo "go version go1.20.1 linux/amd64" ;;
esac
EOF
chmod +x "$T/oldgo/go"
want="$(sed -n 's/^go //p' "$REPO/go.mod")"
old_out="$(PATH="$T/oldgo:$PATH" "$SETUP" install --dry-run --bin-dir "$T/dry-bin" 2>&1)"; rc=$?
assert_eq "$rc" "1" "old Go is refused"
assert_contains "$old_out" "$want" "the required Go version is named"

finish
