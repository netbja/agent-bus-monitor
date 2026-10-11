#!/usr/bin/env bash
# Tiny assert helpers + a PATH-stub maker. Sourced by every tests/*_test.sh.
# Note: intentionally NOT `set -e` — we run all asserts and count failures.
set -uo pipefail
# Hermetic: a test run from inside a herdr pane must not reach the live herdr or broker
# (agent-launch binds through agentbus when HERDR_ENV=1).
unset HERDR_ENV HERDR_PANE_ID REDIS_URL
# The scripts resolve roles through `agentbus roles`: build it once if run.sh did not.
if [[ -z "${AGENTBUS_BIN:-}" ]]; then
  _ab_build="$(mktemp -d)"
  ( cd "$(dirname "${BASH_SOURCE[0]}")/.." && go build -o "$_ab_build/agentbus" ./cmd/agentbus ) || { echo "lib.sh: cannot build agentbus" >&2; exit 1; }
  export AGENTBUS_BIN="$_ab_build/agentbus"
fi

_pass=0; _fail=0
_ok()  { _pass=$((_pass+1)); echo "  ok: $1"; }
_bad() { _fail=$((_fail+1)); echo "  FAIL: $1" >&2; }

assert_eq()       { if [[ "$1" == "$2" ]]; then _ok "$3"; else _bad "$3 (got [$1] want [$2])"; fi; }
assert_contains() { if [[ "$1" == *"$2"* ]]; then _ok "$3"; else _bad "$3 (missing [$2] in [$1])"; fi; }
assert_exit()     { # <expected_code> <msg> -- <cmd...>
  local want="$1" msg="$2"; shift 3
  local got=0; "$@" >/dev/null 2>&1 || got=$?
  if [[ "$got" == "$want" ]]; then _ok "$msg"; else _bad "$msg (exit $got want $want)"; fi
}
finish() { echo "== $_pass passed, $_fail failed =="; [[ "$_fail" == 0 ]]; }

make_stub() { # <dir> <name> [exit_code=0] [stdout='']
  local dir="$1" name="$2" code="${3:-0}" out="${4:-}"
  mkdir -p "$dir"
  cat > "$dir/$name" <<EOF
#!/usr/bin/env bash
echo "\$@" >> "$dir/$name.calls"
[[ -n "$out" ]] && printf '%s\n' "$out"
exit $code
EOF
  chmod +x "$dir/$name"
}
