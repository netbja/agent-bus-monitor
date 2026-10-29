#!/bin/sh
#
# Agent Bus Monitor — setup: the installer's thin front (rollout step 2 of
# docs/superpowers/specs/2026-10-28-go-installer-design.md: the switch). It
# checks git and Go, builds agentbus from this checkout, and hands every
# argument to `agentbus setup`, the installer in Go. The bash installer it
# replaces stays one release as scripts/setup-legacy.sh, the fallback.
#
set -eu

REPO_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

if [ -t 2 ] && [ -z "${NO_COLOR:-}" ]; then
  RED="$(printf '\033[31m')"; RESET="$(printf '\033[0m')"
else
  RED=""; RESET=""
fi
fatal() { printf "\n%serror:%s %s\n" "$RED" "$RESET" "$1" >&2; exit 1; }

[ -f "$REPO_DIR/go.mod" ] && [ -d "$REPO_DIR/cmd/agentbus" ] ||
  fatal "setup.sh must run from a clone of agent-bus-monitor (no go.mod next to it in $REPO_DIR)"
command -v git >/dev/null 2>&1 || fatal "git is required but not installed."
GO_WANT="$(sed -n 's/^go //p' "$REPO_DIR/go.mod" | head -n 1)"
command -v go >/dev/null 2>&1 ||
  fatal "Go $GO_WANT or newer is required to build busmon and agentbus: https://go.dev/dl/"

# version_ge A B: setup.sh's, dotted A >= B (non-numeric suffixes such as rc1 are ignored).
version_ge() {
  va="$1"
  vb="$2"
  while [ -n "$va" ] || [ -n "$vb" ]; do
    xa="${va%%.*}"
    xb="${vb%%.*}"
    if [ "$va" = "$xa" ]; then va=""; else va="${va#*.}"; fi
    if [ "$vb" = "$xb" ]; then vb=""; else vb="${vb#*.}"; fi
    xa="$(printf '%s' "$xa" | sed 's/[^0-9].*//')"
    xb="$(printf '%s' "$xb" | sed 's/[^0-9].*//')"
    xa="${xa:-0}"
    xb="${xb:-0}"
    [ "$xa" -gt "$xb" ] && return 0
    [ "$xa" -lt "$xb" ] && return 1
  done
  return 0
}
# An old Go that may not download a newer toolchain cannot build the installer: refuse with
# setup.sh's words before building (the Go preflight runs only after the build).
GO_HAVE="$(GOTOOLCHAIN=local go env GOVERSION 2>/dev/null | sed 's/^go//; s/ .*//')"
if ! { [ -n "$GO_HAVE" ] && version_ge "$GO_HAVE" "$GO_WANT"; } && [ "$(go env GOTOOLCHAIN 2>/dev/null)" = "local" ]; then
  fatal "Go $GO_WANT or newer is required (found ${GO_HAVE:-unknown}, GOTOOLCHAIN=local): https://go.dev/dl/"
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM
# Unstamped: this binary only runs setup, which builds the stamped ones.
if ! (cd "$REPO_DIR" && go build -o "$TMP_DIR/agentbus" ./cmd/agentbus) >"$TMP_DIR/build.log" 2>&1; then
  tail -n 15 "$TMP_DIR/build.log" | sed 's/^/      /' >&2
  fatal "could not build agentbus to run the installer (see above)"
fi
# update execs this script again after the pull, so the pulled tree builds the installer that runs.
AGENTBUS_SETUP_SCRIPT="$REPO_DIR/setup.sh" "$TMP_DIR/agentbus" setup --repo "$REPO_DIR" "$@"
