#!/usr/bin/env bash
# Run the whole bootstrap-tooling suite (bash + go + coreutils). One agentbus build serves
# every test: the scripts resolve roles through `agentbus roles`.
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -z "${AGENTBUS_BIN:-}" ]]; then
  build="$(mktemp -d)"; trap 'rm -rf "$build"' EXIT
  ( cd "$DIR/.." && go build -o "$build/agentbus" ./cmd/agentbus )
  export AGENTBUS_BIN="$build/agentbus"
fi
rc=0
for t in "$DIR"/*_test.sh; do
  echo "### $(basename "$t")"
  bash "$t" || rc=1
done
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s "$DIR" -p 'test_role_config.py' || rc=1
exit "$rc"
