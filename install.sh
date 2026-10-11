#!/bin/sh
#
# Agent Bus Monitor — Automated Idempotent Installer
#
# Installs and validates the Agent Bus environment:
# - System preflight: OS/Arch, Python (>= 3.11), Docker & Docker Compose, Go (>= 1.22)
# - herdr terminal manager & herdr-plus workspace plugin
# - Agent Bus binaries: agentbus & busmon
# - Role skills for supported AI clients
# - Redis broker container (:6380)
#
# Usage:
#   ./install.sh [options]
#   curl -fsSL https://raw.githubusercontent.com/netbja/agent-bus-monitor/main/install.sh | sh
#
# Options:
#   --bin-dir <dir>   Installation directory for binaries (default: $HOME/.local/bin)
#   --dry-run         Verify environment and show planned actions without modifying system
#   --force           Rebuild and re-link even if current binaries/skills are up to date
#   --no-docker       Skip starting the Redis Docker broker
#   --no-skills       Skip installing role skills
#   -h, --help        Show this help message
#
set -eu

# Script directory and repository root
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_DIR="$SCRIPT_DIR"

# Defaults
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
HERDR_PLUS_DIR="${HERDR_PLUS_PATH:-$HOME/Tools/herdr-plugins/herdr-plus}"
DRY_RUN=0
FORCE=0
START_DOCKER=1
INSTALL_SKILLS=1

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
log_warn()    { printf "    %s!%s %s%s%s\n" "$YELLOW" "$RESET" "$YELLOW" "$1" "$RESET"; }
log_error()   { printf "    %s✗%s %s%s%s\n" "$RED" "$RESET" "$RED" "$1" "$RESET" >&2; }
log_fatal()   { printf "\n%serror:%s %s\n" "$RED" "$RESET" "$1" >&2; exit 1; }

