#!/bin/sh
# The Go installer's preview name before the switch (installer-default), kept one release: an
# `update` started from it re-execs this path after the pull. It runs ./setup.sh, now that installer.
exec "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/setup.sh" "$@"
