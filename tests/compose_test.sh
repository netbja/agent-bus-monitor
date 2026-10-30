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
# The password (redis-password): read from the person's 0600 file through env_file (optional, so an
# install from before keeps the historical default), never put on the server's argv, where `ps` and
# `docker inspect` show it, nor on redis-cli's.
assert_contains "$compose" 'path: ${XDG_CONFIG_HOME:-${HOME}/.config}/agentbus/redis.env' "password file read through env_file"
assert_contains "$compose" 'required: false' "the password file is optional (an older install keeps its default)"
assert_contains "$compose" 'redis-server - --appendonly yes' "the server reads its password from a config on stdin"
assert_contains "$compose" 'REDISCLI_AUTH=' "the healthcheck authenticates through REDISCLI_AUTH"
assert_contains "$compose" '$${AGENTBUS_SHELL_PASSWORD:-$${REDIS_PASSWORD:-AgentBus2025!}}' "shell, then file, then the historical default, as the clients"
assert_contains "$compose" "sed 's/[\\\\\"]/\\\\&/g'" "a quote or backslash in the password is escaped for the config line"
if [[ "$compose" == *"--requirepass"* || "$compose" == *"redis-cli\", \"-a"* || "$compose" == *"redis-cli -a"* ]]; then
  _bad "the password is on a command line (server --requirepass or redis-cli -a)"
else
  _ok "the password is on no command line"
fi
finish
