# Agent screen, blocked agents, holders in Services

Status: spec, r2, for review (subject `agent-screen-blocked-spec`). r2: reviewer-codex (the
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
- busmon shows `holder-behind` as attention, like `waiting-holder`.
- Whether the dispatcher may install and start its build's holder itself is the person's
  question Q1. Without a yes, it only says it.

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

**What exists already:**

- The dispatcher's observe pass judges a terminal `blocked` when its client's signature reads a
  prompt or a dialog (`HostRuntime.Inspect`). It writes `blocked`/`unblocked` events.
- busmon's agent row shows a `blocked` flag from those events; the desktop rings once per
  episode (prompt-notify).
- The state column still shows the agent's own last declaration (`idle`, `working`). That is why
  the person cannot see it at a glance.

**What changes:**

- **The state column reads `blocked`** while the dispatcher's latest event for that agent's
  current terminal is `blocked`. busmon derives this; the agent's declared status in
  `{p}:agents` is not overwritten (the putSnapshot rule: an observation never replaces the
  agent's word). Its declared state stays in the detail.
- **Stuck is its own state, not a prompt** (r2, reviewer-codex).
  - **What counts:** a settled screen that the client's signature reads as neither ready, nor
    busy, nor a prompt (`Inspect` leaves it `unknown`), unchanged (same `ScreenHash`) for
    `dispatch.stuck_after` (default 3 min, Q3).
  - **Its own events:** `stuck` and `unstuck` (new event kinds in `bus/events.go`).
  - **Its own episode:** the same pid-and-since rule as prompt-notify, under its own mark
    `stuck:<agent>` in `{p}:quota`. A change of the hash, a recognised state or a prompt ends
    it. A prompt that appears opens a `blocked` episode instead.
  - **What busmon shows:** the state column reads `stuck`, and the text is `stuck: its screen is
    not recognised and has not changed for <d>`.
  - **Its ring:** once per episode, through the same runner, with its own words: `<project>:
    <agent>'s screen has not changed for <d> and is not a prompt agentbus knows (<term>)`. It
    never says "waits on a prompt". Whether it rings at all is Q6.
- **The text busmon shows for `blocked`**, in the agent's detail (`enter` on the row) and in the
  For you line: `blocked: waiting for an answer in its terminal`, then the screen's last lines.
- **The lines are a snapshot the dispatcher keeps current** (r2, reviewer-codex: the
  blocked/unblocked events are written only when an episode opens or closes, so lines carried by
  them would go stale).
  - **Where:** the hash `{p}:screens`, one field per agent. Its value is JSON: `{term, pid,
    episode, kind (blocked|stuck), at (Redis TIME), lines[]}`.
  - **When it is written:** a fenced write at each observe pass (15 s) of an open episode, and
    only when the `ScreenHash` changed since the last write. It costs no holder read: the
    dispatcher already holds the screen it judged.
  - **What the lines are:** the last 6 non-blank lines (Q4), each cut to 200 runes, through the
    same secret redaction as reports before the write.
  - **When it is removed:** by a fenced write when the episode ends
    (`unblocked`/`unstuck`); when the agent leaves the observation; and when a different term or
    pid is observed. A dispatcher that dies mid-episode leaves its last snapshot, so busmon also
    bounds it: it shows a snapshot only while busmon's own episode for that agent (term, pid,
    episode) is open and `at` is under 2 minutes old (8 passes). Otherwise it shows `screen not
    read since <at>`. The next dispatcher with the lease overwrites or removes it.
  - **Retention:** at most one snapshot per observed agent; nothing archived. `agents forget`
    removes the agent's field (step 3 adds it there); `projects delete` removes the key with the
    project's other `{p}:` keys.
  - busmon reads `{p}:screens` with the agents and never reads the holder for it.
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
  - uptime (the unit's `ActiveEnterTimestamp`, or the holder's start time from `Describe`);
  - agents hosted (live terminals).
- **Flags:**
  - `old build, hosts no agent`: a holder of a build other than the dispatcher's, with no live
    terminal. No command stops one holder today:
    `down` stops them all, and `service gc` removes only holders that no longer run. Step 4 adds
    `agentbus -p <p> service stop holder <build>`, refused while that holder has a live terminal
    and for the current build. The flag's detail names it, then `service gc`. See Q5.
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
   - the state column reads `blocked` from the events, and the declaration is kept;
   - a stuck screen emits after `stuck_after`, not before; a changed hash ends the episode;
   - stuck emits `stuck`, never `blocked`, and its ring never says prompt; a prompt that appears
     ends it and opens `blocked`;
   - the snapshot is rewritten when the hash changes, not when it does not; it is removed at the
     episode's end and on a new pid; busmon hides one older than 2 minutes or from another
     episode;
   - the lines are capped and redacted;
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

## 7. Steps

One PR each, from origin/dev, base dev, in this order:

1. The holder state (§1). With Q1's answer, the start of the missing holder.
2. `agentbus screen` (§2).
3. Blocked and stuck in the state column, the screen snapshot (§3).
4. Holders in Services (§4).
