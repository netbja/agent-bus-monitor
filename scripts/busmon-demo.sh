#!/usr/bin/env bash
# busmon-demo — an isolated bus with reproducible demo data, for renders and
# manual inspection.
#
# It never touches the real broker: it runs its own redis container on port
# 6390 (the project's broker is 6380) under the container name below, and every
# key it writes belongs to the throwaway project "demo".
#
#   scripts/busmon-demo.sh up                 start the container and seed it
#   scripts/busmon-demo.sh run [args…]        run ./busmon against the demo bus
#   scripts/busmon-demo.sh capture OUT [BIN]  capture one screen to OUT (tmux)
#   scripts/busmon-demo.sh down               remove the container
#
# The seeded data is deliberately awkward: an agent that declared "working" and
# then went silent, an agent known only to the agents hash, a long multi-line
# Unicode report, a report from before full-text retention, an answered thread,
# an unanswered directive, and four tracked requests covering every state.
set -euo pipefail

NAME=agent-bus-demo
PORT=6390
PASS=AgentBus2025!
PROJECT=demo
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

r() { docker exec -i "$NAME" redis-cli -a "$PASS" --no-auth-warning "$@" >/dev/null; }

now_ms() { echo $(($(date +%s) * 1000)); }

up() {
  if ! docker ps -a --format '{{.Names}}' | grep -qx "$NAME"; then
    docker run -d --name "$NAME" -p "127.0.0.1:$PORT:6379" redis:8-alpine \
      redis-server --requirepass "$PASS" >/dev/null
  else
    docker start "$NAME" >/dev/null
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
  local now min
  now=$(now_ms)
  min=60000
  r FLUSHALL

  # ---- status: coder declared working 18 minutes ago and has said nothing since
  r XADD "$PROJECT:status" $((now - 18 * min))-0 agent coder state working message "refactoring the stream parser"
  r XADD "$PROJECT:status" $((now - 9 * min))-0 agent foureyes state idle message "waiting for a diff to review"
  r XADD "$PROJECT:status" $((now - 40000))-0 agent hermes state working message "driving the slice"
  r XADD "$PROJECT:status" $((now - 25000))-0 agent sentinel state blocked message "no API key for the nightly refresh"

  # ---- the agents hash: authoritative roster. architect last spoke long before
  # the ACTIVITY window, so only this hash knows about it.
  r HSET "$PROJECT:agents" coder "{\"state\":\"working\",\"message\":\"refactoring the stream parser\",\"ts\":$((now - 18 * min)),\"pane\":\"w1:p1\",\"session\":\"s-coder\"}"
  r HSET "$PROJECT:agents" foureyes "{\"state\":\"idle\",\"message\":\"waiting for a diff to review\",\"ts\":$((now - 9 * min)),\"pane\":\"w1:p2\"}"
  r HSET "$PROJECT:agents" hermes "{\"state\":\"working\",\"message\":\"driving the slice\",\"ts\":$((now - 40000)),\"pane\":\"w2:p1\"}"
  r HSET "$PROJECT:agents" sentinel "{\"state\":\"blocked\",\"message\":\"no API key for the nightly refresh\",\"ts\":$((now - 25000))}"
  r HSET "$PROJECT:agents" architect "{\"state\":\"done\",\"message\":\"verdict on the gRPC question\",\"ts\":$((now - 300 * min))}"

  # ---- reports: one long multi-line Unicode report with retained full text,
  # one from before full-text retention existed (preview only, unmarked).
  r XADD "$PROJECT:report" $((now - 12 * min))-0 agent coder kind note \
    message "résumé du refactor: 3 étapes, 2 faites — le parser accepte désormais les entrées sans champ full…" \
    full "résumé du refactor:

  1. relire le contrat Coordination v1 ✅
  2. écrire les tests de rendu ✅
  3. brancher la vue de suivi sur le board — en cours

détails:
  - les entrées anciennes n'ont pas de marqueur text_complete, donc la
    complétude est inconnue et l'écran doit le dire
  - 日本語の行も通る
  - la ligne suivante est délibérément très longue pour vérifier le retour à la ligne dans un terminal étroit"
  r XADD "$PROJECT:report" $((now - 6 * min))-0 agent foureyes kind note \
    message "relecture du diff: deux remarques, rien de bloquant"

  # ---- notify
  r XADD "$PROJECT:notify" $((now - 5 * min))-0 from hermes message "slice 1 gelée pour revue — ne pas merger"

  # ---- cmd: one answered exchange, one directive still unanswered, and a
  # challenge threaded on a subject ref rather than a stream id.
  r XADD "$PROJECT:cmd" $((now - 30 * min))-0 from hermes target coder type directive ref "" command "rebase sur main et relance la suite"
  r XADD "$PROJECT:cmd" $((now - 28 * min))-0 from coder target hermes type reply ref $((now - 30 * min))-0 command "fait, suite verte"
  r XADD "$PROJECT:cmd" $((now - 11 * min))-0 from hermes target foureyes type directive ref "" command "relis la tranche busmon quand elle est stable"
  r XADD "$PROJECT:cmd" $((now - 4 * min))-0 from foureyes target coder type challenge ref "pr-42" command "explique pourquoi le parser ignore le champ full"

  # ---- board: four tracked requests (one per state) plus a plain task.
  # Shape and field names come straight from the Coordination v1 contract.
  r HSET "$PROJECT:board" task-20 "{\"owner\":\"foureyes\",\"state\":\"requested\",\"updated\":$(((now - 42 * min) / 1000)),\"request\":{\"id\":\"$((now - 42 * min))-0\",\"thread\":\"$((now - 42 * min))-0\",\"from\":\"hermes\",\"target\":\"foureyes\",\"created_at\":$((now - 42 * min)),\"expires_at\":$((now + 18 * min)),\"delivery\":\"output_written\",\"attempts\":1}}"
  r HSET "$PROJECT:board" task-21 "{\"owner\":\"coder\",\"state\":\"accepted\",\"branch\":\"coder/task-21\",\"updated\":$(((now - 20 * min) / 1000)),\"request\":{\"id\":\"$((now - 120 * min))-0\",\"thread\":\"$((now - 120 * min))-0\",\"from\":\"hermes\",\"target\":\"coder\",\"created_at\":$((now - 120 * min)),\"expires_at\":0,\"delivery\":\"queued\",\"accepted_at\":$((now - 20 * min))}}"
  r HSET "$PROJECT:board" task-22 "{\"owner\":\"coder\",\"state\":\"blocked\",\"updated\":$(((now - 60 * min) / 1000)),\"request\":{\"id\":\"$((now - 180 * min))-0\",\"thread\":\"$((now - 180 * min))-0\",\"from\":\"hermes\",\"target\":\"coder\",\"created_at\":$((now - 180 * min)),\"expires_at\":0,\"delivery\":\"output_written\",\"blocked_reason\":\"clé API absente sur le VDR\"}}"
  r HSET "$PROJECT:board" task-19 "{\"owner\":\"architect\",\"state\":\"done\",\"branch\":\"architect/task-19\",\"updated\":$(((now - 200 * min) / 1000)),\"request\":{\"id\":\"$((now - 300 * min))-0\",\"thread\":\"$((now - 300 * min))-0\",\"from\":\"hermes\",\"target\":\"architect\",\"created_at\":$((now - 300 * min)),\"expires_at\":0,\"delivery\":\"output_written\",\"accepted_at\":$((now - 280 * min)),\"response_id\":\"$((now - 200 * min))-0\",\"responded_at\":$((now - 200 * min))}}"
  r HSET "$PROJECT:board" task-23 "{\"owner\":\"sentinel\",\"state\":\"working\",\"branch\":\"sentinel/task-23\",\"updated\":$(((now - 3 * min) / 1000))}"

  # ---- leases, budget, per-agent usage
  r SET "$PROJECT:pilot" hermes EX 300
  r SET "$PROJECT:armed:coder" "coder@laptop" EX 240
  r SET "$PROJECT:armed:foureyes" "foureyes@laptop" EX 240
  r HSET "$PROJECT:budget" anthropic "{\"provider\":\"anthropic\",\"session_pct\":25,\"weekly_pct\":44,\"ts\":$now}"
  r HSET "$PROJECT:usage" coder "{\"model\":\"opus\",\"ctx\":\"120k ctx\",\"provider\":\"anthropic\",\"ts\":$now}"
  r HSET "$PROJECT:usage" hermes "{\"model\":\"opus\",\"ctx\":\"84k ctx\",\"provider\":\"anthropic\",\"ts\":$now}"

  # ---- a gate on coder, so the 🔒 badge has something to show
  r HSET "$PROJECT:gate:coder" "pr-42" "foureyes|explique le champ full"
}

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
    "REDIS_HOST=127.0.0.1 REDIS_PORT=$PORT REDIS_PASSWORD='$PASS' '$bin' --project $PROJECT --limit 25"
  sleep 2.5
  for key in "${@:3}"; do tmux send-keys -t "$session" "$key"; sleep 0.6; done
  tmux capture-pane -p -t "$session" >"$out"
  tmux kill-session -t "$session" 2>/dev/null || true
  echo "captured $cols×$rows → $out"
}

down() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
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
