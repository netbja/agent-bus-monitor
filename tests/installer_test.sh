#!/usr/bin/env bash
# Test install.sh: flags, a dry run that changes nothing, a real install into a copy of the
# repository (stale binaries replaced, idempotent herdr config edit), and the Go floor.
# The real install never touches this checkout, the live BIN_DIR, docker or the skills.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

INSTALLER="$REPO/install.sh"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
export NO_COLOR=1 XDG_CONFIG_HOME="$T/cfg" HERDR_PLUS_PATH="$T/no-herdr-plus"

# Flags
out="$("$INSTALLER" --help 2>&1 || true)"
assert_contains "$out" "Automated Idempotent Installer" "help displays title"
assert_contains "$out" "--install-herdr" "help lists --install-herdr"
assert_exit 0 "help exits 0" -- "$INSTALLER" --help
assert_exit 1 "unknown flag exits with code 1" -- "$INSTALLER" --unknown-flag-test

# A dry run reports and changes nothing: no bin dir, no herdr config edit
mkdir -p "$T/cfg/herdr"
printf '[ui]\ntheme = "dark"\n' > "$T/cfg/herdr/config.toml"
cp "$T/cfg/herdr/config.toml" "$T/config.orig"
dry_out="$("$INSTALLER" --dry-run --bin-dir "$T/dry-bin" 2>&1)"; rc=$?
assert_eq "$rc" "0" "dry-run exits 0"
assert_contains "$dry_out" "would compile ./cmd/agentbus -> $T/dry-bin/agentbus" "dry-run names the target"
assert_contains "$dry_out" "Dry run complete" "dry-run says it is a dry run"
assert_eq "$([[ -e "$T/dry-bin" ]] && echo exists || echo absent)" "absent" "dry-run creates no bin dir"
assert_eq "$(cmp -s "$T/config.orig" "$T/cfg/herdr/config.toml" && echo same)" "same" "dry-run leaves herdr config alone"
assert_contains "$dry_out" "herdr-plus not found" "missing herdr-plus is reported"
assert_contains "$dry_out" "--install-herdr" "and the opt-in flag is named"

# A real install into a copy of the repository
mkdir -p "$T/repo" "$T/bin"
git -C "$REPO" ls-files -z | (cd "$REPO" && xargs -0 tar cf -) | tar xf - -C "$T/repo"
cp "$INSTALLER" "$T/repo/install.sh"
printf '#!/bin/sh\necho stale\n' > "$T/bin/agentbus"; chmod +x "$T/bin/agentbus"
run_install() { "$T/repo/install.sh" --bin-dir "$T/bin" --no-docker --no-skills 2>&1; }

inst_out="$(run_install)"; rc=$?
assert_eq "$rc" "0" "install exits 0"
assert_contains "$("$T/bin/agentbus" version 2>&1)" "agentbus protocol" "stale agentbus in BIN_DIR is rebuilt"
assert_contains "$("$T/bin/busmon" --help 2>&1)" "project" "busmon installed in BIN_DIR"
assert_contains "$("$T/repo/agentbus" version 2>&1)" "agentbus protocol" "agentbus also built in the repo (bootstrap tabs use it)"
assert_contains "$(cat "$T/cfg/herdr/config.toml")" "cloudmanic.herdr-plus.projects" "herdr keybinding added"
assert_eq "$(ls "$T/cfg/herdr" | grep -c '^config.toml.bak.')" "1" "herdr config backed up before the edit"
assert_contains "$inst_out" "warning" "warnings are counted in the summary"

cp "$T/cfg/herdr/config.toml" "$T/config.after"
run_install >/dev/null; rc=$?
assert_eq "$rc" "0" "second install exits 0"
assert_eq "$(cmp -s "$T/config.after" "$T/cfg/herdr/config.toml" && echo same)" "same" "second install leaves herdr config alone"

# A key already bound to prefix+up is not overridden
printf '[[keys.command]]\nkey = "prefix+up"\ntype = "split"\n' > "$T/cfg/herdr/config.toml"
cp "$T/cfg/herdr/config.toml" "$T/config.taken"
taken_out="$(run_install)"
assert_eq "$(cmp -s "$T/config.taken" "$T/cfg/herdr/config.toml" && echo same)" "same" "taken prefix+up is not overridden"
assert_contains "$taken_out" "prefix+up is already bound" "taken prefix+up is reported"

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
old_out="$(PATH="$T/oldgo:$PATH" "$INSTALLER" --dry-run --bin-dir "$T/dry-bin" 2>&1)"; rc=$?
assert_eq "$rc" "1" "old Go is refused"
assert_contains "$old_out" "$want" "the required Go version is named"

finish
