#!/usr/bin/env bash
# Test setup.sh: flags, a dry run that changes nothing, a real install
# into a copy of the repository (stale binaries replaced, version stamped, external skills
# cloned), status, uninstall and the Go floor. Hermetic: a fake herdr that only logs (setup never
# calls it since the herdr exit), a local skills repository, a throwaway HOME; it never touches
# this checkout, the live BIN_DIR, docker or the real skills.
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

# A fake herdr: setup never calls it since the herdr exit; every call is logged, and the log
# must stay empty (checked at the end).
mkdir -p "$T/fakebin"
export HERDR_LOG="$T/herdr.log"
printf '#!/bin/sh\necho "$*" >> "$HERDR_LOG"\n' > "$T/fakebin/herdr"; chmod +x "$T/fakebin/herdr"
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
# SELF is how the installer under test names itself in what it tells the person to run: the
# fallback never sends them back to ./setup.sh, the Go installer they fell back from (reviewer-codex).
SELF=./setup.sh

# Flags
out="$("$SETUP" --help 2>&1 || true)"
assert_contains "$out" "Agent Bus Monitor — setup" "help displays the title"
assert_contains "$out" "$SELF uninstall" "help lists uninstall"
assert_eq "$([[ "$out" == *install.sh* ]] && echo named || echo gone)" "gone" "help no longer mentions install.sh (master: the alias never existed)"
# Every script the help names exists (reviewer-codex: it once advertised a ./install.sh alias that never did).
for named in $(printf '%s\n' "$out" | grep -oE '(\./|scripts/)[A-Za-z0-9_./-]+\.sh' | sort -u); do
  assert_eq "$([[ -f "$T/repo/${named#./}" ]] && echo exists || echo "missing $named")" "exists" "help names $named, which exists"
done
if [[ "$SELF" != ./setup.sh ]]; then
  assert_eq "$([[ "$out" == *"./setup.sh"* ]] && echo named || echo self)" "self" "the fallback's help never names ./setup.sh"
fi
assert_eq "$([[ "$out" == *--install-herdr* ]] && echo named || echo gone)" "gone" "help no longer offers --install-herdr"
assert_eq "$([[ "$out" == *HERDR_PLUS* || "$out" == *"herdr-plus"* ]] && echo named || echo gone)" "gone" "help no longer mentions herdr-plus"
assert_exit 0 "help exits 0" -- "$SETUP" --help
assert_exit 1 "unknown flag exits with code 1" -- "$SETUP" install --unknown-flag-test
if true; then
  # The preview's name before the switch still runs it (an update started from it re-execs it),
  # from any directory; --force's warning is the Go installer's only.
  shim_out="$(cd "$T" && "$T/repo/scripts/setup-go.sh" install --dry-run --force --bin-dir "$T/dry-bin" 2>&1)"
  assert_contains "$shim_out" "--force has had no effect since v0.20" "scripts/setup-go.sh runs the Go installer"
fi

# A dry run reports and changes nothing
dry_out="$("$SETUP" install --dry-run --bin-dir "$T/dry-bin" 2>&1)"; rc=$?
assert_eq "$rc" "0" "dry-run exits 0"
assert_contains "$dry_out" "would compile ./cmd/agentbus -> $T/dry-bin/agentbus" "dry-run names the target"
assert_contains "$dry_out" "Dry run complete" "dry-run says it is a dry run"
assert_eq "$([[ -e "$T/dry-bin" ]] && echo exists || echo absent)" "absent" "dry-run creates no bin dir"

# A real install: binaries rebuilt and stamped, skills cloned; an older setup's agent-spawn link removed
printf '#!/bin/sh\necho stale\n' > "$T/bin/agentbus"; chmod +x "$T/bin/agentbus"
run_setup() { "$SETUP" "$@" --bin-dir "$T/bin" --no-docker 2>&1; }
ln -s "$T/repo/scripts/agent-spawn" "$T/bin/agent-spawn"
inst_out="$(run_setup install)"; rc=$?
assert_eq "$rc" "0" "install exits 0"
assert_contains "$inst_out" "Keep it current: $SELF update" "the summary names the installer that ran"
assert_contains "$("$T/bin/agentbus" version 2>&1)" "agentbus version v9.9.9" "stale agentbus rebuilt and stamped with the release"
assert_contains "$("$T/bin/busmon" --version 2>&1)" "busmon version v9.9.9" "busmon stamped too"
assert_contains "$("$T/repo/agentbus" version 2>&1)" "agentbus protocol" "agentbus also built in the repo"
assert_eq "$(readlink "$T/bin/agent-launch" 2>/dev/null)" "$T/repo/scripts/agent-launch" "agent-launch symlink installed into BIN_DIR"
assert_eq "$([[ -L "$T/bin/agent-spawn" ]] && echo left || echo gone)" "gone" "an older setup's agent-spawn link is removed (agentbus pop is its successor)"
launch_out="$(PATH="$T/bin:$PATH" ROLES_TOML="$T/repo/roles.toml" AGENT_LAUNCH_DRYRUN=1 agent-launch master myproject 2>&1)"
assert_contains "$launch_out" "claude --model" "agent-launch works by its bare name through the installed symlink"
assert_eq "$(git -C "$T/data/agentbus/skills/mattpocock" describe --tags 2>/dev/null)" "v1.3.1" "external skills cloned at the pinned tag"
assert_eq "$([[ -f "$T/data/agentbus/skills/mattpocock/.git/agentbus-setup" ]] && echo ours || echo unmarked)" "ours" "the clone is marked as setup's own"
assert_eq "$(readlink "$HOME/.claude/skills/wayfinder")" "$T/data/agentbus/skills/mattpocock/skills/engineering/wayfinder" "role skills linked to the clone"
assert_contains "$inst_out" "agentbus up myproject" "next steps point at agentbus up"

second_out="$(run_setup install)"; rc=$?
# This step failed once under load in a change suite, unexplained: keep the evidence next time.
[[ "$rc" == 0 ]] || { echo "  second install output (rc=$rc):"; printf "%s\n" "$second_out" | tail -30 | sed "s/^/    /"; }
assert_eq "$rc" "0" "second install exits 0"
assert_contains "$second_out" "external skills: v1.3.1" "the skills clone is refreshed in place"

# Project services: install says which ones run the previous binary; uninstall removes them first
mkdir -p "$T/cfg/systemd/user" && printf '[Service]\nExecStart="%s/agentbus"\n' "$T/bin" > "$T/cfg/systemd/user/agentbus-demo-dispatch.service"
svc_out="$(run_setup install --no-skills)"
assert_contains "$svc_out" "demo dispatch" "install lists the project services that run the previous binary"
assert_contains "$svc_out" "service restart" "and how to restart them"

# status changes nothing and reports what is there
status_out="$("$SETUP" status --bin-dir "$T/bin" 2>&1)"; rc=$?
if [[ "$SELF" != ./setup.sh ]]; then
  assert_eq "$([[ "$inst_out$status_out" == *"./setup.sh"* ]] && echo named || echo self)" "self" "the fallback's install and status never send the person to ./setup.sh"
fi
assert_eq "$rc" "0" "status exits 0"
assert_contains "$status_out" "agentbus v9.9.9: $T/bin/agentbus" "status shows the installed version"
assert_contains "$status_out" "agent-launch: $T/bin/agent-launch -> $T/repo/scripts/agent-launch" "status reports agent-launch"
assert_contains "$status_out" "external skills: v1.3.1" "status shows the external skills"
# uninstall has no dry run: it refuses one rather than remove things while "dry"
assert_exit 1 "uninstall --dry-run is refused" -- "$SETUP" uninstall --yes --dry-run --bin-dir "$T/bin"
assert_eq "$([[ -x "$T/bin/agentbus" ]] && echo kept || echo gone)" "kept" "and removes nothing"
# a SKILLS_DEST with a space is one directory
mkdir -p "$T/My Skills" && ln -s "$T/repo/skills/agent-bus" "$T/My Skills/agent-bus"
# A service that will not stop halts the uninstall: nothing else is removed
fail_out="$(SYSTEMCTL_FAIL_STOP=1 $(command -v setsid || true) "$SETUP" uninstall --yes --bin-dir "$T/bin" < /dev/null 2>&1)"; rc=$?
assert_eq "$rc" "1" "uninstall stops when a project service cannot be stopped"
assert_eq "$([[ -x "$T/bin/agentbus" && -e "$T/cfg/systemd/user/agentbus-demo-dispatch.service" ]] && echo kept || echo removed)" "kept" "and keeps the binaries and the unit"
# uninstall without a terminal and without --yes refuses to guess
command -v setsid >/dev/null && assert_exit 1 "uninstall with no terminal and no --yes stops" -- setsid bash -c "'$SETUP' uninstall --bin-dir '$T/bin' < /dev/null"
# uninstall --yes takes the cautious defaults: ours removed, the user's kept
mkdir -p "$HOME/.claude/skills/real-dir"
rm -f "$T/bin/agent-spawn"
ln -s "$T/fakebin/docker" "$T/bin/agent-spawn" # a user's replacement is not setup's link
un_out="$(SKILLS_DEST="$T/My Skills" $(command -v setsid || true) "$SETUP" uninstall --yes --bin-dir "$T/bin" < /dev/null 2>&1)"; rc=$?
assert_eq "$rc" "0" "uninstall --yes exits 0"
assert_eq "$([[ -e "$T/bin/agentbus" || -e "$T/bin/busmon" ]] && echo left || echo gone)" "gone" "our binaries removed"
assert_eq "$([[ -e "$T/bin/agent-launch" || -L "$T/bin/agent-launch" ]] && echo left || echo gone)" "gone" "our agent-launch symlink removed"
assert_eq "$(readlink "$T/bin/agent-spawn" 2>/dev/null)" "$T/fakebin/docker" "a foreign agent-spawn symlink is preserved"
assert_eq "$([[ -L "$HOME/.claude/skills/wayfinder" ]] && echo left || echo gone)" "gone" "our skill links removed"
assert_eq "$([[ -d "$HOME/.claude/skills/real-dir" ]] && echo kept || echo lost)" "kept" "a real directory is never removed"
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
if true; then
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
  sed -i 's|^\to.Step("1/4 Checking system platform & toolchain")$|&; o.Info("the pulled installer is the one running")|' "$T/dev/setup/preflight.go" && grep -q "pulled installer" "$T/dev/setup/preflight.go"
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


assert_eq "$(cat "$HERDR_LOG" 2>/dev/null)" "" "setup never calls herdr (herdr exit)"
finish
