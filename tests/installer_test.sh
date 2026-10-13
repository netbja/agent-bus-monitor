#!/usr/bin/env bash
# Test setup.sh (and its install.sh alias): flags, a dry run that changes nothing, a real install
# into a copy of the repository (stale binaries replaced, version stamped, idempotent herdr
# config edit, herdr-plus registered through herdr, external skills cloned), status, uninstall
# and the Go floor. Hermetic: a fake herdr, a local skills repository, a throwaway HOME; it never
# touches this checkout, the live BIN_DIR, herdr, docker or the real skills.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
# Keep Go's caches where they are: a throwaway HOME must not mean a cold build.
export GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOPATH="$(go env GOPATH)"
export NO_COLOR=1 HOME="$T/home" XDG_CONFIG_HOME="$T/cfg" XDG_DATA_HOME="$T/data" HERDR_PLUS_PATH="$T/no-herdr-plus"
mkdir -p "$HOME"

# A fake herdr: records calls; the plugin is "registered" once installed or linked.
mkdir -p "$T/fakebin"
export HERDR_LOG="$T/herdr.log" HERDR_STATE="$T/herdr-plus.registered"
cat > "$T/fakebin/herdr" <<'EOF'
#!/bin/sh
echo "$*" >> "$HERDR_LOG"
case "$1 ${2:-}" in
  "plugin list") if [ -f "$HERDR_STATE" ]; then echo "- cloudmanic.herdr-plus (Herdr Plus) enabled [$(cat "$HERDR_STATE")]"; else echo "0 plugins installed"; fi ;;
  "plugin install") echo "github" > "$HERDR_STATE" ;;
  "plugin link") echo "local:$3" > "$HERDR_STATE" ;;
  "plugin uninstall"|"plugin unlink") rm -f "$HERDR_STATE" ;;
  "plugin action") [ -f "$HERDR_STATE" ] && echo '{"result":{"actions":[{"action_id":"projects","command":["x"],"plugin_id":"cloudmanic.herdr-plus"}]}}' ;;
  "--version ") echo "herdr 0.0-test" ;;
esac
exit 0
EOF
chmod +x "$T/fakebin/herdr"
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
cp "$REPO/setup.sh" "$REPO/install.sh" "$T/repo/"
git -C "$T/repo" init -q && git -C "$T/repo" add -A && git -C "$T/repo" -c user.email=t@t -c user.name=t commit -qm copy && git -C "$T/repo" tag v9.9.9
SETUP="$T/repo/setup.sh"

# Flags
out="$("$SETUP" --help 2>&1 || true)"
assert_contains "$out" "Agent Bus Monitor — setup" "help displays the title"
assert_contains "$out" "./setup.sh uninstall" "help lists uninstall"
assert_contains "$out" "--install-herdr" "help lists --install-herdr"
assert_exit 0 "help exits 0" -- "$SETUP" --help
assert_exit 1 "unknown flag exits with code 1" -- "$SETUP" install --unknown-flag-test

# A dry run reports and changes nothing
mkdir -p "$T/cfg/herdr"
printf '[ui]\ntheme = "dark"\n' > "$T/cfg/herdr/config.toml"
cp "$T/cfg/herdr/config.toml" "$T/config.orig"
dry_out="$("$T/repo/install.sh" --dry-run --bin-dir "$T/dry-bin" 2>&1)"; rc=$?
assert_eq "$rc" "0" "dry-run (through the install.sh alias) exits 0"
assert_contains "$dry_out" "would compile ./cmd/agentbus -> $T/dry-bin/agentbus" "dry-run names the target"
assert_contains "$dry_out" "Dry run complete" "dry-run says it is a dry run"
assert_eq "$([[ -e "$T/dry-bin" ]] && echo exists || echo absent)" "absent" "dry-run creates no bin dir"
assert_eq "$(cmp -s "$T/config.orig" "$T/cfg/herdr/config.toml" && echo same)" "same" "dry-run leaves herdr config alone"
assert_contains "$dry_out" "herdr-plus is not installed" "an unregistered herdr-plus is reported"
assert_contains "$dry_out" "herdr plugin install cloudmanic/herdr-plus --ref" "and the herdr command is named"
assert_eq "$([[ -e "$HERDR_STATE" ]] && echo registered || echo none)" "none" "dry-run registers nothing"

