#!/usr/bin/env bash
# The broker's port binding is a security boundary, not a preference: the Redis password travels
# in plaintext, so the bus must never be LAN-reachable (remote access goes through the SSH tunnel).
# A one-character edit turns that off with nothing on screen to say so — this is the tripwire.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

compose="$(cat "$REPO/docker-compose.yml")"
assert_contains "$compose" '"127.0.0.1:6380:6379"' "broker bound to loopback only"
if [[ "$compose" == *'"0.0.0.0:6380:6379"'* || "$compose" == *'"6380:6379"'* ]]; then
  _bad "broker port published on all interfaces (plaintext password reachable from the LAN)"
else
  _ok "broker port not published on all interfaces"
fi
finish
