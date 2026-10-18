#!/bin/sh
#
# Agent Bus Monitor — setup: install, update, uninstall, status
#
#   ./setup.sh install   [options]   preflight, build agentbus + busmon, external skills, herdr-plus, Redis
#   ./setup.sh update    [options]   git pull --ff-only, then install (prints vA -> vB and what changed)
#   ./setup.sh uninstall [--yes] [--purge]   remove what install added, asking at every step
#   ./setup.sh status                what is installed, where, and whether it runs
#
# Run it from a clone of this repository; piping it into sh is not supported.
# ./install.sh is kept as an alias of ./setup.sh install.
#
set -eu

# Script directory and repository root
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_DIR="$SCRIPT_DIR"

# Defaults
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
# A local herdr-plus checkout to link (HERDR_PLUS_PATH, or the historical ~/Tools path if it exists);
# otherwise --install-herdr installs it through herdr's own plugin store.
HERDR_PLUS_DIR="${HERDR_PLUS_PATH:-$HOME/Tools/herdr-plugins/herdr-plus}"
HERDR_PLUS_ID="cloudmanic.herdr-plus"
HERDR_PLUS_GITHUB="cloudmanic/herdr-plus"
HERDR_PLUS_REF="${HERDR_PLUS_REF:-v0.1.16}"
# External role skills (wayfinder, to-tickets, to-spec, tdd, ...), pinned.
SKILLS_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/agentbus/skills/mattpocock"
SKILLS_REPO="${SKILLS_REPO:-https://github.com/mattpocock/skills.git}"
SKILLS_REF="${SKILLS_REF:-v1.3.1}"
DRY_RUN=0
FORCE=0
START_DOCKER=1
INSTALL_SKILLS=1
INSTALL_HERDR=0
ASSUME_YES=0
PURGE=0
WARNINGS=0
MISSING=0

SUBCOMMAND=install
case "${1:-}" in
  install|update|uninstall|status) SUBCOMMAND="$1"; shift ;;
esac
# Terminal styling (disabled when non-interactive or NO_COLOR is set)
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  BOLD="$(printf '\033[1m')"
  DIM="$(printf '\033[2m')"
  GREEN="$(printf '\033[32m')"
  YELLOW="$(printf '\033[33m')"
  RED="$(printf '\033[31m')"
  CYAN="$(printf '\033[36m')"
  RESET="$(printf '\033[0m')"
else
  BOLD=""
  DIM=""
  GREEN=""
  YELLOW=""
  RED=""
  CYAN=""
  RESET=""
fi

log_step()    { printf "\n%s==>%s %s%s%s\n" "$CYAN" "$RESET" "$BOLD" "$1" "$RESET"; }
log_info()    { printf "    %s•%s %s\n" "$DIM" "$RESET" "$1"; }
log_ok()      { printf "    %s✓%s %s\n" "$GREEN" "$RESET" "$1"; }
log_warn()    { WARNINGS=$((WARNINGS + 1)); printf "    %s!%s %s%s%s\n" "$YELLOW" "$RESET" "$YELLOW" "$1" "$RESET"; }
log_error()   { printf "    %s✗%s %s%s%s\n" "$RED" "$RESET" "$RED" "$1" "$RESET" >&2; }
log_fatal()   { printf "\n%serror:%s %s\n" "$RED" "$RESET" "$1" >&2; exit 1; }

# A required tool is missing: a dry run lists every gap, a real run stops after the preflight.
require_failed() { MISSING=$((MISSING + 1)); log_error "$1"; }

# Print the tail of a captured log, indented, after a failed step.
show_log() { tail -n 15 "$1" | sed 's/^/      /' >&2; }

usage() {
  cat <<USAGE
Agent Bus Monitor — setup

Usage:
  ./setup.sh install   [options]          (./install.sh is an alias)
  ./setup.sh update    [options]          git pull --ff-only, then install
  ./setup.sh uninstall [--yes] [--purge]  asks before every removal
  ./setup.sh status

Install / update options:
  --bin-dir <dir>   Install directory for binaries (default: \$HOME/.local/bin)
  --dry-run         Inspect the environment and show the actions, change nothing
  --install-herdr   Download herdr (herdr.dev installer) if missing, and install the herdr-plus
                    plugin through herdr (herdr plugin install $HERDR_PLUS_GITHUB --ref $HERDR_PLUS_REF)
  --force           Rebuild a linked herdr-plus checkout even if its binary is present
  --no-docker       Do not start the Redis broker container
  --no-skills       Do not fetch the external skills nor link role skills
  -h, --help        Show this help

Uninstall options:
  --yes             Accept the cautious default at every step (Redis is stopped, its data kept)
  --purge           Also remove the Redis container and its data without asking

agentbus and busmon are rebuilt on every install, into the repository (where the herdr tabs run
them) and into the bin dir, stamped with the release (agentbus version, busmon --version).

Environment variables:
  BIN_DIR           Binary installation directory
  HERDR_PLUS_PATH   A local herdr-plus checkout to link instead of installing it
  HERDR_PLUS_REF    herdr-plus tag installed by --install-herdr (default: $HERDR_PLUS_REF)
  SKILLS_REF        mattpocock/skills tag cloned into $SKILLS_HOME (default: $SKILLS_REF)
  NO_COLOR          Disables colorized terminal output
USAGE
  exit 0
}

# Parse command line flags
while [ $# -gt 0 ]; do
  case "$1" in
    --bin-dir)
      [ $# -gt 1 ] || log_fatal "--bin-dir requires a directory argument"
      BIN_DIR="$2"
      shift 2
      ;;
    --dry-run) DRY_RUN=1; shift ;;
    --install-herdr) INSTALL_HERDR=1; shift ;;
    --force) FORCE=1; shift ;;
    --no-docker) START_DOCKER=0; shift ;;
    --no-skills) INSTALL_SKILLS=0; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --purge) PURGE=1; shift ;;
    -h|--help) usage ;;
    *) log_fatal "Unknown flag: $1 (see --help)" ;;
  esac
