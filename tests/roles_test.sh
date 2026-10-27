#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
source "$REPO/scripts/lib/roles.sh"

assert_eq   "$(role_field master model)"      "sonnet"  "master model"
assert_eq   "$(role_field master permission)" "acceptEdits"      "master permission"
assert_eq   "$(role_field coder permission)"  "bypassPermissions" "coder permission"
assert_eq   "$(role_field architect fallback)" "claude-opus-5" "architect fallback"
assert_eq   "$(role_field coder fallback)"    ""                 "coder has no fallback"
assert_contains "$(role_field coder skills)"  "tdd"              "coder skills include tdd"
assert_exit 0 "role_exists coder"    -- role_exists coder
assert_exit 3 "role_exists nobody"   -- role_exists nobody
assert_contains "$(roles_by_tier boot | tr '\n' ' ')" "sentinel" "sentinel is boot-tier"
assert_eq   "$(roles_by_tier pop | tr '\n' ' ')" "architect deploy security docs ui-grok ui-kimi reviewer-codex " "architect + deploy + security + docs + the ui panel + reviewer-codex are the pop roles"
assert_eq   "$(role_field deploy model)"      "sonnet"  "deploy model"
finish
