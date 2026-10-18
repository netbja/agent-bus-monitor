#!/usr/bin/env bash
# busmon-demo — an isolated bus with reproducible demo data, for renders and
# manual inspection.
#
# It never touches the real broker, and it never touches a container it did not
# create: it runs its own redis on port 6390 (the project's broker is 6380) in a
# container labelled busmon-demo=1, and every key it writes belongs to the
# throwaway project "acme-shop" (a fictional web shop). A container of the same name without that label is
# somebody else's, and this script refuses it rather than reusing it — the seed
# wipes state, and wiping state you do not own is how a demo eats a real bus.
#
#   scripts/busmon-demo.sh up                 start the container and seed it
#   scripts/busmon-demo.sh run [args…]        run ./busmon against the demo bus
#   scripts/busmon-demo.sh capture OUT [BIN]  capture one screen to OUT (tmux; ANSI=1 keeps colours)
#   scripts/busmon-demo.sh down               remove the container
#
# The seeded data is deliberately awkward: an agent that declared "working" and
# then went silent, an agent known only to the agents hash, a long multi-line
# Unicode report, a report from before full-text retention, an answered thread,
# an unanswered directive, and four tracked requests covering every state.
set -euo pipefail

NAME=agent-bus-demo
LABEL=busmon-demo
PORT=6390
PASS=AgentBus2025!
PROJECT=acme-shop
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

r() { docker exec -i "$NAME" redis-cli -a "$PASS" --no-auth-warning "$@" >/dev/null; }

now_ms() { echo $(($(date +%s) * 1000)); }

exists() { docker ps -a --format '{{.Names}}' | grep -qx "$NAME"; }

