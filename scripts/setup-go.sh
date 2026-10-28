#!/bin/sh
#
# Agent Bus Monitor — setup, the Go installer's thin front (rollout step 1 of
# docs/superpowers/specs/2026-10-28-go-installer-design.md; ./setup.sh stays
# the installer until the switch). It checks git and Go, builds agentbus from
# this checkout, and hands every argument to `agentbus setup`.
#
set -eu

REPO_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

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

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM
# Unstamped: this binary only runs setup, which builds the stamped ones.
if ! (cd "$REPO_DIR" && go build -o "$TMP_DIR/agentbus" ./cmd/agentbus) >"$TMP_DIR/build.log" 2>&1; then
  tail -n 15 "$TMP_DIR/build.log" | sed 's/^/      /' >&2
  fatal "could not build agentbus to run the installer (see above)"
fi
"$TMP_DIR/agentbus" setup --repo "$REPO_DIR" "$@"
