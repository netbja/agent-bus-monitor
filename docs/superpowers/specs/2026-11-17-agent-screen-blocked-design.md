# Agent screen, blocked agents, holders in Services

Status: spec, r3, for review (subject `agent-screen-blocked-spec`). r3: foureyes (§3 states
what busmon shows today, a prompt reads `waiting`; one source and one word for a prompt;
`holder-behind` in busmon's maps; uptime not from `Describe`; `stop holder` vs `restart holder`; screen text stays out of Redis, so the lines
are read live by busmon and held in memory; r2's `{p}:screens` is withdrawn). r2: reviewer-codex (the
screen lines as a snapshot kept current, not carried by the events; stuck apart from blocked,
with its own events, episode and ring). Not built. Base: origin/dev
`15e50f7e`. Author: coder, task `agent-screen-blocked`, from master's directive of 2026-11-17
relaying what the person approved. The person's decisions are §6; the build is §7's steps, one
PR each from origin/dev.

## 1. The holder the dispatcher cannot reach (found first)

**What the person saw.** Services says `dispatch waiting-holder since 00:18, dial unix
/run/user/1000/agentbus/agent-bus-monitor/h-16d10b5c0b43ca02.sock: no such file`, while two
holder services run (builds 886 and 888).

**Why** (read on the machine on 2026-11-17):

- The dispatch service runs the installed `~/.local/bin/agentbus`, v0.29.0, restarted at
  00:18. The two holders are builds 886 (hosts no agent) and 888 (hosts every agent). No holder
  of v0.29.0 is installed: `~/.local/share/agentbus/holder/` has no v0.29.0 directory.
- `HostPing` (`cmd/agentbus/servicecmd.go`) and `holder.Host.Start` dial **the dispatcher's own
  build's** socket (`h.key()`), `h-16d10b5c…` for v0.29.0. Nothing listens there.
- `List`, `Screen`, `Submit` route by the terminal's own build key, so agents on 888 are still
  read and woken: the dispatch log shows wakes after 00:18. The state therefore says more than
  is true ("no agent is woken").
- What is truly broken is every **start** on the current build: a `pop`, the self-repair's
  relaunch, a `reload`. Master's `pop architect` of today (`1794950103908-0`, host-accepted) left
  architect with `no terminal`.

**The fix** (step 1):

- `HostPing` asks every holder of the project (`Describe`), not only its own build's.
- The state says what is true:
  - its own build's holder answers: `ready`, as today;
  - it does not, but another build's holder answers: a new state `holder-behind`, detail
    `no holder of this build (v0.29.0) runs: agents on build 888 are still woken; a pop, a
    relaunch or a reload fails until agentbus up agent-bus-monitor starts it`;
  - no holder answers: `waiting-holder`, as today, with the socket named.
- busmon shows `holder-behind` as attention and running, like `waiting-holder`: it joins
  `serviceRunning` (`cmd/busmon/services.go`) and the role map (`services_cards.go`).
- **The person answered Q1 yes** (task `screen-blocked-q1`, 2026-11-17).
  - The dispatch service, the first time it holds the lease, checks once whether its own build's
    holder answers. If it does not, the service installs and starts it as `up` does
    (`startHolder`: the binary copy, the unit, the start, the wait for its socket, the gc of
    older builds that no longer run).
  - It never stops, restarts or touches an older build's holder or its agents.
  - A standby on another host holds no lease and installs nothing.
  - A failure is logged in `dispatch.log`. The state then says `holder-behind` (step 1).

## 2. `agentbus screen <agent>` (step 2)

- **What it prints.** The agent's terminal's last screen text, plain (no ANSI), read through
  `holder.Host.Screen`, which routes by the terminal's build.
  - `--lines N`: default 40, at most 200.
  - `--json`: `{agent, term, build, rows, lines[], trusted}`.
  - Header line: `<agent> on <term> (build <b>), read <time>`.
- **What it never does.** It never types: it calls no `Submit`, `Prompt`, `Clear` or `Start`. A
  test names every holder request it sends.
- **Who may run it:**
  - Allowed: master and sentinel. The caller is known as `deliver` knows it: the agent the
    registry binds to the caller's `AGENTBUS_TERM_ID`.
  - Refused: any other agent in a terminal, with exit 1: `screen is master's and sentinel's; you
    are <agent>`.
  - Outside any terminal (the person in a shell): allowed, with the person's yes (Q2).
- **When it cannot read:**
  - An agent with no live terminal says so, and exits 1.
  - A screen the holder marks untrusted (G1, `ErrScreenUntrusted`) prints `screen not trusted
    (<reason>)` and no lines.

## 3. Blocked: an agent waiting for an answer in its terminal (step 3)

**What busmon shows today** (r3, foureyes):