done
[ "$SUBCOMMAND" = "uninstall" ] && [ "$DRY_RUN" = "1" ] &&
  log_fatal "uninstall has no --dry-run: it asks before every step (run it without --yes to choose)"

[ -f "$REPO_DIR/go.mod" ] && [ -d "$REPO_DIR/cmd/agentbus" ] ||
  log_fatal "setup.sh must run from a clone of agent-bus-monitor (no go.mod next to it in $REPO_DIR)"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

# version_ge A B: true when dotted version A >= B (non-numeric suffixes such as rc1 are ignored).
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

# -----------------------------------------------------------------------------
# Shared helpers for status / uninstall / update
# -----------------------------------------------------------------------------
HERDR_CONFIG="${HERDR_CONFIG_PATH:-${XDG_CONFIG_HOME:-$HOME/.config}/herdr/config.toml}"
KEYBIND_MARK="# herdr-plus Projects picker: prefix (ctrl+b) then Up"
KEYBIND_BLOCK="$(cat <<'EOF'

# herdr-plus Projects picker: prefix (ctrl+b) then Up
[[keys.command]]
key = "prefix+up"
type = "plugin_action"
command = "cloudmanic.herdr-plus.projects"
description = "herdr-plus: projects"
EOF
)"
# Only a clone setup made carries this marker; any other folder there is the user's.
SKILLS_MARK="$SKILLS_HOME/.git/agentbus-setup"
# The herdr-plus release setup installed through herdr, so a new pin reaches it.
HERDR_PLUS_STATE="${XDG_STATE_HOME:-$HOME/.local/state}/agentbus/herdr-plus.ref"
REDIS_CONTAINER="agent-bus-redis"

compose_cmd() {
  if docker compose version >/dev/null 2>&1; then echo "docker compose"
  elif command -v docker-compose >/dev/null 2>&1; then echo "docker-compose"
  fi
}

# ask QUESTION DEFAULT(y|n): yes/no on the terminal. --yes takes the default; no terminal and
# no --yes stops, so nothing is removed unattended by accident.
ask() {
  if [ "$ASSUME_YES" = "1" ]; then [ "$2" = "y" ]; return; fi
  ( : < /dev/tty ) 2>/dev/null || log_fatal "no terminal to ask on: re-run with --yes to accept the cautious defaults"
  hint="[y/N]"; [ "$2" = "y" ] && hint="[Y/n]"
  printf "    ? %s %s " "$1" "$hint" > /dev/tty
  read -r answer < /dev/tty || answer=""
  case "${answer:-$2}" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

# Ours: a binary that answers like agentbus/busmon.
is_our_binary() {
  case "$(basename "$1")" in
    agentbus) "$1" version 2>/dev/null | grep -q "agentbus protocol" ;;
    busmon)   "$1" --version 2>/dev/null | grep -q "busmon version" || "$1" --help 2>&1 | grep -q -- "-project" ;;
    *) return 1 ;;
  esac
}

