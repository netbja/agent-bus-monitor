#!/usr/bin/env bash
# Test install.sh: options, dry-run, help, and binary installation
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

INSTALLER="$REPO/install.sh"

# Test 1: --help exits 0 and prints usage
out="$("$INSTALLER" --help 2>&1 || true)"
assert_contains "$out" "Automated Idempotent Installer" "help displays title"
assert_contains "$out" "--dry-run" "help lists --dry-run"
assert_exit 0 "help exits 0" -- "$INSTALLER" --help

# Test 2: Unknown flag exits non-zero
assert_exit 1 "unknown flag exits with code 1" -- "$INSTALLER" --unknown-flag-test

# Test 3: --dry-run executes successfully and prints check steps
dry_out="$("$INSTALLER" --dry-run 2>&1 || true)"
assert_contains "$dry_out" "Checking system platform & toolchain" "dry-run checks platform"
assert_contains "$dry_out" "Scanning installed AI coding agents" "dry-run scans AI agents"
assert_contains "$dry_out" "Installation Complete" "dry-run concludes with summary"
assert_exit 0 "dry-run exits 0" -- "$INSTALLER" --dry-run

# Test 4: Custom bin-dir with dry-run
custom_bin="/tmp/test-agent-bus-custom-bin-$$"
dry_custom="$("$INSTALLER" --dry-run --bin-dir "$custom_bin" 2>&1 || true)"
assert_contains "$dry_custom" "$custom_bin" "custom bin-dir recognized in dry-run"

finish
