# Agent Bus — Quick-Use Card for Agents

You are an agent (Claude Code, Codex, hermes, …) talking to other agents over a
shared **Redis Streams** bus. One CLI: **`agentbus`**. This card is the full
syntax — copy a line, change the words, run it. **Don't guess the flags; they
are listed verbatim below.**

---

## Drop this into your project's CLAUDE.md

`CLAUDE.md` is the one file every subagent reads. Paste this block into the
consuming project's `CLAUDE.md` (fill the two names) so subagents inherit bus
access without re-asking:

```markdown
## Agent Bus (coordination over Redis Streams)
- CLI: `agentbus` (from github.com/netbja/agent-bus-monitor; `go install ./...`).
- Identity / namespace (export once):
  `export AGENT_BUS_PROJECT=<project>` and `export AGENT_BUS_AGENT=<your-agent>`.
- Receive directives — arm as a background task; its exit wakes you, then re-arm:
  `agentbus subscribe "$AGENT_BUS_AGENT" --since "$LAST_CURSOR"`  # one JSON line/fire; persist its `id`
- Publish your state (this IS your heartbeat):
  `agentbus status "$AGENT_BUS_AGENT" working "<msg>"`
- Peers' current state: `agentbus agents`. Full reference: docs/AGENT-BUS-GUIDE.md.
```

---

## 0. Read this first — the 4 traps that make commands fail

These are the only reasons a well-formed-looking command gets rejected. Check
them before retrying anything.

1. **Project is mandatory.** Every call needs `--project <p>` (or `export
   AGENT_BUS_PROJECT=<p>`). No default. Missing it → `error: project required`.
2. **Flags use a DOUBLE dash and a SPACE — never `=`, never a single dash.**
   - ✅ `agentbus --project trading ...`   ✅ `--ref abc123`   ✅ `--auto`   ✅ `--ttl 90s`
   - ❌ `-project trading` (single dash — silently ignored → "project required")
   - ❌ `--ref=abc123` (the `=` form is NOT parsed → "usage" error)
   - (Exception: the `busmon` TUI uses Go flags and tolerates `-project` *or*
     `--project`. `agentbus` does **not** — always use `--project`.)
3. **Positional order is fixed.** `<agent>` and `<state>`/`<target>` come in the
   exact order shown. Trailing words are joined into the message, so the message
   always goes **last** and needs no quotes (quotes are fine too).
4. **`<agent>`/`<project>` names must match `^[a-z][a-z0-9_-]{0,31}$`** —
   lowercase, start with a **letter**, then letters/digits/`_`/`-`, ≤32 chars.
   - ✅ `claude1`  ✅ `claude_1`  ✅ `dev`  ✅ `hermes`  ✅ `c`
   - ❌ `Claude1` (uppercase)  ❌ `1claude` (leading digit)  ❌ `_claude` (leading `_`)  ❌ `claude.1` (dot)

State values are a closed set: **`working` · `idle` · `blocked` · `done`**.

---

## 1. 30-second setup

```bash
export AGENT_BUS_PROJECT=<project> # REQUIRED namespace (every stream is {project}:{kind})
export AGENT_BUS_AGENT=claude1     # who YOU are (used as `from` and pilot identity); default "hermes"
```

With those exported you can drop `--project` from every command below. The
broker defaults to `localhost:6380` (override with `--host` or `REDIS_*`).

> **Substitute your real project name — never paste the placeholder as-is.** A
> wrong-but-valid namespace does not error: the stream is created on first write,
> your messages land where nobody listens, and every command still exits 0.
> Observed in the wild: two agents reviewing the same PRs sat in two namespaces
> for 10 days, one side publishing "ready for review", the other replying
> elsewhere. Run `agentbus agents` and confirm you can see your peers before you
> trust the bus.

Sanity check it works:

```bash
agentbus notify "claude1 online"   # should return silently with exit 0
```

---

## 2. Cheat sheet — every command, copy-paste ready

Assumes `AGENT_BUS_PROJECT` is exported. If not, add `--project <p>` after `agentbus`.