# Role-skill symlinks install made: links pointing into this repository's skills or the
# external skills folder (or the historical ~/Tools checkout). Never a real directory.
our_skill_links() {
  { [ -n "${SKILLS_DEST:-}" ] && printf '%s\n' "$SKILLS_DEST"; printf '%s\n%s\n' "$HOME/.claude/skills" "$HOME/.agents/skills"; } |
  while IFS= read -r dir; do
    [ -d "$dir" ] || continue
    for link in "$dir"/*; do
      [ -L "$link" ] || continue
      target="$(readlink "$link")"
      case "$target" in
        "$REPO_DIR"/skills/*|"$SKILLS_HOME"/*|"$HOME"/Tools/herdr-plugins/skills/*) echo "$link" ;;
      esac
    done
  done
}

# The public launcher names are symlinks into this checkout. Only that exact
# target is ours: status and uninstall must preserve any user's replacement.
our_role_script_link() {
  [ -L "$1" ] && [ "$(readlink "$1")" = "$REPO_DIR/scripts/$(basename "$1")" ]
}

install_role_script_links() {
  for script in agent-launch agent-spawn; do
    target="$BIN_DIR/$script"
    source="$REPO_DIR/scripts/$script"
    if our_role_script_link "$target"; then
      log_ok "$script: linked to $source"
    elif [ -e "$target" ] || [ -L "$target" ]; then
      log_warn "$target exists and is not this repository's $script link; left as it is"
    elif [ "$DRY_RUN" = "1" ]; then
      log_info "[dry-run] would link $target -> $source"
    elif ln -s "$source" "$target"; then
      log_ok "$script: linked to $source"
    else
      log_warn "could not link $target -> $source"
    fi
  done
}

redis_state() { # running | stopped | absent
  command -v docker >/dev/null 2>&1 || { echo absent; return; }
  st="$(docker ps -a --filter "name=^${REDIS_CONTAINER}\$" --format '{{.State}}' 2>/dev/null | head -n 1)"
  case "$st" in running) echo running ;; "") echo absent ;; *) echo stopped ;; esac
}

# project_services: "<project> <kind> <state>" per agentbus user service (systemd --user /
# launchd), read through the agentbus that is installed; empty when there is none.
project_services() {
  for ab in "$BIN_DIR/agentbus" "$REPO_DIR/agentbus"; do
    if [ -x "$ab" ]; then
      "$ab" service list 2>/dev/null | grep -v '^no ' || true
      return
    fi
  done
}

herdr_plus_linked() { herdr plugin list 2>/dev/null | grep "$HERDR_PLUS_ID" | grep -q "local:"; }
# True only for a herdr-plus setup installed from GitHub at another ref; a linked checkout is the user's.
herdr_plus_pin_changed() { [ -f "$HERDR_PLUS_STATE" ] && [ "$(cat "$HERDR_PLUS_STATE")" != "$HERDR_PLUS_REF" ] && herdr_plus_registered && ! herdr_plus_linked; }
herdr_plus_registered() { command -v herdr >/dev/null 2>&1 && herdr plugin list 2>/dev/null | grep -q "$HERDR_PLUS_ID"; }
# The action list comes from the running herdr server, the plugin list from disk: outside a
# herdr session there may be no server to ask, which says nothing about the plugin.
herdr_plus_action_state() {
  local out
  command -v herdr >/dev/null 2>&1 || { echo missing; return; }
  out="$(herdr plugin action list 2>/dev/null | tr -d "\n")"
  if printf '%s' "$out" | grep -q "\"action_id\":\"projects\"[^}]*\"plugin_id\":\"$HERDR_PLUS_ID\""; then echo ok
  elif printf '%s' "$out" | grep -q '"server_not_running"'; then echo noserver
  else echo missing; fi
}

# -----------------------------------------------------------------------------
# setup.sh status
# -----------------------------------------------------------------------------
do_status() {
  log_step "Agent Bus Monitor — status ($REPO_DIR)"
  log_info "repository: $(git -C "$REPO_DIR" describe --tags --always --dirty 2>/dev/null || echo unknown) on $(git -C "$REPO_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo '?')"
  for bin_name in agentbus busmon; do
    for p in "$BIN_DIR/$bin_name" "$REPO_DIR/$bin_name"; do
      if [ -x "$p" ]; then
        case "$bin_name" in
          agentbus) v="$("$p" version 2>/dev/null | sed -n 's/^agentbus version //p')" ;;
          busmon)   v="$("$p" --version 2>/dev/null | sed -n 's/^busmon version //p')" ;;
        esac
        log_ok "$bin_name ${v:-?}: $p"
      else
        log_info "$bin_name: not installed at $p"
      fi
    done
    found="$(command -v "$bin_name" 2>/dev/null || true)"
    [ -n "$found" ] && [ "$found" != "$BIN_DIR/$bin_name" ] && log_warn "$bin_name on PATH is $found (shadows $BIN_DIR/$bin_name)"
  done
  for script in agent-launch agent-spawn; do
    p="$BIN_DIR/$script"
    if our_role_script_link "$p"; then
      log_ok "$script: $p -> $(readlink "$p")"
    elif [ -e "$p" ] || [ -L "$p" ]; then
      log_warn "$script: $p exists but is not this repository's link"
    else
      log_info "$script: not installed at $p"
    fi
    found="$(command -v "$script" 2>/dev/null || true)"
    [ -n "$found" ] && [ "$found" != "$p" ] && log_warn "$script on PATH is $found (shadows $p)"
  done
  if [ -d "$SKILLS_HOME/.git" ]; then
    log_ok "external skills: $(git -C "$SKILLS_HOME" describe --tags --always 2>/dev/null) at $SKILLS_HOME"
  else
    log_info "external skills: not installed ($SKILLS_HOME)"
  fi
  links="$(our_skill_links | wc -l | tr -d ' ')"
  log_info "role skill links: $links"
  if ! command -v herdr >/dev/null 2>&1; then
    log_info "herdr: not on PATH"
  elif ! herdr_plus_registered; then
    log_info "herdr-plus: not registered (./setup.sh install --install-herdr)"
  else
    case "$(herdr_plus_action_state)" in
      ok)       log_ok "herdr-plus: registered, Projects picker available (Ctrl+B then Up)" ;;
      noserver) log_info "herdr-plus: registered; Projects picker not checked (no herdr server running here)" ;;
      *)        log_warn "herdr-plus: registered but its projects action is missing (check: herdr plugin action list)" ;;
    esac
  fi
  if grep -qF "$KEYBIND_MARK" "$HERDR_CONFIG" 2>/dev/null; then log_ok "herdr key binding: prefix+up -> projects"; else log_info "herdr key binding: not added"; fi
  svcs="$(project_services)"
  if [ -n "$svcs" ]; then
    printf '%s\n' "$svcs" | while IFS= read -r line; do log_info "project service: $line"; done
  else
    log_info "project services: none"
  fi
  case "$(redis_state)" in
    running) log_ok "Redis broker: running ($REDIS_CONTAINER, 127.0.0.1:6380)" ;;
    stopped) log_info "Redis broker: stopped ($REDIS_CONTAINER)" ;;
    absent)  log_info "Redis broker: no container" ;;
  esac
  exit 0
}

# -----------------------------------------------------------------------------
# setup.sh uninstall — every step asks; --yes takes the cautious default
# -----------------------------------------------------------------------------
do_uninstall() {
  log_step "Agent Bus Monitor — uninstall"
  # The project services first, while an agentbus is still there to remove them.
  svcs="$(project_services)"
  if [ -n "$svcs" ]; then
    n="$(printf '%s\n' "$svcs" | wc -l | tr -d ' ')"
    if ask "stop and remove the $n project services (dispatch/missions units; their data and logs are kept)?" y; then
      for ab in "$BIN_DIR/agentbus" "$REPO_DIR/agentbus"; do [ -x "$ab" ] && break; done
      if "$ab" service remove --all >"$TMP_DIR/services.log" 2>&1; then log_ok "removed $n project services"
      else show_log "$TMP_DIR/services.log"; log_fatal "some project services could not be stopped or removed (agentbus service list); nothing else was removed"; fi
    fi
  fi
  for bin_name in agentbus busmon; do
    p="$BIN_DIR/$bin_name"
    if [ -x "$p" ] && is_our_binary "$p"; then
      if ask "remove $p?" y; then rm -f "$p"; log_ok "removed $p"; else log_info "kept $p"; fi
    fi
  done
  for script in agent-launch agent-spawn; do
    p="$BIN_DIR/$script"
    if our_role_script_link "$p"; then
      if ask "remove $p?" y; then rm -f "$p"; log_ok "removed $p"; else log_info "kept $p"; fi
    fi
  done
  links="$(our_skill_links)"
  if [ -n "$links" ]; then
    n="$(printf '%s\n' "$links" | wc -l | tr -d ' ')"
    if ask "remove the $n role-skill links (symlinks only)?" y; then
      printf '%s\n' "$links" | while IFS= read -r l; do rm -f "$l"; done
      log_ok "removed $n role-skill links"
    fi
  fi
  if grep -qF "$KEYBIND_MARK" "$HERDR_CONFIG" 2>/dev/null; then
    if ask "remove the herdr key binding (prefix+up -> herdr-plus projects)?" y; then
      backup="$HERDR_CONFIG.bak.$(date +%Y%m%d%H%M%S)"
      cp -p "$HERDR_CONFIG" "$backup"
      # Drop the block install wrote, line for line, and the blank line before it. A block the
      # user edited is left alone: guessing where it ends could take their next section with it.
      if printf '%s\n' "$KEYBIND_BLOCK" | awk '
          NR == FNR { if (FNR > 1) want[++w] = $0; next }
          { line[++n] = $0 }
          END {
            for (i = 1; i <= n; i++) {
              ok = 1
              for (k = 1; k <= w; k++) if (line[i + k - 1] != want[k]) { ok = 0; break }
              if (ok) { for (k = 0; k < w; k++) skip[i + k] = 1; if (i > 1 && line[i - 1] == "") skip[i - 1] = 1; found = 1; i += w - 1 }
            }
            for (i = 1; i <= n; i++) if (!skip[i]) print line[i]
            exit found ? 0 : 3
          }' - "$backup" > "$TMP_DIR/herdr-config.new"; then
        cat "$TMP_DIR/herdr-config.new" > "$HERDR_CONFIG"
        log_ok "removed the key binding (backup: $backup)"
        command -v herdr >/dev/null 2>&1 && herdr server reload-config >/dev/null 2>&1 || true
      else
        rm -f "$backup"
        log_warn "the key binding block in $HERDR_CONFIG was edited: left as it is (remove it by hand)"
      fi
    fi
  fi
  if herdr_plus_registered; then
    # herdr-plus is useful without agent-bus: kept unless asked.
    if ask "uninstall the herdr-plus plugin too (useful without agent-bus)?" n; then
      if herdr_plus_linked; then herdr plugin unlink "$HERDR_PLUS_ID" >/dev/null 2>&1 || true
      else herdr plugin uninstall "$HERDR_PLUS_ID" --yes >/dev/null 2>&1 || herdr plugin uninstall "$HERDR_PLUS_ID" >/dev/null 2>&1 || true; fi
      rm -f "$HERDR_PLUS_STATE"
      log_ok "herdr-plus: uninstalled"
    fi
  fi
  if [ -f "$SKILLS_MARK" ]; then
    if ! changes="$(git -C "$SKILLS_HOME" status --porcelain 2>/dev/null)" || [ -n "$changes" ]; then
      log_warn "$SKILLS_HOME has local changes, or git cannot read it: kept"
    elif ask "remove the external skills folder $SKILLS_HOME?" y; then rm -rf "$SKILLS_HOME"; log_ok "removed $SKILLS_HOME"; fi
  elif [ -e "$SKILLS_HOME" ]; then
    log_info "$SKILLS_HOME was not created by setup: kept"
  fi
  state="$(redis_state)"
  cc="$(compose_cmd)"
  if [ "$state" != "absent" ] && [ -n "$cc" ]; then
    if [ "$PURGE" = "1" ] || { [ "$ASSUME_YES" = "0" ] && ask "remove the Redis container AND its data (every project on the bus)?" n; }; then
      if ( cd "$REPO_DIR" && $cc down -v ) >/dev/null 2>&1; then log_ok "Redis: container and data removed"; else log_warn "Redis: $cc down -v failed"; fi
    elif [ "$state" = "running" ] && ask "stop the Redis broker (its data is kept)?" y; then
      if ( cd "$REPO_DIR" && $cc stop ) >/dev/null 2>&1; then log_ok "Redis: stopped (data kept; ./setup.sh install starts it again)"; else log_warn "Redis: $cc stop failed"; fi
    else
      log_info "Redis: left as it is ($state)"
    fi
  fi
  log_info "The repository and its built copies ($REPO_DIR/agentbus, busmon) are left in place."
  exit 0
}

# -----------------------------------------------------------------------------
# setup.sh update — fast-forward the repository, then install
# -----------------------------------------------------------------------------
do_update() {
  log_step "Agent Bus Monitor — update"
  git -C "$REPO_DIR" diff --quiet && git -C "$REPO_DIR" diff --cached --quiet ||
    log_fatal "the repository has uncommitted changes; commit or stash them first"
  from="$(git -C "$REPO_DIR" describe --tags --always 2>/dev/null)"
  from_rev="$(git -C "$REPO_DIR" rev-parse HEAD)"
  if [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would run git pull --ff-only in $REPO_DIR, then install"
    return 0
  fi
  git -C "$REPO_DIR" pull --ff-only --quiet >"$TMP_DIR/pull.log" 2>&1 || { show_log "$TMP_DIR/pull.log"; log_fatal "git pull --ff-only failed (a diverged branch is never merged by setup)"; }
  to="$(git -C "$REPO_DIR" describe --tags --always 2>/dev/null)"
  if [ "$(git -C "$REPO_DIR" rev-parse HEAD)" = "$from_rev" ]; then
    log_ok "already up to date ($to); reinstalling"
  else
    log_ok "updated $from -> $to (see CHANGELOG.md)"
    for tag in $(git -C "$REPO_DIR" tag --merged HEAD --no-merged "$from_rev" --sort=v:refname 2>/dev/null); do
      if [ -f "$REPO_DIR/docs/releases/$tag.md" ]; then log_info "$tag: docs/releases/$tag.md"; fi
    done
  fi
  # The pull may have changed this very script (pins, steps): install with the new one, not with
  # the copy already in memory.
  set -- install --bin-dir "$BIN_DIR"
  [ "$INSTALL_HERDR" = "1" ] && set -- "$@" --install-herdr
  [ "$FORCE" = "1" ] && set -- "$@" --force
  [ "$START_DOCKER" = "0" ] && set -- "$@" --no-docker
  [ "$INSTALL_SKILLS" = "0" ] && set -- "$@" --no-skills
  [ "$ASSUME_YES" = "1" ] && set -- "$@" --yes
  rm -rf "$TMP_DIR"
  exec "$REPO_DIR/setup.sh" "$@"
}

case "$SUBCOMMAND" in
  status) do_status ;;
  uninstall) do_uninstall ;;
  update) do_update ;;
esac

# -----------------------------------------------------------------------------
# 1. Platform & Toolchain Preflight
# -----------------------------------------------------------------------------
log_step "1/5 Checking system platform & toolchain"

# OS detection
RAW_OS="$(uname -s 2>/dev/null || echo unknown)"
case "$RAW_OS" in
  Linux*)   OS="linux" ;;
  Darwin*)  OS="darwin" ;;
  MSYS*|MINGW*|CYGWIN*) OS="windows" ;;
  *)        OS="$RAW_OS" ;;
esac

# Architecture detection
RAW_ARCH="$(uname -m 2>/dev/null || echo unknown)"
case "$RAW_ARCH" in
  x86_64|amd64)   ARCH="amd64" ;;
  aarch64|arm64)  ARCH="arm64" ;;
  armv7l)         ARCH="armv7" ;;
  *)              ARCH="$RAW_ARCH" ;;
esac

log_ok "Platform: $OS / $ARCH"

# Git check
if command -v git >/dev/null 2>&1; then
  log_ok "git: $(git --version 2>/dev/null || echo 'installed')"
else
  require_failed "git is required but not installed."
fi

# Curl is only needed to download herdr
if [ "$INSTALL_HERDR" = "1" ]; then
  if command -v curl >/dev/null 2>&1; then
    log_ok "curl: $(curl --version 2>/dev/null | head -n 1 || echo 'installed')"
  else
    require_failed "curl is required by --install-herdr but not installed."
  fi
fi

# Go compiler check, against the version go.mod requires
GO_WANT="$(sed -n 's/^go //p' "$REPO_DIR/go.mod" | head -n 1)"
if command -v go >/dev/null 2>&1; then
  # GOTOOLCHAIN=local: report the installed Go, never download a toolchain during preflight
  GO_HAVE="$(GOTOOLCHAIN=local go env GOVERSION 2>/dev/null | sed 's/^go//; s/ .*//')"
  if [ -n "$GO_HAVE" ] && version_ge "$GO_HAVE" "$GO_WANT"; then
    log_ok "go: $GO_HAVE (go.mod requires $GO_WANT)"
  elif [ "$(go env GOTOOLCHAIN 2>/dev/null)" = "local" ]; then
    require_failed "Go $GO_WANT or newer is required (found ${GO_HAVE:-unknown}, GOTOOLCHAIN=local): https://go.dev/dl/"
  else
    log_warn "go ${GO_HAVE:-unknown} is older than $GO_WANT; the build will download the go$GO_WANT toolchain."
  fi
else
  require_failed "Go $GO_WANT or newer is required to build busmon and agentbus: https://go.dev/dl/"
fi

# Docker & Docker Compose check
DOCKER_OK=0
DOCKER_COMPOSE_CMD=""
if command -v docker >/dev/null 2>&1; then
  if docker info >/dev/null 2>&1; then
    DOCKER_OK=1
    log_ok "docker: daemon is running"
  else
    log_warn "docker is installed but daemon is not running (needed for Redis broker)."
  fi

  if docker compose version >/dev/null 2>&1; then
    DOCKER_COMPOSE_CMD="docker compose"
    log_ok "docker compose: plugin available"
  elif command -v docker-compose >/dev/null 2>&1; then
    DOCKER_COMPOSE_CMD="docker-compose"
    log_ok "docker-compose: standalone command available"
  else
    log_warn "docker compose plugin is not available."
  fi
else
  log_warn "docker is not installed. Redis broker container requires Docker: https://docs.docker.com/engine/install/"
fi

if [ "$MISSING" -gt 0 ] && [ "$DRY_RUN" = "0" ]; then
  log_fatal "$MISSING required tool(s) missing; install them and re-run."
fi

# -----------------------------------------------------------------------------
# 2. herdr & herdr-plus Setup
# -----------------------------------------------------------------------------
log_step "2/5 Checking herdr workspace manager & herdr-plus plugin"

# herdr binary check & opt-in installation
if command -v herdr >/dev/null 2>&1; then
  log_ok "herdr: $(herdr --version 2>/dev/null || echo 'installed') ($(command -v herdr))"
elif [ "$INSTALL_HERDR" = "0" ]; then
  log_warn "herdr is not on PATH. Install it from https://herdr.dev, or re-run with --install-herdr."
elif [ "$DRY_RUN" = "1" ]; then
  log_info "[dry-run] would download and run https://herdr.dev/install.sh"
else
  log_info "Downloading the herdr installer (https://herdr.dev/install.sh)..."
  # Download first, run second: a truncated download is never executed half-way.
  if curl -fsSL https://herdr.dev/install.sh -o "$TMP_DIR/herdr-install.sh" && sh "$TMP_DIR/herdr-install.sh"; then
    export PATH="$HOME/.local/bin:$PATH"
    if command -v herdr >/dev/null 2>&1; then
      log_ok "herdr: installed successfully ($(command -v herdr))"
    else
      log_warn "herdr installed to ~/.local/bin; ensure it is added to your PATH."
    fi
  else
    log_warn "Automatic herdr installation failed. Install manually: https://herdr.dev"
  fi
fi

# herdr-plus: registered with herdr (the Projects picker is a herdr plugin action). A local
# checkout (HERDR_PLUS_PATH, or the historical ~/Tools path) is built and linked; otherwise
# --install-herdr installs the pinned release through herdr's own plugin store.
if ! command -v herdr >/dev/null 2>&1; then
  :
elif herdr_plus_pin_changed && [ "$DRY_RUN" = "1" ]; then
  log_info "[dry-run] would reinstall herdr-plus $(cat "$HERDR_PLUS_STATE") -> $HERDR_PLUS_REF"
elif herdr_plus_pin_changed; then
  old_ref="$(cat "$HERDR_PLUS_STATE")"
  herdr plugin uninstall "$HERDR_PLUS_ID" --yes >/dev/null 2>&1 || herdr plugin uninstall "$HERDR_PLUS_ID" >/dev/null 2>&1 || true
  if herdr plugin install "$HERDR_PLUS_GITHUB" --ref "$HERDR_PLUS_REF" --yes >"$TMP_DIR/herdr-install-plugin.log" 2>&1; then
    printf '%s\n' "$HERDR_PLUS_REF" > "$HERDR_PLUS_STATE"
    log_ok "herdr-plus: $old_ref -> $HERDR_PLUS_REF through herdr"
  else
    show_log "$TMP_DIR/herdr-install-plugin.log"
    if herdr plugin install "$HERDR_PLUS_GITHUB" --ref "$old_ref" --yes >/dev/null 2>&1 && herdr_plus_registered; then
      log_warn "herdr plugin install $HERDR_PLUS_GITHUB --ref $HERDR_PLUS_REF failed; $old_ref reinstalled."
    else
      rm -f "$HERDR_PLUS_STATE"
      log_warn "herdr-plus is NOT installed: $HERDR_PLUS_REF and the rollback to $old_ref both failed (re-run: herdr plugin install $HERDR_PLUS_GITHUB --ref $HERDR_PLUS_REF)."
    fi
  fi
elif herdr_plus_registered && [ "$FORCE" = "0" ]; then
  log_ok "herdr-plus: registered with herdr"
elif [ -d "$HERDR_PLUS_DIR" ]; then
  if [ -x "$HERDR_PLUS_DIR/bin/herdr-plus" ] && [ "$FORCE" = "0" ]; then
    log_ok "herdr-plus checkout: built ($HERDR_PLUS_DIR)"
  elif [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would build herdr-plus in $HERDR_PLUS_DIR"
  else
    log_info "Building herdr-plus in $HERDR_PLUS_DIR..."
    if [ -f "$HERDR_PLUS_DIR/scripts/build.sh" ]; then build="sh scripts/build.sh"; else build="make build"; fi
    if ( cd "$HERDR_PLUS_DIR" && $build ) >"$TMP_DIR/herdr-plus-build.log" 2>&1 && [ -x "$HERDR_PLUS_DIR/bin/herdr-plus" ]; then
      log_ok "herdr-plus: built"
    else
      show_log "$TMP_DIR/herdr-plus-build.log"
      log_warn "herdr-plus build ($build) failed or produced no bin/herdr-plus."
    fi
  fi
  if herdr_plus_registered; then
    :
  elif [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would run: herdr plugin link $HERDR_PLUS_DIR"
  elif herdr plugin link "$HERDR_PLUS_DIR" >"$TMP_DIR/herdr-link.log" 2>&1; then
    log_ok "herdr-plus: linked into herdr ($HERDR_PLUS_DIR)"
  else
    show_log "$TMP_DIR/herdr-link.log"
    log_warn "herdr plugin link $HERDR_PLUS_DIR failed."
  fi
elif [ "$INSTALL_HERDR" = "0" ]; then
  log_warn "herdr-plus is not installed: re-run with --install-herdr (or: herdr plugin install $HERDR_PLUS_GITHUB --ref $HERDR_PLUS_REF)."
elif [ "$DRY_RUN" = "1" ]; then
  log_info "[dry-run] would run: herdr plugin install $HERDR_PLUS_GITHUB --ref $HERDR_PLUS_REF --yes"
elif herdr plugin install "$HERDR_PLUS_GITHUB" --ref "$HERDR_PLUS_REF" --yes >"$TMP_DIR/herdr-install-plugin.log" 2>&1; then
  mkdir -p "$(dirname "$HERDR_PLUS_STATE")" && printf '%s\n' "$HERDR_PLUS_REF" > "$HERDR_PLUS_STATE"
  log_ok "herdr-plus: installed $HERDR_PLUS_REF through herdr"
else
  show_log "$TMP_DIR/herdr-install-plugin.log"
  log_warn "herdr plugin install $HERDR_PLUS_GITHUB --ref $HERDR_PLUS_REF failed."
fi
# What Ctrl+B Up actually needs: the plugin's projects action, as herdr sees it.
if command -v herdr >/dev/null 2>&1 && [ "$DRY_RUN" = "0" ] && herdr_plus_registered; then
  case "$(herdr_plus_action_state)" in
    ok)       log_ok "herdr-plus: Projects picker action available" ;;
    noserver) log_info "herdr-plus: registered; Projects picker not checked (no herdr server running here, run ./setup.sh status inside herdr)" ;;
    *)        log_warn "herdr-plus is registered but herdr lists no $HERDR_PLUS_ID.projects action (check: herdr plugin action list)." ;;
  esac