- The dispatcher's observe pass judges each terminal (`HostRuntime.Inspect`). It writes the
  judgement to `{p}:observed`: state `blocked` when the client's signature reads a prompt or a
  dialog, plus `Since`, `PID`, `ScreenHash` and `ScreenRepeats`. That hash expires 2 minutes
  after the last write.
- busmon reads it. A fresh observed prompt (`promptWaiting`: not declared `blocked` or
  `waiting`, no open ask) already turns the state column to `waiting`, in group 0, as a
  person's decision. The desktop rings once per episode (prompt-notify).
- The holder's `blocked`/`unblocked` events add the `blocked` flag on the row (herdr exit 4.3a):
  a second source and a second word for the same fact.
- What the person does not get: the words "blocked: waiting for an answer in its terminal", and
  what the screen says. `waiting` reads as the agent's own question (an ask), and the person
  has to attach to the terminal to see the prompt.

**What the person saw is not known.** The directive says "unknown/stuck on a client prompt",
which covers two cases: a prompt the signature recognises, shown today as `waiting`, which reads
as the agent's own question; and a screen it does not recognise, shown today as the agent's last
declaration. This spec handles both: the word for the first, the new `stuck` for the second. Q7
asks master which one it was, for the regression test.

**The rule kept: screen text is never written to Redis.** `bus/observed.go` and
`dispatch/decide.go` say it; its reason
is that a screen can hold anything the client printed (secrets, file contents). Only an opaque
digest (`ScreenHash`) goes to the broker. This spec writes no screen text to Redis; r2's
`{p}:screens` snapshot is withdrawn.

**What changes:**

- **One source and one word for a prompt:** the observation (`{p}:observed`, state `blocked`),
  read as `blocked`.
  - `promptWaiting` becomes `promptBlocked`: same test (a fresh observed prompt, not declared
    `blocked` or `waiting`, no open ask); `stateLook` maps it to `blocked` instead of `waiting`.
  - It keeps group 0 and the attention role: the person must act. The glyph is `GlyphBlocked`.
  - A declared `waiting`, or an open ask, still reads `waiting`: the agent's own question.
  - The row's `blocked` flag from the holder's events is dropped from the row: the state column
    says it. The events stay on the timeline.
  - Nothing is written to `{p}:agents`: it is busmon's reading of `{p}:observed`, as today.
- **The text busmon shows** (as built, step 3a): the For you line says `blocked: waiting for an
  answer in its terminal` (a folder-trust prompt adds what it asks after the words); `enter` opens the agent's page, whose Screen section is headed with
  the same words and shows the screen's last lines. One line in For you has no room for six.
- **The lines are read live from the holder by busmon, and kept in memory only.**
  - As built, they come from the agent page's existing screen read (`readTermView`, the terminal
    preview): every tick while that page is open and on top, the same read `t attaches` uses.
    No other agent's screen is read, and no read happens while the page is closed.
  - It keeps the last 6 non-blank lines (Q4), each through `health.Redact` (a line with a
    credential shape is replaced whole) and cut to the page's width. They are held on the page
    and dropped with it. They are never logged, never written to Redis, and never sent
    in a notification: the desktop ring keeps its fixed words.
  - **When busmon cannot read the holder:** a failed read (an untrusted screen, G1, among
    them) shows `not read: <the error>`, the preview's existing text, instead of lines. busmon on
    another host than the holder finds no holder there, so the page has no Screen section; the
    page's blocked words still say it. `agentbus -p <p> screen <agent>` on the holder's machine
    reads it there.
  - This answers reviewer-codex's r1 point (lines would go stale) without a stored snapshot.