usage() {
  cat <<EOF
Agent Bus Monitor — Automated Idempotent Installer

Usage:
  ./install.sh [options]

Options:
  --bin-dir <dir>   Install directory for binaries (default: \$HOME/.local/bin)
  --dry-run         Inspect environment and display actions without mutating state
  --force           Rebuild and reinstall tools even if already present
  --no-docker       Skip starting the Redis broker container
  --no-skills       Skip linking role skills
  -h, --help        Show this help message

Environment variables:
  BIN_DIR           Overrides binary installation directory
  HERDR_PLUS_PATH   Overrides herdr-plus plugin repository path
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
  log_fatal "git is required but not installed."
fi

# Curl check
if command -v curl >/dev/null 2>&1; then
  log_ok "curl: $(curl --version 2>/dev/null | head -n 1 || echo 'installed')"
else
  log_fatal "curl is required but not installed."
fi

# Python 3.11+ check (required by roles manifest parser tomllib)
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
  log_fatal "Python 3.11 or newer is required (for roles & tomllib). Please install python3 >= 3.11."
fi

# Go compiler check
if command -v go >/dev/null 2>&1; then
  GO_VER="$(go version 2>/dev/null || echo 'installed')"
  log_ok "go: $GO_VER"
else
  log_fatal "Go compiler is required to build busmon and agentbus. Install Go (>= 1.22): https://go.dev/dl/"
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

# -----------------------------------------------------------------------------
# 2. herdr & herdr-plus Setup
# -----------------------------------------------------------------------------
log_step "2/5 Checking herdr workspace manager & herdr-plus plugin"

# herdr binary check & optional installation
if command -v herdr >/dev/null 2>&1; then
  log_ok "herdr: $(herdr --version 2>/dev/null || echo 'installed') ($(command -v herdr))"
else
  if [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would install herdr via https://herdr.dev/install.sh"
  else
    log_warn "herdr is not on PATH. Installing herdr..."
    if curl -fsSL https://herdr.dev/install.sh | sh; then
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
fi

# herdr-plus checkout & compilation
if [ -d "$HERDR_PLUS_DIR" ]; then
  log_ok "herdr-plus repository: present ($HERDR_PLUS_DIR)"
else
  if [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would clone herdr-plus to $HERDR_PLUS_DIR"
  else
    log_info "Cloning herdr-plus into $HERDR_PLUS_DIR..."
    mkdir -p "$(dirname "$HERDR_PLUS_DIR")"
    if git clone https://github.com/cloudmanic/herdr-plus.git "$HERDR_PLUS_DIR" >/dev/null 2>&1; then
      log_ok "herdr-plus: cloned successfully"
    else
      log_fatal "Failed to clone herdr-plus repository."
    fi
  fi
fi

# Build herdr-plus binary if missing
if [ -x "$HERDR_PLUS_DIR/bin/herdr-plus" ] && [ "$FORCE" = "0" ]; then
  log_ok "herdr-plus binary: compiled ($HERDR_PLUS_DIR/bin/herdr-plus)"
else
  if [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would build herdr-plus binary via make build"
  elif [ -d "$HERDR_PLUS_DIR" ]; then
    log_info "Building herdr-plus binary..."
    if ( cd "$HERDR_PLUS_DIR" && (make build >/dev/null 2>&1 || sh scripts/build.sh >/dev/null 2>&1) ); then
      if [ -x "$HERDR_PLUS_DIR/bin/herdr-plus" ]; then
        log_ok "herdr-plus binary: built successfully"
      else
        log_warn "herdr-plus build finished without executable in bin/herdr-plus"
      fi
    else
      log_warn "herdr-plus compilation failed. Run 'make -C $HERDR_PLUS_DIR build' manually."
    fi
  fi
fi

# Configure herdr-plus keybinding in ~/.config/herdr/config.toml idempotently
HERDR_CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/herdr/config.toml"
if [ -f "$HERDR_CONFIG" ]; then
  if grep -q "cloudmanic.herdr-plus.projects" "$HERDR_CONFIG" 2>/dev/null; then
    log_ok "herdr config: Projects picker keybinding already configured"
  else
    if [ "$DRY_RUN" = "1" ]; then
      log_info "[dry-run] would add herdr-plus keybinding (prefix+up) to $HERDR_CONFIG"
    else
      cat <<'EOF' >> "$HERDR_CONFIG"

# herdr-plus Projects picker: prefix (ctrl+b) then Up
[[keys.command]]
key = "prefix+up"
type = "plugin_action"
command = "cloudmanic.herdr-plus.projects"
description = "herdr-plus: projects"
EOF
      log_ok "herdr config: Added Projects picker shortcut (prefix+up) to $HERDR_CONFIG"
    fi
  fi
else
  log_info "herdr config file not yet created at $HERDR_CONFIG (will be created on first herdr launch)."
fi

# -----------------------------------------------------------------------------
# 3. Build & Install Agent Bus Binaries
# -----------------------------------------------------------------------------
log_step "3/5 Building and installing agentbus & busmon"

mkdir -p "$BIN_DIR"

build_and_install_binary() {
  bin_name="$1"
  pkg_path="$2"
  target="$BIN_DIR/$bin_name"

  if [ -x "$target" ] && [ "$FORCE" = "0" ]; then
    log_ok "$bin_name: already installed at $target"
    return 0
  fi

  if [ "$DRY_RUN" = "1" ]; then
    log_info "[dry-run] would compile $pkg_path -> $target"
    return 0
  fi

  log_info "Compiling $bin_name..."
  if ( cd "$REPO_DIR" && go build -o "$target" "$pkg_path" ); then
    chmod 755 "$target"
    log_ok "$bin_name: installed to $target"
  else
    log_fatal "Failed to build $bin_name from $pkg_path"
  fi
}

build_and_install_binary "agentbus" "./cmd/agentbus"
build_and_install_binary "busmon"   "./cmd/busmon"

# Verify PATH
case ":$PATH:" in
  *":$BIN_DIR:"*)
    log_ok "PATH: $BIN_DIR is in your active PATH"
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
  else
    if [ -f "$REPO_DIR/scripts/link-role-skills.sh" ]; then
      log_info "Linking role skills for AI clients..."
      if "$REPO_DIR/scripts/link-role-skills.sh" >/dev/null 2>&1; then
        log_ok "Role skills: linked successfully"
      else
        log_warn "Role skills linking had warnings or skipped non-configured clients."
      fi
    fi
  fi
fi

# -----------------------------------------------------------------------------
# 4. Redis Broker Setup (:6380)
# -----------------------------------------------------------------------------
log_step "4/5 Checking Redis broker service"

is_port_open() {
  host="$1"
  port="$2"
  if command -v nc >/dev/null 2>&1; then
    nc -z "$host" "$port" 2>/dev/null
    return $?
  fi
  # Fallback: /dev/tcp test in sh/bash if available
  (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null && exec 3<&- && return 0
  return 1
}

if is_port_open "127.0.0.1" 6380; then
  log_ok "Redis broker: active and listening on 127.0.0.1:6380"
else
  if [ "$START_DOCKER" = "1" ] && [ "$DOCKER_OK" = "1" ] && [ -n "$DOCKER_COMPOSE_CMD" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      log_info "[dry-run] would start Redis container via $DOCKER_COMPOSE_CMD up -d"
    else
      log_info "Redis not detected on :6380. Starting broker container..."
      if ( cd "$REPO_DIR" && $DOCKER_COMPOSE_CMD up -d >/dev/null 2>&1 ); then
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
        log_warn "Failed to launch Redis container via $DOCKER_COMPOSE_CMD up -d"
      fi
    fi
  else
    log_warn "Redis broker not reachable on 127.0.0.1:6380 (run 'docker compose up -d' manually in $REPO_DIR)."
  fi
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
printf "\n%s=================================================================%s\n" "$GREEN" "$RESET"
printf "%s%s Agent Bus Monitor — Installation Complete! %s\n" "$BOLD" "$GREEN" "$RESET"
printf "%s=================================================================%s\n\n" "$GREEN" "$RESET"

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
