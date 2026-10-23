#!/usr/bin/env bash
# Run the whole bootstrap-tooling suite (bash + go + coreutils). One agentbus build serves
# every test: the scripts resolve roles through `agentbus roles`.
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Always this checkout's build: an inherited AGENTBUS_BIN is the installed binary (tests/lib.sh).
build="$(mktemp -d)"; trap 'rm -rf "$build"' EXIT
( cd "$DIR/.." && go build -o "$build/agentbus" ./cmd/agentbus )
export AGENTBUS_TEST_BIN="$build/agentbus"
rc=0
for t in "$DIR"/*_test.sh; do
  echo "### $(basename "$t")"
  bash "$t" || rc=1
done
exit "$rc"
