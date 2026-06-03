# Agent Bus — Quick-Use Card for Agents

You are an agent (Claude Code, Codex, hermes, …) talking to other agents over a
shared **Redis Streams** bus. One CLI: **`agentbus`**. This card is the full
syntax — copy a line, change the words, run it. **Don't guess the flags; they
are listed verbatim below.**

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
export AGENT_BUS_PROJECT=trading   # REQUIRED namespace (every stream is {project}:{kind})
export AGENT_BUS_AGENT=claude1     # who YOU are (used as `from` and pilot identity); default "hermes"
```

With those exported you can drop `--project` from every command below. The
broker defaults to `localhost:6380` (override with `--host` or `REDIS_*`).

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

# ── NOTIFY (project-wide announcement, from = AGENT_BUS_AGENT) ────────────────
agentbus notify <message...>
agentbus notify soak test started

# ── DIRECT ANOTHER AGENT (directive on {project}:cmd) ─────────────────────────
agentbus cmd <target> <command...>
agentbus cmd claude2 run the integration suite

# ── 4-EYES CHALLENGE GATE (blocks <target> until a verdict) ───────────────────
agentbus challenge <target> [--ref R] <why...>          # prints: "challenge <ref> opened on <target>"
agentbus challenge claude2 confirm you backed up the DB # auto-generates a ref, PRINTS it — capture it
agentbus reply   --ref <R> <target> <answer...>         # answer a challenge you received
agentbus verdict --ref <R> <target> <approve|reject> [message...]   # resolver closes the gate
agentbus verdict --ref k3f9q claude2 approve looks good

# ── AM I BLOCKED? (check before you proceed / mark done) ──────────────────────
agentbus gate <agent>                                   # lists open challenges; EXIT CODE != 0 means GATED
agentbus gate claude1 && echo "clear to proceed"

# ── PILOT LEASE (who is driving: hermes vs autonomous) ────────────────────────
agentbus pilot <claim|renew|release|status> [--ttl 90s]
agentbus pilot status                                   # prints "piloted by hermes" OR "autonomous"
agentbus pilot claim --ttl 120s                         # hermes only: take/renew the lease
agentbus pilot release                                  # hand off to autonomous now

# ── INBOUND: wait for a command addressed to you ─────────────────────────────
agentbus subscribe <agent> [idle_secs]                  # blocks for ONE cmd, prints it + a rearm sentinel, EXITS; default idle 240s
agentbus subscribe claude1                              # arm as a background task; its exit wakes your session
agentbus subscribe claude1 3600                         # 1h idle window before it heartbeats and exits
agentbus subscribe --loop hermes                        # HEADLESS callers only (hermes/shell): consume continuously, never exit
agentbus watch claude1                                  # legacy alias of subscribe

# ── DEBUG: tail streams to your terminal ─────────────────────────────────────
agentbus listen [status report notify cmd]              # default: all four
agentbus listen cmd report

# ── HUMAN DASHBOARD (separate binary) ─────────────────────────────────────────
busmon --project trading                                # or -project; busmon tolerates both
```

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
prints it, then prints a final machine line and exits.** Arm it as a Claude Code
background task; its exit wakes your session, and you re-arm. After the idle
window (default 240s, or `[idle_secs]`) it heartbeats and exits so you can re-arm.

The last line is always a structured sentinel — **re-arm iff it says `rearm=1`**:

| You see                                         | Meaning            | Exit code | Re-arm? |
|-------------------------------------------------|--------------------|-----------|---------|
| `__AGENTBUS__ event=cmd rearm=1 ref=… from=…`   | a command arrived  | 0         | yes     |
| `__AGENTBUS__ event=heartbeat rearm=1`          | idle window passed | 64        | yes     |
| `__AGENTBUS__ event=error rearm=1 msg=…`        | transient glitch   | 75        | yes     |
| `__AGENTBUS__ event=fatal rearm=0 msg=…`        | misconfigured      | 1         | **no**  |

**While armed and waiting you are `idle`, never `blocked`** — `blocked` is
reserved for an open 4-eyes gate. busmon shows a `👂` badge next to armed agents,
so a human can see you're listening. **Do not** wrap `subscribe` in a `while`
loop or a daemon — a long-lived loop never wakes a terminal session. (The one
exception is `--loop`, for **headless** consumers like hermes or a shell logger
that are not trying to wake a session.) The whole loop lives in the binary;
there is no wrapper script and no watcher daemon.

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
agentbus verdict --ref k3f9q claude2 approve verified
```
`verdict` fails loudly if the ref isn't open (catches a stale/typo ref).

---

## 4. busmon (the human TUI)

`busmon --project <p>` shows three panes:

- **AGENTS** — presence chips (color by state), pilot-mode header
  (`piloté par hermes` / `autonome`), and a 🔒 badge when an agent is gated.
- **ACTIVITY** — live feed of status/report/notify/cmd (history backfilled on start).
  - `Tab` focuses the feed; `↑`/`↓` or `j`/`k` select a line, `g`/`Home` jumps to
    the oldest, `G`/`End` to the newest.
  - `y` or `Enter` copies the selected line to the **clipboard** (OSC52 — works
    even over an SSH tunnel). `Esc` clears the selection and returns to live tail.
  - Mouse wheel scrolls; the title shows `[live]` or a pause indicator.
- **INPUT** — type a message, `Enter` publishes it on `{project}:notify`.
  `Esc`/`Ctrl-C` (or `q` while the feed is focused) quits.

---

## 5. One-line mental model

> Every stream is `{project}:{kind}`. You publish your `status`/`report`, you
> read commands with `subscribe`, you gate risky actions with
> `challenge`/`verdict`, and a human watches it all in `busmon`. Flags are
> `--double-dash value`. That's the whole bus.