# A real install: binaries rebuilt and stamped, herdr-plus through herdr, external skills cloned
printf '#!/bin/sh\necho stale\n' > "$T/bin/agentbus"; chmod +x "$T/bin/agentbus"
run_setup() { "$SETUP" "$@" --bin-dir "$T/bin" --no-docker 2>&1; }
inst_out="$(run_setup install --install-herdr)"; rc=$?
assert_eq "$rc" "0" "install exits 0"
assert_contains "$("$T/bin/agentbus" version 2>&1)" "agentbus version v9.9.9" "stale agentbus rebuilt and stamped with the release"
assert_contains "$("$T/bin/busmon" --version 2>&1)" "busmon version v9.9.9" "busmon stamped too"
assert_contains "$("$T/repo/agentbus" version 2>&1)" "agentbus protocol" "agentbus also built in the repo (the herdr tabs use it)"
assert_contains "$(cat "$HERDR_LOG")" "plugin install cloudmanic/herdr-plus --ref v0.1.16 --yes" "herdr-plus installed through herdr, pinned"
assert_contains "$inst_out" "Projects picker action available" "the picker action is checked"
assert_contains "$(cat "$T/cfg/herdr/config.toml")" "cloudmanic.herdr-plus.projects" "herdr keybinding added"
assert_eq "$(ls "$T/cfg/herdr" | grep -c '^config.toml.bak.')" "1" "herdr config backed up before the edit"
assert_eq "$(git -C "$T/data/agentbus/skills/mattpocock" describe --tags 2>/dev/null)" "v1.3.1" "external skills cloned at the pinned tag"
assert_eq "$(readlink "$HOME/.claude/skills/wayfinder")" "$T/data/agentbus/skills/mattpocock/skills/engineering/wayfinder" "role skills linked to the clone"
assert_contains "$inst_out" "agentbus up myproject" "next steps point at agentbus up"

cp "$T/cfg/herdr/config.toml" "$T/config.after"
: > "$HERDR_LOG"
second_out="$(run_setup install)"; rc=$?
# This step failed once under load in a change suite, unexplained: keep the evidence next time.
[[ "$rc" == 0 ]] || { echo "  second install output (rc=$rc):"; printf "%s\n" "$second_out" | tail -30 | sed "s/^/    /"; }
assert_eq "$rc" "0" "second install exits 0"
assert_eq "$(cmp -s "$T/config.after" "$T/cfg/herdr/config.toml" && echo same)" "same" "second install leaves herdr config alone"
assert_eq "$(grep -c 'plugin install' "$HERDR_LOG")" "0" "a registered herdr-plus is not installed again"
assert_contains "$second_out" "external skills: v1.3.1" "the skills clone is refreshed in place"

# A local herdr-plus checkout is linked, not downloaded
rm -f "$HERDR_STATE"; : > "$HERDR_LOG"
mkdir -p "$T/hp/bin" && printf '#!/bin/sh\n' > "$T/hp/bin/herdr-plus" && chmod +x "$T/hp/bin/herdr-plus"
HERDR_PLUS_PATH="$T/hp" "$SETUP" install --bin-dir "$T/bin" --no-docker --no-skills >/dev/null 2>&1
assert_contains "$(cat "$HERDR_LOG")" "plugin link $T/hp" "a local checkout is linked into herdr"

# A key already bound to prefix+up is not overridden
cp "$T/cfg/herdr/config.toml" "$T/config.ours"
printf '[[keys.command]]\nkey = "prefix+up"\ntype = "split"\n' > "$T/cfg/herdr/config.toml"
cp "$T/cfg/herdr/config.toml" "$T/config.taken"
taken_out="$(run_setup install --no-skills)"
assert_eq "$(cmp -s "$T/config.taken" "$T/cfg/herdr/config.toml" && echo same)" "same" "taken prefix+up is not overridden"
assert_contains "$taken_out" "prefix+up is already bound" "taken prefix+up is reported"
cp "$T/config.ours" "$T/cfg/herdr/config.toml"