- **Stuck is its own state, not a prompt** (r2, reviewer-codex).
  - **What counts:** an observation in state `unknown` (settled, the client's signature reads
    neither ready, nor busy, nor a prompt) whose `ScreenHash` has not changed, for the same pid,
    for `dispatch.stuck_after` (default 3 min, Q3).
  - **The measure:** a new additive field `ScreenSince` in the observation: the Redis TIME of the
    first reading of this hash by this pid, carried like `Since`. `ScreenRepeats` alone counts
    passes, not time: a pass can be skipped. Within version: additive JSON, an older busmon
    ignores it.
  - **Its episode:** pid and `ScreenSince`. A new hash, a recognised state or a prompt ends it;
    a prompt reads `blocked` instead.
  - **What busmon shows:** the state column reads `stuck` (attention, group 0). The text is
    `stuck: its screen is not recognised and has not changed for <d>`, then the lines, read as
    above.
  - **Its ring:** once per episode, under its own mark `stuck:<agent>` in `{p}:quota`, through
    prompt-notify's runner, with its own words: `<project>: <agent>'s screen has not changed for
    <d> and is not a prompt agentbus knows (<term>)`. It never says "waits on a prompt". Whether
    it rings at all is Q6.
- **Suppressions stay prompt-notify's**, for both `blocked` and `stuck`: the agent declared
  `blocked` or `waiting`, an open ask, the project paused, the agent swapping.

## 4. Holders in busmon's Services (step 4)

- Services lists, besides dispatch and missions, **every holder unit of the project**, one row
  each. The holders come from the service manager (`HolderUnits`) and from `Describe` for those
  that answer.
- **Columns:**
  - state (active, inactive, failed, `not answering` when the unit runs but its socket does not
    answer);
  - build;
  - uptime: the unit's `ActiveEnterTimestamp` (launchd and the detached supervisor: the
    process's start from `ps`). `Describe`'s `Start` is clock ticks since boot, an identity
    check, not a wall time: it is not used;
  - agents hosted (live terminals).
- **Flags:**
  - `old build, hosts no agent`: a holder of a build other than the dispatcher's, with no live
    terminal. No command stops one holder today:
    `down` stops them all, and `service gc` removes only holders that no longer run. Step 4 adds
    `agentbus -p <p> service stop holder <build>`, refused while that holder has a live terminal
    and for the current build. It differs from `service restart holder --kill-agents`, which kills
    the current build's agents: `stop holder <build>` never kills an agent, it refuses. The
    flag's detail names it, then `service gc`. See Q5.
  - `current build missing`: no holder of the dispatcher's build. This is §1's state, shown on
    the holder block too.
- busmon only reads: it never stops a holder.

## 5. Tests first, per step

1. **Step 1:**
   - a dispatcher of build B with only a build-A holder answering publishes `holder-behind`, and
     still wakes an A-hosted agent;
   - with no holder answering, it publishes `waiting-holder`;
   - with its own build's holder answering, it publishes `ready`.
2. **Step 2:**
   - master reads; coder is refused; a missing terminal exits 1; an untrusted screen prints no
     lines;
   - a fake host records the requests, and only List/Screen/Describe appear.
3. **Step 3:**
   - an observed prompt reads `blocked` from `{p}:observed`, not `waiting`; a declared
     `waiting` or an open ask still reads `waiting`; the row has no second `blocked` flag;
   - stuck reads after `stuck_after`, not before; a new hash or pid ends it; a prompt turns it to
     `blocked`; its ring never says prompt;
   - busmon reads `Screen` only for blocked or stuck agents; the lines are capped and redacted
     and dropped when the state ends;
   - no write of screen text to Redis: a test reads every key the dispatcher and busmon write
     during a blocked episode and finds none of the screen's words;
   - the unreadable cases (another host, untrusted) show their text;
   - the suppressions hold.
4. **Step 4:**
   - the rows from fake units plus Describe;
   - each flag;
   - a unit that runs but does not answer.

## 6. Questions for the person

- **Q1.** May the dispatch service, at start, install and start the holder of its own build when
  none runs? Today only `agentbus up` installs it (`service start holder` refuses a build with
  no unit). A new holder starts beside the old ones without touching their agents; `holder
  drain` later moves the agents.
  - Recommended: **yes**. Otherwise every update leaves pop, relaunch and reload broken until
    someone runs `up`.
  - No: §1's message only.
  - **Answer: yes** (the person, 2026-11-17).
- **Q2.** May the person, in a shell outside any terminal, run `agentbus screen`? Recommended:
  yes. The person can already attach to the terminal.
- **Q3.** Stuck after how long unchanged and unrecognised? Recommended: 3 minutes, a setting.
- **Q4.** How many screen lines in busmon? Recommended: the last 6 non-blank.
- **Q5.** An old holder that hosts nothing: should busmon only flag it and name the new
  `service stop holder <build>` (refused while it hosts a terminal), or should the dispatcher
  stop it by itself after a while? Recommended: flag it and name the command. Stopping a holder
  is the person's call.

- **Q6.** Should a stuck screen ring the desktop, with its own words (§3), or only show in
  busmon? Recommended: ring once per episode. A screen nobody recognises for 3 minutes is
  usually something only the person can answer.

- **Q7** (for master). What did the person see: a recognised client prompt, which busmon showed
  as `waiting`, or a screen agentbus does not recognise? Step 3's regression test reproduces
  that case.

## 7. Steps

One PR each, from origin/dev, base dev, in this order:

1. The holder state (§1). With Q1's answer, the start of the missing holder.
2. `agentbus screen` (§2).
3. Blocked and stuck (§3), in two PRs: 3a, blocked in the state column with the screen's last
   lines on the agent's page; 3b, stuck (`ScreenSince`, `dispatch.stuck_after`, its ring).
4. Holders in Services (§4).