fi

# Configure herdr-plus keybinding in ~/.config/herdr/config.toml idempotently
HERDR_CONFIG="${HERDR_CONFIG_PATH:-${XDG_CONFIG_HOME:-$HOME/.config}/herdr/config.toml}"
# herdr runs with a partial config.toml, so a machine where it never ran gets one holding the binding.
if [ ! -f "$HERDR_CONFIG" ] && [ "$DRY_RUN" = "1" ]; then
  log_info "[dry-run] would create $HERDR_CONFIG with the herdr-plus keybinding (prefix+up)"
elif [ ! -f "$HERDR_CONFIG" ]; then
  mkdir -p "$(dirname "$HERDR_CONFIG")"
  printf '%s\n' "$KEYBIND_BLOCK" > "$HERDR_CONFIG"
  log_ok "herdr config: created $HERDR_CONFIG with the Projects picker shortcut (prefix+up)"
  KEYBIND_WRITTEN=1
elif grep -q "cloudmanic.herdr-plus.projects" "$HERDR_CONFIG" 2>/dev/null; then
  log_ok "herdr config: Projects picker keybinding already configured"
elif grep -qF '"prefix+up"' "$HERDR_CONFIG" 2>/dev/null; then
  log_warn "herdr config: prefix+up is already bound in $HERDR_CONFIG; bind cloudmanic.herdr-plus.projects to another key by hand."
