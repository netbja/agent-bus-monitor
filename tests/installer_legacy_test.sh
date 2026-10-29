#!/usr/bin/env bash
# The installer suite against the bash installer, kept one release as scripts/setup-legacy.sh after
# the switch to the Go installer (rollout step 2 of docs/superpowers/specs/2026-10-28-go-installer-design.md):
# the same asserts as ./setup.sh, but the broker's ownership ones, which only the Go installer makes.
SETUP_SCRIPT=scripts/setup-legacy.sh exec bash "$(dirname "${BASH_SOURCE[0]}")/installer_test.sh"