# owned is the whole safety story: only a container this script created carries
# the label, and only a labelled container is ever started, seeded or removed.
owned() {
  [[ "$(docker inspect -f "{{index .Config.Labels \"$LABEL\"}}" "$NAME" 2>/dev/null)" == "1" ]]
}

require_owned() {
  if ! owned; then
    echo "refusing: a container named '$NAME' exists but carries no $LABEL=1 label," >&2
    echo "so this script did not create it. It will not be started, seeded or removed." >&2
    echo "Rename or remove it yourself if the name is genuinely free." >&2
    exit 1
  fi
}

# wipe_namespace clears ONLY {project}:* inside our own container. Deliberately
# not FLUSHALL: a wipe that is not scoped to the namespace it owns is one typo
# away from erasing a real bus.
wipe_namespace() {
  docker exec -i "$NAME" sh -c \
    "redis-cli -a '$PASS' --no-auth-warning --scan --pattern '$PROJECT:*' | xargs -r redis-cli -a '$PASS' --no-auth-warning DEL" >/dev/null
}

up() {
  if exists; then
    require_owned
    docker start "$NAME" >/dev/null
  else
    docker run -d --name "$NAME" --label "$LABEL=1" -p "127.0.0.1:$PORT:6379" redis:8-alpine \
      redis-server --requirepass "$PASS" >/dev/null
  fi
  for _ in $(seq 30); do
    docker exec -i "$NAME" redis-cli -a "$PASS" --no-auth-warning ping 2>/dev/null | grep -q PONG && break
    sleep 0.2
  done
  seed
  echo "demo bus ready on 127.0.0.1:$PORT, project '$PROJECT'"
  echo "run it with: scripts/busmon-demo.sh run"
}

seed() {
  require_owned
  local now min
  now=$(now_ms)
  min=60000
  wipe_namespace

  # ---- status: coder declared working 18 minutes ago and has said nothing since
  r XADD "$PROJECT:status" $((now - 18 * min))-0 agent coder state working message "refactoring the stream parser"
  r XADD "$PROJECT:status" $((now - 9 * min))-0 agent foureyes state idle message "waiting for a diff to review"
  r XADD "$PROJECT:status" $((now - 40000))-0 agent master state working message "driving the slice"
  r XADD "$PROJECT:status" $((now - 25000))-0 agent sentinel state blocked message "no API key for the nightly refresh"

  # ---- the agents hash: authoritative roster. architect last spoke long before
  # the ACTIVITY window, so only this hash knows about it.
  r HSET "$PROJECT:agents" coder "{\"state\":\"working\",\"message\":\"refactoring the stream parser\",\"ts\":$((now - 18 * min)),\"pane\":\"w1:p1\",\"session\":\"s-coder\"}"
  r HSET "$PROJECT:agents" foureyes "{\"state\":\"idle\",\"message\":\"waiting for a diff to review\",\"ts\":$((now - 9 * min)),\"pane\":\"w1:p2\"}"
  r HSET "$PROJECT:agents" master "{\"state\":\"working\",\"message\":\"driving the slice\",\"ts\":$((now - 40000)),\"pane\":\"w2:p1\"}"
  r HSET "$PROJECT:agents" sentinel "{\"state\":\"blocked\",\"message\":\"no API key for the nightly refresh\",\"ts\":$((now - 25000))}"
  r HSET "$PROJECT:agents" architect "{\"state\":\"done\",\"message\":\"verdict on the caching question\",\"ts\":$((now - 300 * min))}"

  # ---- reports: one long multi-line Unicode report with retained full text,
  # one from before full-text retention existed (preview only, unmarked).
  r XADD "$PROJECT:report" $((now - 12 * min))-0 agent coder kind note \
    message "refactor summary: 3 steps, 2 done — the parser now accepts entries with no full field…" \
    full "refactor summary:

  1. read the Coordination v1 contract ✅
  2. write the render tests ✅
  3. wire the tracking view to the board — in progress

details:
  - old entries carry no text_complete marker, so completeness is unknown
    and the screen has to say so
  - 日本語の行も通る
  - the next line is deliberately very long to check wrapping in a narrow terminal"
  r XADD "$PROJECT:report" $((now - 6 * min))-0 agent foureyes kind note \
    message "diff review: two remarks, nothing blocking"
  # A Coordination v1 writer marks completeness. Seeded alongside the unmarked
  # ones above so the demo shows both readings side by side: this entry can be
  # promised whole, the older ones cannot.
  r XADD "$PROJECT:report" $((now - 3 * min))-0 agent sentinel kind note text_complete yes \
    message "daily review: budget 25%/44%, no task blocked since yesterday" \
    full "daily review

budget: 25% of the session window, 44% of the week
tasks: none blocked since yesterday
note: this entry carries the text_complete marker, so its text can be
announced as complete — unlike the older entries."

  # ---- notify
  r XADD "$PROJECT:notify" $((now - 5 * min))-0 from master message "slice 1 frozen for review — do not merge"

  # ---- cmd, in id order (XADD only accepts increasing ids).
  #
  # The three newest tracked requests point at REAL entries here, exactly as
  # bus.Request does when it publishes the directive and records its id. That
  # matters for the demo: Board() derives availability by reading the body back,
  # so a request whose id names nothing would render as "not retained".
  # task-19 is the deliberate exception — five hours old, its body no longer in
  # the stream, which is what an aged-out request actually looks like.
  r XADD "$PROJECT:cmd" $((now - 180 * min))-0 from master target coder type directive ref "" \
    command "instrument the sentinel's resume path" task task-22 expires_at 0 text_complete yes
  r XADD "$PROJECT:cmd" $((now - 120 * min))-0 from master target coder type directive ref "" \
    command "add the delivery counters to the board" task task-21 expires_at 0 text_complete yes
  r XADD "$PROJECT:cmd" $((now - 42 * min))-0 from master target foureyes type directive ref "" \
    command "review the coordination slice before the freeze" task task-20 expires_at $((now + 18 * min)) text_complete yes
  r XADD "$PROJECT:cmd" $((now - 30 * min))-0 from master target coder type directive ref "" command "rebase on main and rerun the suite"
  r XADD "$PROJECT:cmd" $((now - 28 * min))-0 from coder target master type reply ref $((now - 30 * min))-0 command "done, suite green"
  r XADD "$PROJECT:cmd" $((now - 11 * min))-0 from master target foureyes type directive ref "" command "review the busmon slice once it is stable"
  r XADD "$PROJECT:cmd" $((now - 4 * min))-0 from foureyes target coder type challenge ref "pr-42" command "explain why the parser ignores the full field"

  # ---- board: four tracked requests (one per state) plus a plain task.
  # Shape and field names come straight from the Coordination v1 contract.
  r HSET "$PROJECT:board" task-20 "{\"owner\":\"foureyes\",\"state\":\"requested\",\"updated\":$(((now - 42 * min) / 1000)),\"request\":{\"id\":\"$((now - 42 * min))-0\",\"thread\":\"$((now - 42 * min))-0\",\"from\":\"master\",\"target\":\"foureyes\",\"created_at\":$((now - 42 * min)),\"expires_at\":$((now + 18 * min)),\"delivery\":\"output_written\",\"attempts\":1}}"
  r HSET "$PROJECT:board" task-21 "{\"owner\":\"coder\",\"state\":\"accepted\",\"branch\":\"coder/task-21\",\"updated\":$(((now - 20 * min) / 1000)),\"request\":{\"id\":\"$((now - 120 * min))-0\",\"thread\":\"$((now - 120 * min))-0\",\"from\":\"master\",\"target\":\"coder\",\"created_at\":$((now - 120 * min)),\"expires_at\":0,\"delivery\":\"queued\",\"accepted_at\":$((now - 20 * min))}}"
  r HSET "$PROJECT:board" task-22 "{\"owner\":\"coder\",\"state\":\"blocked\",\"updated\":$(((now - 60 * min) / 1000)),\"request\":{\"id\":\"$((now - 180 * min))-0\",\"thread\":\"$((now - 180 * min))-0\",\"from\":\"master\",\"target\":\"coder\",\"created_at\":$((now - 180 * min)),\"expires_at\":0,\"delivery\":\"output_written\",\"blocked_reason\":\"API key missing on host1\"}}"
  r HSET "$PROJECT:board" task-19 "{\"owner\":\"architect\",\"state\":\"done\",\"branch\":\"architect/task-19\",\"updated\":$(((now - 200 * min) / 1000)),\"request\":{\"id\":\"$((now - 300 * min))-0\",\"thread\":\"$((now - 300 * min))-0\",\"from\":\"master\",\"target\":\"architect\",\"created_at\":$((now - 300 * min)),\"expires_at\":0,\"delivery\":\"output_written\",\"accepted_at\":$((now - 280 * min)),\"response_id\":\"$((now - 200 * min))-0\",\"responded_at\":$((now - 200 * min))}}"
  r HSET "$PROJECT:board" task-23 "{\"owner\":\"sentinel\",\"state\":\"working\",\"branch\":\"sentinel/task-23\",\"updated\":$(((now - 3 * min) / 1000))}"

  # ---- leases, budget, per-agent usage
  r SET "$PROJECT:pilot" master EX 300
  r SET "$PROJECT:dispatch" demo-dispatcher EX 300
  r SET "$PROJECT:armed:coder" "coder@laptop" EX 240
  r SET "$PROJECT:armed:foureyes" "foureyes@laptop" EX 240
  r HSET "$PROJECT:budget" anthropic "{\"provider\":\"anthropic\",\"windows\":[{\"name\":\"5h\",\"used_pct\":52,\"resets_at\":$((now / 1000 + 2 * 3600))},{\"name\":\"weekly\",\"used_pct\":44,\"resets_at\":$((now / 1000 + 3 * 86400))}],\"observed_at\":$now,\"collected_at\":$now,\"ts\":$now}"
  r HSET "$PROJECT:budget" openai "{\"provider\":\"openai\",\"windows\":[{\"name\":\"5h\",\"used_pct\":31,\"resets_at\":$((now / 1000 + 3 * 3600))}],\"observed_at\":$now,\"collected_at\":$now,\"ts\":$now}"
  r HSET "$PROJECT:usage" coder "{\"model\":\"opus\",\"ctx\":\"120k ctx\",\"provider\":\"anthropic\",\"ts\":$now}"
  r HSET "$PROJECT:usage" master "{\"model\":\"opus\",\"ctx\":\"84k ctx\",\"provider\":\"anthropic\",\"ts\":$now}"

  # ---- a gate on coder, so the 🔒 badge has something to show
  r HSET "$PROJECT:gate:coder" "pr-42" "foureyes|explain the full field"
}

# REDIS_URL wins over REDIS_HOST/PORT in bus.Connect, so leaving it set would
# quietly point a "demo" run at whatever bus it names — including the real one.
env_for_demo() {
  export REDIS_HOST=127.0.0.1 REDIS_PORT=$PORT REDIS_PASSWORD=$PASS
  unset REDIS_URL || true
  export AGENT_BUS_PROJECT=$PROJECT
}

run() {
  env_for_demo
  exec "$HERE/busmon" "$@"
}

# capture OUT [BIN] — draw one screen of BIN (default ./busmon) into OUT.
# tmux gives a real pty at a fixed size, so the render is reproducible.
capture() {
  local out=${1:?usage: capture OUT [BIN]} bin=${2:-$HERE/busmon}
  local session="busmon-demo-$$"
  local cols=${COLS:-120} rows=${ROWS:-34}
  mkdir -p "$(dirname "$out")"
  tmux new-session -d -s "$session" -x "$cols" -y "$rows" \
    "REDIS_URL= REDIS_HOST=127.0.0.1 REDIS_PORT=$PORT REDIS_PASSWORD='$PASS' '$bin' --project $PROJECT --limit 25"
  sleep 2.5
  for key in "${@:3}"; do tmux send-keys -t "$session" "$key"; sleep 0.6; done
  # ANSI=1 keeps the colours, for a renderer such as freeze
  tmux capture-pane -p ${ANSI:+-e} -t "$session" >"$out"
  tmux kill-session -t "$session" 2>/dev/null || true
  echo "captured $cols×$rows → $out"
}

down() {
  if ! exists; then
    echo "no '$NAME' container — nothing to remove"
    return
  fi
  require_owned
  docker rm -f "$NAME" >/dev/null
  echo "demo bus removed"
}

case "${1:-}" in
  up) up ;;
  seed) seed ;;
  run) shift; run "$@" ;;
  capture) shift; capture "$@" ;;
  down) down ;;
  *) sed -n '2,16p' "${BASH_SOURCE[0]}" | sed 's/^# \?//' ; exit 1 ;;
esac
