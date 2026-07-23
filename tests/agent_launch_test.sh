#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
L="$REPO/scripts/agent-launch"

out="$(AGENT_LAUNCH_DRYRUN=1 "$L" master demo)"
assert_contains "$out" "--model claude-sonnet-5"            "master model flag"
assert_contains "$out" "--permission-mode acceptEdits"     "master permission flag"
assert_contains "$out" "--name demo:master"                "master session name"
assert_contains "$out" "roles/master.md"                   "master prompt file"

out="$(AGENT_LAUNCH_DRYRUN=1 "$L" coder demo)"
assert_contains "$out" "--permission-mode bypassPermissions" "coder permission flag"

out="$(AGENT_LAUNCH_DRYRUN=1 "$L" architect demo)"
assert_contains "$out" "--model claude-fable-5"            "architect model"
assert_contains "$out" "--fallback-model claude-opus-4-8"  "architect fallback"

assert_exit 1 "unknown role fails"     -- env AGENT_LAUNCH_DRYRUN=1 "$L" bogus demo
assert_exit 1 "invalid project fails"  -- env AGENT_LAUNCH_DRYRUN=1 "$L" master 1bad

# --- billing guard (real exec path, via a `claude` stub that reports its own env) ---------------
# A stray ANTHROPIC_API_KEY would silently bill every agent to pay-as-you-go instead of the
# subscription, with nothing on screen to say so.
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/claude" <<'CSTUB'
#!/usr/bin/env bash
echo "KEY=${ANTHROPIC_API_KEY:-<unset>} BASE=${ANTHROPIC_BASE_URL:-<unset>} TOKEN=${ANTHROPIC_AUTH_TOKEN:-<unset>}"
CSTUB
chmod +x "$tmp/claude"

out="$(PATH="$tmp:$PATH" ANTHROPIC_API_KEY=sk-should-be-dropped "$L" master demo)"
assert_contains "$out" "KEY=<unset>"          "ANTHROPIC_API_KEY dropped before exec"

# the third-party seam must survive — it's how a role reaches an Anthropic-compatible provider
out="$(PATH="$tmp:$PATH" ANTHROPIC_API_KEY=sk-x ANTHROPIC_BASE_URL=https://example.test ANTHROPIC_AUTH_TOKEN=tok "$L" master demo)"
assert_contains "$out" "BASE=https://example.test" "ANTHROPIC_BASE_URL preserved"
assert_contains "$out" "TOKEN=tok"                 "ANTHROPIC_AUTH_TOKEN preserved"

# explicit opt-in keeps the key
out="$(PATH="$tmp:$PATH" AGENT_LAUNCH_KEEP_API_KEY=1 ANTHROPIC_API_KEY=sk-keep "$L" master demo)"
assert_contains "$out" "KEY=sk-keep"          "AGENT_LAUNCH_KEEP_API_KEY=1 keeps the key"
finish
