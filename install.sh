#!/bin/sh
# Kept for existing instructions: ./install.sh [options] is ./setup.sh install [options].
exec "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/setup.sh" install "$@"
