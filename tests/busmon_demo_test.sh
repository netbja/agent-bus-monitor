#!/usr/bin/env bash
# The demo script seeds a bus by wiping state first. That is only ever safe on a
# container it created itself, so the ownership check IS the safety feature —
# exactly like compose_test.sh guards the broker's loopback binding. These are
# the tripwires: a reused container, an unscoped wipe, or a stray REDIS_URL each
# turn a demo into a data-loss incident on somebody's real bus.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

SCRIPT="$REPO/scripts/busmon-demo.sh"
src="$(cat "$SCRIPT")"

# ---- static tripwires ------------------------------------------------------

# Comments are allowed to name it — the ban is on calling it. Strip comment
# lines before looking, or the prose explaining why it is absent trips the wire.
code="$(grep -v '^[[:space:]]*#' "$SCRIPT")"
if [[ "$code" == *FLUSHALL* ]]; then
  _bad "script calls FLUSHALL (an unscoped wipe of whatever bus it is pointed at)"
else
  _ok "no FLUSHALL call (the wipe cannot leave its namespace)"
fi

assert_contains "$src" "--scan --pattern '\$PROJECT:*'" "the wipe is scoped to the demo namespace"
assert_contains "$src" '--label "$LABEL=1"' "containers it creates are labelled as its own"
assert_contains "$src" 'unset REDIS_URL' "run neutralises REDIS_URL (it outranks REDIS_HOST/PORT)"
assert_contains "$src" 'REDIS_URL= REDIS_HOST=' "capture neutralises REDIS_URL too"
assert_contains "$src" '127.0.0.1:$PORT:6379' "the demo broker is bound to loopback only"

# ---- behaviour with a foreign container ------------------------------------

stub="$(mktemp -d)"
trap 'rm -rf "$stub"' EXIT
export CALLS="$stub/docker.calls"
cat > "$stub/docker" <<'STUB'
#!/usr/bin/env bash
echo "$*" >> "$CALLS"
case "$1" in
  ps)      [[ "${FAKE_EXISTS:-0}" == 1 ]] && echo "agent-bus-demo" ;;
  inspect) [[ "${FAKE_OWNED:-0}" == 1 ]] && echo "1" ;;
esac
exit 0
STUB
chmod +x "$stub/docker"

run_demo() { # <subcommand> — with the stubbed docker on PATH
  PATH="$stub:$PATH" bash "$SCRIPT" "$@" 2>&1
}

# A container of the same name that this script did not create: every verb must
# refuse it, and above all must not remove it.
: > "$CALLS"
export FAKE_EXISTS=1 FAKE_OWNED=0
out="$(run_demo up || true)"
assert_contains "$out" "refusing" "up refuses a container it does not own"
calls="$(cat "$CALLS")"
if [[ "$calls" == *"start"* || "$calls" == *"run -d"* ]]; then
  _bad "up started or created a container despite the refusal"
else
  _ok "up touched no container it does not own"
fi

: > "$CALLS"
out="$(run_demo down || true)"
assert_contains "$out" "refusing" "down refuses a container it does not own"
if [[ "$(cat "$CALLS")" == *"rm -f"* ]]; then
  _bad "down force-removed a container it does not own"
else
  _ok "down removed nothing it does not own"
fi

# Nothing to remove is a quiet success, so a repeated teardown does not fail.
: > "$CALLS"
export FAKE_EXISTS=0
assert_exit 0 "down with no container is a no-op success" -- env PATH="$stub:$PATH" bash "$SCRIPT" down
if [[ "$(cat "$CALLS")" == *"rm -f"* ]]; then
  _bad "down tried to remove a container that does not exist"
else
  _ok "down with no container removes nothing"
fi

finish
