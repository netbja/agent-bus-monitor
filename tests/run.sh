#!/usr/bin/env bash
# Run the whole bootstrap-tooling suite. Dependency-free (bash + python3 + coreutils).
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
rc=0
for t in "$DIR"/*_test.sh; do
  echo "### $(basename "$t")"
  bash "$t" || rc=1
done
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s "$DIR" -p 'test_role_config.py' || rc=1
exit "$rc"
