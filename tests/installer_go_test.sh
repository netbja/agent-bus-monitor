#!/usr/bin/env bash
# The installer suite against the Go installer's thin script (rollout step 1 of
# docs/superpowers/specs/2026-10-28-go-installer-design.md): the same asserts as setup.sh, plus
# the broker's ownership ones.
SETUP_SCRIPT=scripts/setup-go.sh exec bash "$(dirname "${BASH_SOURCE[0]}")/installer_test.sh"