elif [ "$DRY_RUN" = "1" ]; then
  log_info "[dry-run] would add herdr-plus keybinding (prefix+up) to $HERDR_CONFIG"
else
  backup="$HERDR_CONFIG.bak.$(date +%Y%m%d%H%M%S)"
  cp -p "$HERDR_CONFIG" "$backup"
  printf '%s\n' "$KEYBIND_BLOCK" >> "$HERDR_CONFIG"
  log_ok "herdr config: added Projects picker shortcut (prefix+up) to $HERDR_CONFIG (backup: $backup)"
  KEYBIND_WRITTEN=1
fi
# A herdr that is already running reads a new binding only when told to.
if [ "${KEYBIND_WRITTEN:-0}" = "1" ] && command -v herdr >/dev/null 2>&1; then
  if herdr server reload-config >/dev/null 2>&1; then
    log_ok "herdr: running server reloaded its config (Ctrl+B then Up works now)"
  else
    log_info "herdr: the binding applies when herdr next starts (or run: herdr server reload-config)"
  fi
fi

# -----------------------------------------------------------------------------
# 3. Build & Install Agent Bus Binaries
# -----------------------------------------------------------------------------
log_step "3/5 Building and installing agentbus & busmon"

REPO_REV="$(git -C "$REPO_DIR" describe --tags --always --dirty 2>/dev/null || echo 'unknown revision')"
# The release stamped into both binaries (agentbus version, busmon --version, the update notice).
case "$REPO_REV" in v[0-9]*) BUILD_VERSION="$REPO_REV" ;; *) BUILD_VERSION="dev" ;; esac
LDFLAGS="-X github.com/netbja/agent-bus-monitor/buildinfo.Version=$BUILD_VERSION -X 'github.com/netbja/agent-bus-monitor/buildinfo.Repo=$REPO_DIR'"

