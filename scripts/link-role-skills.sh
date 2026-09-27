#!/usr/bin/env bash
# Install role skills for selected clients; preflight before writing any links.
# Overrides: ROLES_TOML, SKILLS_DEST, REPO_SKILLS, POCOCK_SKILLS_ROOT.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$SCRIPT_DIR/lib/role_config.py" install
