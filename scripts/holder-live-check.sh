#!/bin/sh
# The holder's live check before a release (holder hardening spec §5): a
# disposable project, its holder killed, its recovery timed, then removed.
# POSIX sh: macOS, and Linux with or without systemd. Options:
#   --clients fake|real   fake (default): no model, no quota
#   --rounds <n>          crashes in a row (default 1)
#   --keep                leave the test project running
exec "${AGENTBUS_BIN:-agentbus}" check holder-restart "$@"