```bash
# ── PUBLISH YOUR OWN STATE (this IS your heartbeat — emit often) ──────────────
agentbus status <agent> <working|idle|blocked|done> [message...]
agentbus status claude1 working plan 10 shipped         # message = trailing words, no quotes needed
agentbus status claude1 done

# ── REPORT (curated human-facing note) ───────────────────────────────────────
agentbus report <agent> [--auto] <message...>
agentbus report claude1 bug in order router fixed
agentbus report claude1 --auto soak 24h done            # --auto = Stop-hook safety-net report
agentbus reports                                        # list recent reports; (+N) = full text retained
agentbus reports --json                                 # same list, machine-readable JSON
agentbus reports <id>                                   # print that report's full text (multi-line, untruncated)

# ── NOTIFY (project-wide announcement, from = AGENT_BUS_AGENT) ────────────────
agentbus notify <message...>
agentbus notify soak test started

# ── DIRECT ANOTHER AGENT (directive on {project}:cmd) ─────────────────────────
agentbus cmd [--ref T] <target> <command...>           # prints the entry id (= thread root); --ref continues a thread
agentbus thread <thread-id>                            # show the :cmd thread (ref or id == arg), chronological
agentbus cmd claude2 run the integration suite
ID=$(agentbus cmd claude2 run the integration suite)   # capture the printed id = thread root
agentbus reply --ref "$ID" hermes on it                # thread a reply onto that directive
agentbus thread "$ID"                                  # see the whole chain (directive → reply → verdict)
# a full <ms>-<seq> id is an exact match; a bare <ms> is best-effort. Flag tokens (--ref) in cmd text are reserved.

# ── 4-EYES CHALLENGE GATE (blocks <target> until a verdict) ───────────────────
agentbus challenge <target> [--ref R] <why...>          # prints: "challenge <ref> opened on <target>"
agentbus challenge claude2 confirm you backed up the DB # auto-generates a ref, PRINTS it — capture it
agentbus reply   --ref <R> <target> <answer...>         # answer a challenge you received
agentbus verdict (--pr N | --subject S) [--ref R] <target> <approve|reject> [msg...]   # records to {p}:verdicts; --ref resolves the gate (best-effort)
agentbus verdict --pr 25 --ref k3f9q claude2 approve looks good
agentbus verdicts --pr 25                                          # roll-up 4-eyes state: APPROVED/REJECTED/PENDING (exit 0/3/2)

# ── AM I BLOCKED? (check before you proceed / mark done) ──────────────────────
agentbus gate <agent>                                   # lists open challenges; EXIT CODE != 0 means GATED
agentbus gate claude1 && echo "clear to proceed"

# ── PILOT LEASE (who is driving: hermes vs autonomous) ────────────────────────
agentbus pilot <claim|renew|release|status> [--ttl 90s]
agentbus pilot status                                   # prints "piloted by hermes" OR "autonomous"
agentbus pilot claim --ttl 120s                         # hermes only: take/renew the lease
agentbus pilot release                                  # hand off to autonomous now

# ── PEERS: current state of every agent (one line each) ───────────────────────
agentbus agents                                         # name · state · (message) · age; marks idle/offline; shows ⧉<pane> if attached to a herdr pane
agentbus agents --json                                  # raw map for scripts
agentbus pane <agent>                                   # print the agent's herdr pane (HERDR_PANE_ID); non-zero if none
agentbus usage                                          # per-agent: model · context fill; --json for raw
agentbus budget                                         # per-provider ACCOUNT window: session % / weekly % / resets; --json for raw
agentbus refresh                                        # republish both from local sources (the sentinel runs this); --quiet from cron
agentbus version                                        # print the bus protocol version (v1) — no project/broker needed

# ── BOARD: shared task ownership — check it BEFORE starting any task ──────────
agentbus board                                          # TASK OWNER STATE BRANCH AGE (newest first); --json for raw
agentbus board claim <task> [--branch <b>]              # take ownership (state=working); FAILS if a peer owns it — that refusal is the point
agentbus board state <task> <state>                     # move the task along (review, blocked, …)
agentbus board done <task>                              # merged/finished; a done task can be re-claimed by anyone
agentbus board drop <task>                              # release the task without doing it
# claim/state/done apply to UNTRACKED tasks only: on a tracked request they are refused
# ("tracked request: use request accept/block/done or explicit board drop"). drop is allowed.

# ── TRACKED REQUESTS: a directive whose acceptance and answer are RECORDED ────
# The <task> slug is the board key: `request send` creates the board entry in state
# "requested". Everything below is recorded on the board; nothing is ever inferred.
agentbus request send <task> <target> [--ref T] [--ttl 5m] <body>   # publish + record; prints the cmd id
agentbus request accept <task>                          # TARGET ONLY, before starting. The only thing that means "taken"
agentbus request block  <task> <reason>                 # TARGET ONLY; reason is required, it is what the human reads
agentbus request done   <task> <reply>                  # TARGET ONLY; requires a prior accept; publishes the reply on the thread
agentbus request                                        # EVERY tracked request, done ones included; --json for every field
# Reusing a <task> slug is REFUSED and returns the existing request id — a retry inspects, it never duplicates.
# `block` does NOT require a prior accept: a target can refuse work it never took.
# After a `block`, the request must be accepted again before it can be completed.
# A repeated `done` returns the FIRST response id and does not replace the text you sent before.
# --ttl is optional: without it there is NO deadline. The deadline gates the FIRST acceptance only:
#   never-accepted + expired      -> accept refused ("request expired; cannot accept")
#   never-accepted + body trimmed -> accept refused ("request body missing; cannot accept")
#   already-accepted              -> accept and done both still work, deadline or not
# A reply on the thread, a report, a status, or bytes delivered to a subscriber are NONE of them an acceptance.
# `board claim` / `board state` on a tracked task are REFUSED — the broker answers
#   "tracked request: use request accept/block/done or explicit board drop".
# `board drop` DOES work and is the sharp edge: it deletes the board entry, request metadata
#   included, WITHOUT cancelling the command already published or any work in flight. You lose
#   the tracking, not the action.

# ── SHUTDOWN: stop the whole team when the work is done (saves idle burn) ─────
agentbus shutdown                                       # broadcast "shutdown" to every peer; REFUSED while a board task isn't done or a peer is busy
agentbus shutdown --force                               # override the guard (you know better)
# peers then report, set done, and stop re-arming subscribe; master closes the panes (see the master skill)

# ── INBOUND: wait for a command addressed to you ─────────────────────────────
agentbus subscribe [--since <cursor>] <agent> [idle_secs]   # blocks for ONE cmd, emits ONE JSON object, EXITS; default idle 240s
agentbus subscribe claude1                              # no --since = start at "now" — see the warning below; arm as a background task
agentbus subscribe --since 1782053749061-3 claude1      # resume after a persisted cursor (the `id` from the last fire)
agentbus subscribe claude1 3600                         # 1h idle window before it heartbeats and exits
agentbus subscribe --loop hermes                        # HEADLESS callers only (hermes/shell): consume continuously, never exit
agentbus watch claude1                                  # legacy alias of subscribe
# WARNING — --since is a floor, and a floor DISCARDS. It filters pending entries too: anything
# older than the cursor is ACKNOWLEDGED WITHOUT BEING DELIVERED to you. So arming with no
# --since does not "skip the backlog for now", it consumes it unseen. Persist the `id` of the
# last event you handled and pass it back every time. Moving a cursor forward never means the
# work was accepted — only `request accept` does.

# ── DEBUG: tail streams to your terminal ─────────────────────────────────────
agentbus listen [status report notify cmd]              # default: all four
agentbus listen cmd report

# ── HUMAN DASHBOARD (separate binary) ─────────────────────────────────────────
busmon --project trading                                # or -project; busmon tolerates both
```

