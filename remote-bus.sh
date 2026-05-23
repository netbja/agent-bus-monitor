#!/usr/bin/env bash
# Watch a remote Agent Bus through an SSH tunnel, then tear it down on exit.
# Usage: ./remote-bus.sh <ssh-target> [remote-port=6380] [local-port=6381]
#   ./remote-bus.sh sysnet@192.168.1.5
set -euo pipefail

target="${1:?usage: remote-bus.sh <ssh-target> [remote-port=6380] [local-port=6381]}"
remote_port="${2:-6380}"
local_port="${3:-6381}"

ssh -NL "${local_port}:localhost:${remote_port}" "$target" &
tunnel_pid=$!
trap 'kill "$tunnel_pid" 2>/dev/null || true' EXIT
sleep 1

bin="$(dirname "$0")/busmon"
[ -x "$bin" ] || bin="busmon"
REDIS_PORT="$local_port" "$bin"
