#!/usr/bin/env bash
# Install role skills for selected clients; preflight before writing any links.
# --missing-ok: link what exists, warn for a skill that is not installed (bootstrap uses it).
# Overrides: ROLES_TOML, SKILLS_DEST, REPO_SKILLS, POCOCK_SKILLS_ROOT.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/lib/roles.sh"
exec "$AGENTBUS_BIN" roles install "$@"