---

### Budget & usage: nobody tees anything

`agentbus refresh` reads the artefacts that already exist on disk and publishes **two** things.
No agent cooperates, so an agent that never publishes is still measured:

| Key | Scope | Source | Read with |
|---|---|---|---|
| `{p}:budget` | **account**, per provider — session %, weekly %, resets | ccstatusline's cache of Anthropic's OAuth usage endpoint (`~/.cache/ccstatusline/usage.json`) | `agentbus budget` |
| `{p}:usage`  | **one agent** — model, context fill | that agent's transcript, `~/.claude/projects/*/<session>.jsonl` | `agentbus usage` |

**Why two keys.** A session/weekly window belongs to the *subscription*, not to an agent: five
agents on one account share one number. Storing it per-agent (as the old status-line tee did)
kept five copies of the same figure and let a stale copy read as a per-agent fact.

**How an agent's transcript is found.** `agentbus status` captures `CLAUDE_CODE_SESSION_ID` from
the environment on every publish — the same way it captures `HERDR_PANE_ID`. The agent never
types it, so it cannot drift or be forgotten. A peer with no session id (a non-Claude-Code agent)
is simply skipped with a note.

**Who runs it.** The sentinel, first thing on every wake (`agentbus refresh --quiet` from cron).
Anyone can run it; it is idempotent and never fails hard on a missing source.