# status changes nothing and reports what is there
status_out="$("$SETUP" status --bin-dir "$T/bin" 2>&1)"; rc=$?
assert_eq "$rc" "0" "status exits 0"
assert_contains "$status_out" "agentbus v9.9.9: $T/bin/agentbus" "status shows the installed version"
assert_contains "$status_out" "external skills: v1.3.1" "status shows the external skills"
assert_contains "$status_out" "Projects picker available" "status shows herdr-plus"

# uninstall without a terminal and without --yes refuses to guess
command -v setsid >/dev/null && assert_exit 1 "uninstall with no terminal and no --yes stops" -- setsid bash -c "'$SETUP' uninstall --bin-dir '$T/bin' < /dev/null"
# uninstall --yes takes the cautious defaults: ours removed, herdr-plus kept
mkdir -p "$HOME/.claude/skills/real-dir"
un_out="$($(command -v setsid || true) "$SETUP" uninstall --yes --bin-dir "$T/bin" < /dev/null 2>&1)"; rc=$?
assert_eq "$rc" "0" "uninstall --yes exits 0"
assert_eq "$([[ -e "$T/bin/agentbus" || -e "$T/bin/busmon" ]] && echo left || echo gone)" "gone" "our binaries removed"
assert_eq "$([[ -L "$HOME/.claude/skills/wayfinder" ]] && echo left || echo gone)" "gone" "our skill links removed"
assert_eq "$([[ -d "$HOME/.claude/skills/real-dir" ]] && echo kept || echo lost)" "kept" "a real directory is never removed"
assert_eq "$(grep -c 'cloudmanic.herdr-plus.projects' "$T/cfg/herdr/config.toml")" "0" "the key binding block is removed"
assert_contains "$(cat "$T/cfg/herdr/config.toml")" 'theme = "dark"' "the rest of the herdr config is kept"
assert_eq "$([[ -e "$HERDR_STATE" ]] && echo kept || echo removed)" "kept" "herdr-plus kept by default (useful without agent-bus)"
assert_eq "$([[ -d "$T/data/agentbus/skills/mattpocock" ]] && echo left || echo gone)" "gone" "the external skills folder removed"
assert_eq "$([[ -x "$T/repo/agentbus" ]] && echo kept || echo lost)" "kept" "the repository's own build is left"

# update refuses a dirty tree, then fast-forwards and reinstalls
echo "x" >> "$T/repo/README.md"
assert_exit 1 "update refuses uncommitted changes" -- "$SETUP" update --bin-dir "$T/bin" --no-docker --no-skills
git -C "$T/repo" checkout -q README.md
up_out="$("$SETUP" update --bin-dir "$T/bin" --no-docker --no-skills 2>&1)"; rc=$?
assert_eq "$rc" "1" "update without an upstream fails clearly (no remote in the copy)"
assert_contains "$up_out" "git pull --ff-only failed" "and says why"

# update fast-forwards to a newer release and reinstalls it
git clone -q --bare "$T/repo" "$T/upstream.git" && git -C "$T/repo" remote add origin "$T/upstream.git" && git -C "$T/repo" fetch -q origin && git -C "$T/repo" branch -q -u origin/$(git -C "$T/repo" rev-parse --abbrev-ref HEAD)
git clone -q "$T/upstream.git" "$T/dev" && echo more >> "$T/dev/CHANGELOG.md" && git -C "$T/dev" add -A && git -C "$T/dev" -c user.email=t@t -c user.name=t commit -qm next && git -C "$T/dev" tag v9.9.10 && git -C "$T/dev" push -q origin HEAD --tags
up_out="$("$SETUP" update --bin-dir "$T/bin" --no-docker --no-skills 2>&1)"; rc=$?
[[ "$rc" == 0 ]] || printf "%s\n" "$up_out" | tail -15 | sed "s/^/    /"
assert_eq "$rc" "0" "update exits 0"
assert_contains "$up_out" "updated v9.9.9 -> v9.9.10" "update reports vA -> vB"
assert_contains "$("$T/bin/agentbus" version)" "agentbus version v9.9.10" "and installs the new release"
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