# install_file SRC DEST: copy next to DEST, then rename over it, so a running binary is replaced
# atomically instead of being rewritten in place.
install_file() {
  cp "$1" "$2.tmp.$$"
  chmod 755 "$2.tmp.$$"
  mv -f "$2.tmp.$$" "$2"
}

build_and_install_binary() {
  bin_name="$1"
  pkg_path="$2"
  target="$BIN_DIR/$bin_name"

  if [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would compile $pkg_path -> $target (and $REPO_DIR/$bin_name)"
    return 0
  fi

  log_info "Compiling $bin_name ($REPO_REV)..."
  if ( cd "$REPO_DIR" && go build -ldflags "$LDFLAGS" -o "$TMP_DIR/$bin_name" "$pkg_path" ) >"$TMP_DIR/$bin_name-build.log" 2>&1; then
    # bootstrap's herdr tabs run the copies in the repository; the shell runs the one in BIN_DIR
    install_file "$TMP_DIR/$bin_name" "$REPO_DIR/$bin_name"
    if [ "$(CDPATH= cd -- "$BIN_DIR" && pwd)" != "$REPO_DIR" ]; then
      install_file "$TMP_DIR/$bin_name" "$target"
    fi
    log_ok "$bin_name: installed to $target and $REPO_DIR/$bin_name"
  else
    show_log "$TMP_DIR/$bin_name-build.log"
    log_fatal "Failed to build $bin_name from $pkg_path"
  fi
}

[ "$DRY_RUN" = "1" ] || mkdir -p "$BIN_DIR"
build_and_install_binary "agentbus" "./cmd/agentbus"
build_and_install_binary "busmon"   "./cmd/busmon"
install_role_script_links

# A running project service keeps the binary it started with: say which, and how to move it on.
if [ "$DRY_RUN" = "0" ]; then
  svcs="$(project_services)"
  if [ -n "$svcs" ]; then
    log_info "project services keep running the previous agentbus until restarted:"
    printf '%s\n' "$svcs" | while read -r sp sk srest; do
      log_info "  $sp $sk ($srest): agentbus --project $sp service restart $sk"
    done
  fi
fi

# Verify PATH: BIN_DIR must be on it, and nothing earlier on it may shadow the new binaries
case ":$PATH:" in
  *":$BIN_DIR:"*)
    log_ok "PATH: $BIN_DIR is in your active PATH"
    if [ "$DRY_RUN" = "0" ]; then
      for bin_name in agentbus busmon; do
        found="$(command -v "$bin_name" 2>/dev/null || true)"
        if [ -n "$found" ] && [ "$found" != "$BIN_DIR/$bin_name" ]; then
          log_warn "$bin_name on PATH is $found, which shadows $BIN_DIR/$bin_name; remove it or put $BIN_DIR first."
        fi
      done
    fi
    ;;
  *)
    log_warn "$BIN_DIR is NOT in your current PATH!"
    log_info "Add it to your shell configuration (e.g. ~/.bashrc or ~/.zshrc):"
    log_info "  export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac

# External role skills (mattpocock/skills, pinned): the default roles use wayfinder, to-tickets,
# to-spec, tdd, implement, ... A missing one only warns at launch, but the team is weaker for it.
if [ "$INSTALL_SKILLS" = "1" ]; then
  if [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would fetch $SKILLS_REPO $SKILLS_REF into $SKILLS_HOME"
  elif [ -e "$SKILLS_HOME" ] && [ ! -f "$SKILLS_MARK" ]; then
    log_warn "$SKILLS_HOME was not created by setup: left as it is (agents use the skills it holds)"
  elif [ -d "$SKILLS_HOME/.git" ]; then
    if git -C "$SKILLS_HOME" fetch --quiet --depth 1 origin "refs/tags/$SKILLS_REF:refs/tags/$SKILLS_REF" >"$TMP_DIR/skills-fetch.log" 2>&1 ||
       git -C "$SKILLS_HOME" fetch --quiet --depth 1 origin "$SKILLS_REF" >>"$TMP_DIR/skills-fetch.log" 2>&1; then
      git -C "$SKILLS_HOME" checkout --quiet "$SKILLS_REF" 2>/dev/null || git -C "$SKILLS_HOME" checkout --quiet FETCH_HEAD
      log_ok "external skills: $SKILLS_REF ($SKILLS_HOME)"
    else
      show_log "$TMP_DIR/skills-fetch.log"
      log_warn "could not update the external skills in $SKILLS_HOME (kept as they are)."
    fi
  else
    mkdir -p "$(dirname "$SKILLS_HOME")"
    if git clone --quiet --depth 1 --branch "$SKILLS_REF" "$SKILLS_REPO" "$SKILLS_HOME" >"$TMP_DIR/skills-clone.log" 2>&1; then
      : > "$SKILLS_MARK"
      log_ok "external skills: cloned $SKILLS_REF into $SKILLS_HOME"
    else
      show_log "$TMP_DIR/skills-clone.log"
      log_warn "could not clone $SKILLS_REPO $SKILLS_REF: agents start without wayfinder, to-tickets, to-spec, ..."
    fi
  fi
fi

# Link role skills
if [ "$INSTALL_SKILLS" = "1" ]; then
  if [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would run scripts/link-role-skills.sh"
  elif [ -f "$REPO_DIR/scripts/link-role-skills.sh" ]; then
    log_info "Linking role skills for AI clients..."
    if "$REPO_DIR/scripts/link-role-skills.sh" >"$TMP_DIR/skills.log" 2>&1; then
      log_ok "Role skills: linked successfully"
    else
      show_log "$TMP_DIR/skills.log"
      log_warn "Role skills linking failed; re-run scripts/link-role-skills.sh to see the full output."
    fi
  fi
fi

# -----------------------------------------------------------------------------
# 4. Redis Broker Setup (:6380)
# -----------------------------------------------------------------------------
log_step "4/5 Checking Redis broker service"

is_port_open() {
  if command -v nc >/dev/null 2>&1; then
    nc -z "$1" "$2" 2>/dev/null
    return $?
  fi
  # /dev/tcp is a bash feature; /bin/sh may be dash
  if command -v bash >/dev/null 2>&1; then
    bash -c 'exec 3<>"/dev/tcp/$1/$2"' _ "$1" "$2" 2>/dev/null
    return $?
  fi
  return 1
}

if is_port_open "127.0.0.1" 6380; then
  log_ok "Redis broker: active and listening on 127.0.0.1:6380"
elif [ "$START_DOCKER" = "0" ]; then
  log_info "Redis broker not reachable on 127.0.0.1:6380 (--no-docker: not started)."
elif [ "$DOCKER_OK" = "1" ] && [ -n "$DOCKER_COMPOSE_CMD" ]; then
  if [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would start Redis container via $DOCKER_COMPOSE_CMD up -d"
  else
    log_info "Redis not detected on :6380. Starting broker container..."
    if ( cd "$REPO_DIR" && $DOCKER_COMPOSE_CMD up -d ) >"$TMP_DIR/compose.log" 2>&1; then
      # Wait up to 10 seconds for Redis port to become ready
      tries=0
      redis_ready=0
      while [ $tries -lt 10 ]; do
        if is_port_open "127.0.0.1" 6380; then
          redis_ready=1
          break
        fi
        sleep 1
        tries=$((tries + 1))
      done

      if [ "$redis_ready" = "1" ]; then
        log_ok "Redis broker: started successfully on 127.0.0.1:6380"
      else
        log_warn "Redis container started but port 6380 is not yet responsive."
      fi
    else
      show_log "$TMP_DIR/compose.log"
      log_warn "Failed to launch Redis container via $DOCKER_COMPOSE_CMD up -d"
    fi
  fi
else
  log_warn "Redis broker not reachable on 127.0.0.1:6380 (run 'docker compose up -d' manually in $REPO_DIR)."
fi

# -----------------------------------------------------------------------------
# 5. Installed AI Clients Inventory
# -----------------------------------------------------------------------------
log_step "5/5 Scanning installed AI coding agents"

found_client=0
for client in claude codex kimi agy; do
  if command -v "$client" >/dev/null 2>&1; then
    log_ok "$client: available ($(command -v "$client"))"
    found_client=1
  else
    log_info "$client: not installed (optional)"
  fi
done

if [ "$found_client" = "0" ]; then
  log_warn "No recognized agent CLI found on PATH (claude, codex, kimi, agy). Install at least one agent client."
fi

# The model table (v0.15.2): each client's own free catalogue, never a model call.
# AGENTBUS_SETUP_NO_MODELS=1 skips it (the installer tests do).
if [ "$DRY_RUN" = "1" ]; then
  log_info "[dry-run] would refresh the model table (agentbus models --refresh)"
elif [ "${AGENTBUS_SETUP_NO_MODELS:-0}" != "1" ] && [ -x "$BIN_DIR/agentbus" ]; then
  if "$BIN_DIR/agentbus" models --refresh >"$TMP_DIR/models.log" 2>&1; then
    log_ok "model table refreshed (agentbus models)"
  else
    log_warn "model table not refreshed: $(tail -1 "$TMP_DIR/models.log")"
  fi
fi

# -----------------------------------------------------------------------------
# Summary & Next Steps
# -----------------------------------------------------------------------------
if [ "$DRY_RUN" = "1" ]; then
  COLOR="$GREEN"
  [ "$MISSING" -gt 0 ] && COLOR="$RED"
  TITLE="Dry run complete — nothing was changed ($MISSING missing, $WARNINGS warning(s))"
elif [ "$WARNINGS" -gt 0 ]; then
  COLOR="$YELLOW"
  TITLE="Installation complete with $WARNINGS warning(s) — see above"
else
  COLOR="$GREEN"
  TITLE="Installation complete ($REPO_REV)"
fi
printf "\n%s=================================================================%s\n" "$COLOR" "$RESET"
printf "%s%s %s %s\n" "$BOLD" "$COLOR" "$TITLE" "$RESET"
printf "%s=================================================================%s\n\n" "$COLOR" "$RESET"

[ "$MISSING" -eq 0 ] || exit 1

echo "Next steps:"
echo "  1. Start herdr (a named session per project is handy):"
echo "     ${CYAN}herdr --session myproject${RESET}"
echo ""
echo "  2. Inside it, provision and open the Agent Bus team in one command:"
echo "     ${CYAN}agentbus up myproject /path/to/your/codebase --as <your-name>${RESET}"
echo "     (busmon opens first; later, reopen any project with ${BOLD}Ctrl+B${RESET} then ${BOLD}Up${RESET})"
echo ""
echo "  Keep it current: ${CYAN}./setup.sh update${RESET} · check it: ${CYAN}./setup.sh status${RESET} · remove it: ${CYAN}./setup.sh uninstall${RESET}"
echo ""