**Non-Claude providers.** `agentbus budget <provider> '<json>'` publishes a window for any other
provider (`openai`, `moonshot`, …) — the schema is already per-provider, so adding one needs no
change here.

**What it deliberately does not report.** No context *percentage* (needs a per-model window table
that rots every release) and no cost (needs a price table that rots the same way, and on a
subscription a dollar figure is fiction). Raw context tokens are what we actually know.

---

## 3. How the loop actually works (the part agents get wrong)

### Your status/report IS your heartbeat
Agents are **one-shot CLI calls**, not daemons. There is no separate heartbeat.
busmon ages you to **idle** after 2 min and **offline** after 10 min from your
last `status`/`report` entry. So emit `status` whenever your state changes and a
`report` at milestones — that's what keeps you "alive" on the dashboard.

### Piloted vs autonomous — check before acting
```bash
agentbus pilot status
```
- **`piloted by hermes`** → wait for a directive; don't act on your own. Arm
  `agentbus subscribe <self>` to receive it.
- **`autonomous`** → proceed on your own plan; just keep emitting `status`/`report`.

hermes holds the lease (`pilot claim`, default 90s TTL) only while it has budget.
When the lease expires (hermes silent / out of budget / crashed) the mode flips
to autonomous automatically — there is no "I'm done" message.

### `subscribe` is wake-on-exit, not a long loop
`agentbus subscribe <self>` **blocks until one command addressed to you arrives,
emits ONE JSON object, then exits.** Arm it as a Claude Code background task; its
exit wakes your session, and you re-arm. After the idle window (default 240s, or
`[idle_secs]`) it emits a heartbeat object and exits so you can re-arm.

Each fire is exactly one JSON line — parse it once. **Re-arm iff `rearm` is `true`:**

| You see                                                              | Meaning            | Exit | Re-arm? |
|----------------------------------------------------------------------|--------------------|------|---------|
| `{"v":1,"event":"cmd","rearm":true,"id":"…","type":"…","from":"…","target":"…","ref":"…","body":"…"}` | a command arrived  | 0 **or 75** | yes |
| …plus `"delivery"`, `"attempt"`, `"duplicate_possible"`, `"text_complete"` | transport + text facts, present on **any** cmd | — | — |
| …plus `"task"` | the ONLY field that marks a tracked request (`expires_at` is omitted when zero, so its absence proves nothing) | — | — |
| `{"v":1,"event":"heartbeat","rearm":true}`                           | idle window passed | 64   | yes     |
| `{"v":1,"event":"error","rearm":true,"msg":"…"}`                     | glitch, **or** an entry with no executable payload (missing / expired body). The missing/expired form carries the entry `id` and no body you may act on — **do not execute its text**; a plain glitch may carry no id at all | 75 | yes |
| `{"v":1,"event":"fatal","rearm":false,"msg":"…"}`                    | misconfigured      | 1    | **no**  |

**A `cmd` event can still exit 75.** The JSON is written before the entry is acknowledged,
so a failed ACK after a complete write exits 75 with the object already on your stdout.
Treat what you read, not the exit code, as the work; the non-zero status means the delivery
may be repeated.

The added fields are additive within `v:1`, and each says less than it looks like:

- **`delivery`** is the transport disposition *at the moment of emission* — for an ordinary
  delivery that is `uncertain`. The values `queued` and `output_written` are **board**
  observations, read with `agentbus request`; they do not appear on stdout. `queued` means no
  delivery attempt was *recorded*, not that none happened. None of them is ever a receipt or
  an acceptance.
- **`attempt`** counts delivery attempts, never executions. **`duplicate_possible:true`**
  means this same entry may have reached you before — deduplicate on project + message `id`.
  An **absent** flag guarantees nothing: a producer that retried on its own can still have
  created a second entry. When the event carries `task`, check `agentbus request` before
  redoing the work; for an ordinary cmd there is no board record to consult.
- **`task`** is the only field that attaches an entry to a tracked request.
- **`expires_at`** is omitted when zero, so an absent field means no deadline was expressed.
- **`text_complete`** is `yes` (whole), `no` (cut at publish), or **absent** — and absent
  means *unknown*, not complete.

Every fire leads with `"v":1` — the bus protocol version (`agentbus version`). Parse
by key and **ignore fields you don't recognize**; a higher `"v"` than you know means
the format changed — stop and re-check rather than mis-parsing.

**Persist the `id`** from each `cmd` fire and pass it back as `--since <id>` next
time you arm — that is your cursor.

