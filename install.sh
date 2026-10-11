#!/bin/sh
#
# Agent Bus Monitor — Automated Idempotent Installer
#
# Installs and validates the Agent Bus environment from a clone of this repository:
# - System preflight: OS/Arch, git, Python (>= 3.11), Go (the version go.mod requires), Docker
# - herdr terminal manager & herdr-plus workspace plugin (downloaded only with --install-herdr)
# - Agent Bus binaries: agentbus & busmon, rebuilt on every run
# - Role skills for supported AI clients
# - Redis broker container (127.0.0.1:6380)
#
# Usage (from the repository root; piping this script into sh is not supported):
#   ./install.sh [options]
#
set -eu

# Script directory and repository root
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_DIR="$SCRIPT_DIR"

# Defaults
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
HERDR_PLUS_DIR="${HERDR_PLUS_PATH:-$HOME/Tools/herdr-plugins/herdr-plus}"
HERDR_PLUS_REPO="https://github.com/cloudmanic/herdr-plus.git"
HERDR_PLUS_REF="${HERDR_PLUS_REF:-v0.1.16}"
DRY_RUN=0
FORCE=0
START_DOCKER=1
INSTALL_SKILLS=1
INSTALL_HERDR=0
WARNINGS=0
MISSING=0

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
  cat <<EOF
Agent Bus Monitor — Automated Idempotent Installer

Usage:
  ./install.sh [options]

Options:
  --bin-dir <dir>   Install directory for binaries (default: \$HOME/.local/bin)
  --dry-run         Inspect environment and display actions without changing anything
  --install-herdr   Download herdr (herdr.dev installer) and clone herdr-plus ($HERDR_PLUS_REF) if missing
  --force           Rebuild herdr-plus even if its binary is present
  --no-docker       Skip starting the Redis broker container
  --no-skills       Skip linking role skills
  -h, --help        Show this help message

agentbus and busmon are rebuilt on every run, so a run after 'git pull' always installs the
current code: into the repository (where bootstrap's tabs run them) and into the bin dir.

Environment variables:
  BIN_DIR           Overrides binary installation directory
  HERDR_PLUS_PATH   Overrides herdr-plus plugin repository path
  HERDR_PLUS_REF    herdr-plus tag or branch cloned by --install-herdr (default: $HERDR_PLUS_REF)
  NO_COLOR          Disables colorized terminal output
EOF
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
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --install-herdr)
      INSTALL_HERDR=1
      shift
      ;;
    --force)
      FORCE=1
      shift
      ;;
    --no-docker)
      START_DOCKER=0
      shift
      ;;
    --no-skills)
      INSTALL_SKILLS=0
      shift
      ;;
    -h|--help)
      usage
      ;;
    *)
      log_fatal "Unknown flag: $1 (see --help)"
      ;;
  esac
done

[ -f "$REPO_DIR/go.mod" ] && [ -d "$REPO_DIR/cmd/agentbus" ] ||
  log_fatal "install.sh must run from a clone of agent-bus-monitor (no go.mod next to it in $REPO_DIR)"

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

# Python 3.11+ check (role skills and the bootstrap template generator use tomllib)
PYTHON_BIN=""
for py in python3 python; do
  if command -v "$py" >/dev/null 2>&1; then
    if "$py" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 11) else 1)' >/dev/null 2>&1; then
      PYTHON_BIN="$py"
      break
    fi
  fi
done

if [ -n "$PYTHON_BIN" ]; then
  PY_VER="$("$PYTHON_BIN" -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}")')"
  log_ok "python3 (>= 3.11): $PY_VER ($PYTHON_BIN)"
else
  require_failed "Python 3.11 or newer is required (role skills and bootstrap use tomllib)."
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

# herdr-plus checkout (pinned to HERDR_PLUS_REF, opt-in)
if [ -d "$HERDR_PLUS_DIR" ]; then
  log_ok "herdr-plus repository: present ($HERDR_PLUS_DIR)"
elif [ "$INSTALL_HERDR" = "0" ]; then
  log_warn "herdr-plus not found at $HERDR_PLUS_DIR (set HERDR_PLUS_PATH, or re-run with --install-herdr to clone $HERDR_PLUS_REF)."
elif [ "$DRY_RUN" = "1" ]; then
  log_info "[dry-run] would clone herdr-plus $HERDR_PLUS_REF to $HERDR_PLUS_DIR"
