#!/usr/bin/env bash
# Run the whole bootstrap-tooling suite. Dependency-free (bash + python3 + coreutils).
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
rc=0
for t in "$DIR"/*_test.sh; do
  echo "### $(basename "$t")"
  bash "$t" || rc=1
done
exit "$rc"