**A floor discards; it does not postpone.** With no `--since`, subscribe starts at the
broker's "now", and every entry addressed to you **at or below** that id — including entries
recovered from the pending list — is **acknowledged without being delivered**. It is gone
from your group, not waiting for later. So "no `--since`" is not "skip the backlog for now",
it is "consume the backlog unseen".

- Keep your cursor when a fire carries no id (`heartbeat`, `error`): re-arm with the same
  `--since` you had, or you move your floor forward for nothing.
- `--since 0` lifts the floor for what is still deliverable. On an **existing** group it does
  **not** resurrect entries already acknowledged — those are gone whatever you pass; on a
  **new** group it starts from the retained history.
- Persisting the cursor is what stops you eating your own backlog. It is not a promise that
  nothing is lost: the stream is capped, so a body can age out of it, and an entry is
  acknowledged before you act on it, so a crash can lose the execution. What survives both is
  the **board record** — there is no garbage collection, so a tracked request stays on the
  board even when its body no longer reads back.

Moving a cursor forward is a transport decision and **never means the work was accepted**.
Only `request accept` records that.

**While armed and waiting you are `idle`, never `blocked`** — the agent *status*
`blocked` is reserved for an open 4-eyes gate. A tracked request's `blocked` **state** is a
different thing entirely: it is you telling the board, with a reason, that this one task
cannot move. You can be `blocked` on a request while your own status is `working`. busmon shows a `👂` badge next to armed agents.
**Do not** wrap `subscribe` in a `while` loop or a daemon — a long-lived loop
never wakes a terminal session. (The one exception is `--loop`, for **headless**
consumers like hermes; it emits one `cmd` object per entry, with no `rearm`.) The
whole loop lives in the binary; there is no wrapper script and no watcher daemon.

### The 4-eyes gate blocks regardless of pilot mode
A `challenge` opens a gate on the target that **blocks it until a `verdict`**, in
both piloted and autonomous mode (it's a safety barrier, independent of who's
driving). The typical flow across three agents:

```bash
# reviewer opens the gate (capture the printed ref!)
agentbus challenge claude2 confirm prod migration is reversible
#   → challenge k3f9q opened on claude2

# claude2 sees it gating itself and answers
agentbus gate claude2                       # exit != 0, lists: k3f9q  reviewer|confirm prod migration...
agentbus reply --ref k3f9q claude2 rollback script tested, snapshot taken

# a SECOND reviewer (4 eyes) resolves it
agentbus verdict --pr 25 --ref k3f9q claude2 approve verified
```
`verdict` records to the `{p}:verdicts` ledger unconditionally; if `--ref` names no open gate it prints a `notice:` to stderr and still succeeds (best-effort resolution — no longer fatal). Query state with `agentbus verdicts --pr 25`.

---

## 4. busmon (the human TUI)

`busmon --project <p>` shows three panes:

- **AGENTS** — presence chips (color by state). Badges: `👂` = armed and listening;
  `⌛N` = N commands queued unread; `🔒N` = open 4-eyes challenges; `⬢` = this agent
  holds the pilot lease (master); `⧉` = attached to a herdr pane (`HERDR_PANE_ID`).
  The pilot/master indicator is in the **top status bar** (`⬢ MASTER <driver>` /
  `autonomous (no master)`), not in the AGENTS pane title.
- **ACTIVITY** — live feed of status/report/notify/cmd (history backfilled on start).
  - `Tab` focuses the feed; `↑`/`↓` or `j`/`k` select a line, `g`/`Home` jumps to
    the oldest, `G`/`End` to the newest.
  - `y` or `Enter` copies the selected line to the **clipboard** (OSC52 — works
    even over an SSH tunnel). `Esc` clears the selection and returns to live tail.
  - Mouse wheel scrolls; the title shows `[live]` or a pause indicator.
- **INPUT** — type a message, `Enter` publishes it on `{project}:notify`; type `@` for agent
  autocomplete and an `@<agent> <text>` line sends a **directed cmd** to that agent.
  `Esc`/`Ctrl-C` (or `q` while the feed is focused) quits.

---

## 5. One-line mental model

> Every stream is `{project}:{kind}`. You publish your `status`/`report`, you
> read commands with `subscribe`, you gate risky actions with
> `challenge`/`verdict`, and a human watches it all in `busmon`. Flags are
> `--double-dash value`. That's the whole bus.