else
  log_info "Cloning herdr-plus $HERDR_PLUS_REF into $HERDR_PLUS_DIR..."
  mkdir -p "$(dirname "$HERDR_PLUS_DIR")"
  if git clone --quiet --depth 1 --branch "$HERDR_PLUS_REF" "$HERDR_PLUS_REPO" "$HERDR_PLUS_DIR" >"$TMP_DIR/clone.log" 2>&1; then
    log_ok "herdr-plus: cloned $HERDR_PLUS_REF ($(git -C "$HERDR_PLUS_DIR" rev-parse --short HEAD))"
  else
    show_log "$TMP_DIR/clone.log"
    log_fatal "Failed to clone herdr-plus $HERDR_PLUS_REF from $HERDR_PLUS_REPO."
  fi
fi

# Build herdr-plus binary if missing (the plugin manifest's build script, else its Makefile)
if [ ! -d "$HERDR_PLUS_DIR" ]; then
  :
elif [ -x "$HERDR_PLUS_DIR/bin/herdr-plus" ] && [ "$FORCE" = "0" ]; then
  log_ok "herdr-plus binary: compiled ($HERDR_PLUS_DIR/bin/herdr-plus)"
elif [ "$DRY_RUN" = "1" ]; then
  log_info "[dry-run] would build herdr-plus in $HERDR_PLUS_DIR"
else
  log_info "Building herdr-plus binary..."
  if [ -f "$HERDR_PLUS_DIR/scripts/build.sh" ]; then build="sh scripts/build.sh"; else build="make build"; fi
  if ( cd "$HERDR_PLUS_DIR" && $build ) >"$TMP_DIR/herdr-plus-build.log" 2>&1 && [ -x "$HERDR_PLUS_DIR/bin/herdr-plus" ]; then
    log_ok "herdr-plus binary: built successfully"
  else
    show_log "$TMP_DIR/herdr-plus-build.log"
    log_warn "herdr-plus build ($build) failed or produced no bin/herdr-plus."
  fi
fi

# Configure herdr-plus keybinding in ~/.config/herdr/config.toml idempotently
HERDR_CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/herdr/config.toml"
if [ ! -f "$HERDR_CONFIG" ]; then
  log_info "herdr config file not yet created at $HERDR_CONFIG (will be created on first herdr launch)."
elif grep -q "cloudmanic.herdr-plus.projects" "$HERDR_CONFIG" 2>/dev/null; then
  log_ok "herdr config: Projects picker keybinding already configured"
elif grep -qF '"prefix+up"' "$HERDR_CONFIG" 2>/dev/null; then
  log_warn "herdr config: prefix+up is already bound in $HERDR_CONFIG; bind cloudmanic.herdr-plus.projects to another key by hand."
elif [ "$DRY_RUN" = "1" ]; then
  log_info "[dry-run] would add herdr-plus keybinding (prefix+up) to $HERDR_CONFIG"
else
  backup="$HERDR_CONFIG.bak.$(date +%Y%m%d%H%M%S)"
  cp -p "$HERDR_CONFIG" "$backup"
  cat <<'EOF' >> "$HERDR_CONFIG"

# herdr-plus Projects picker: prefix (ctrl+b) then Up
[[keys.command]]
key = "prefix+up"
type = "plugin_action"
command = "cloudmanic.herdr-plus.projects"
description = "herdr-plus: projects"
EOF
  log_ok "herdr config: added Projects picker shortcut (prefix+up) to $HERDR_CONFIG (backup: $backup)"
fi

# -----------------------------------------------------------------------------
# 3. Build & Install Agent Bus Binaries
# -----------------------------------------------------------------------------
log_step "3/5 Building and installing agentbus & busmon"

REPO_REV="$(git -C "$REPO_DIR" describe --tags --always --dirty 2>/dev/null || echo 'unknown revision')"

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
  if ( cd "$REPO_DIR" && go build -o "$TMP_DIR/$bin_name" "$pkg_path" ) >"$TMP_DIR/$bin_name-build.log" 2>&1; then
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

echo "Quick Start Workflow:"
echo "  1. Start a named herdr session for your project:"
echo "     ${CYAN}herdr --session myproject${RESET}"
echo ""
echo "  2. From inside that session, provision the Agent Bus team:"
echo "     ${CYAN}$REPO_DIR/scripts/bootstrap new myproject /path/to/your/codebase${RESET}"
echo ""
echo "  3. Open the workspace via herdr-plus:"
echo "     Press ${BOLD}Ctrl+B${RESET} then ${BOLD}Up Arrow${RESET} and pick ${CYAN}myproject${RESET}"
echo ""
echo "  4. Monitor live activity:"
echo "     ${CYAN}$BIN_DIR/busmon --project myproject${RESET}"
echo ""
