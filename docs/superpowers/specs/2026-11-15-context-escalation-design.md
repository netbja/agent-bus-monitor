# The context alert gets teeth (context-escalation)

Status: spec, r4 (r3 foureyes: the `below-compaction` notice gets a writer, the dispatcher; the
ceiling is compared in tokens, never a share of one window against a share of another, §4.3).
r3 was Task `context-escalation-r3` (master's directive of 2026-11-15, after the person's
review of r2, human directive 1794777873906-0). r2 (d9610408, PR 540) was approved by both
reviewers; r3 answers the person's review:

- §1, §3.4: the premise "no hook delivers a message inside a turn" was wrong for three clients;
  steps 1 and 2 now reach the agent itself where its client allows it;
- §2: nobody at the desk: the interrupt is not built; auto-compaction ends the episode, and master
  is told (§3.5);
- §4: the ladder stays below the client's own compaction, measured rather than assumed;
- §7: the 2026-11-15 replay and a 200k-window agent are tests.

r1 to r2 (kept): "a hand-off stands" is the existing `VoidFor` rule; the second threshold applies
only above the effective alert.

**Scope.** The ladder acts on agents with a published context fill, which today means Claude Code
agents only (the fill comes from Claude Code's statusLine, decision 2026-10-27). The per-client rows
below say what applies once another client publishes a fill; nothing here invents one.

## 1. What happened on 2026-11-15

From `agentbus history -a coder`:

| Time | Fact |
|---|---|
| 21:30 | the dispatcher sends coder `context 80%: at your next stopping point commit WIP, publish your hand-off …` and master a `wake=no` notice |
| 21:30 to 21:57 | coder keeps working; its fill goes past 90%. Nothing else happens |
| 21:57 | master, noticing by itself, sends coder a second directive |

Why nothing else happened, read from the code on `origin/main` (289ce283):

1. **The alert waits in the agent's stream until its turn ends.** The dispatcher wakes idle agents
   only, and agent-bus installs no hook that delivers anything inside a turn. **The clients do have
   such hooks** (§3.4): Claude Code and Codex after each tool call, agy before each model call.
   Kimi and Grok have none that reaches the model. So on 2026-11-15 the alert could have reached
   coder mid-turn; agent-bus simply did not use the channel.
2. **The alert is one act, at one threshold.** `dispatch/contextalert.go` sends the directive and
   the notice once per episode (the session), then nothing, whatever the fill or the time.
3. **Nobody is told in a way that wakes or rings.** The notice to master is `wake=no`. The health
   check `context` (`health/health.go:108`) is a warn with `Handler: "dispatch"`, no `Person`, no
   `Reasons`: busmon shows it "in hand", so For you and the notification relay never carry it.
4. Sentinel's daily review saw a warn "in hand" and said nothing: correct by its rules.

## 2. The ladder

Three steps per episode, each once. The episode is unchanged: the session proved with the agent's
process; a fresh known fill below `dispatch.context_alert` ends it and deletes its marks.

| Step | When | What | Who sees it |
|---|---|---|---|
| 1. alert | known fill ≥ `context_alert` (80) | directive to the agent; **the same instruction delivered mid-turn where the client allows (§3.4)**; `wake=no` notice to the escalation target; desktop notification | the agent at its next tool call (Claude Code), else at its turn's end; master on its next wake |
| 2. escalation | the episode has alerted, no hand-off stands, **and** either known fill ≥ `context_urgent` (90) **or** the alert is older than `context_handoff_deadline` (20 min) | the firmer instruction mid-turn (§3.4); one directive to the escalation target **that wakes it**; one notification for the person (`FencedNotifyOnceForPerson`); the desktop notification; the `context` check goes **For you** | the agent; master; the person on ntfy/Discord and in busmon |
| 3. hand-off and clear (today) | the agent declared `agentbus handoff` | the dispatcher's existing `/clear` after a declared hand-off | nobody: the normal end |

**A hand-off stands** exactly as for the dispatcher's clear: the agent's record in `{p}:handoff`
exists and `Handoff.VoidFor(snapshot)` is false (same verified pid and start, same proved session,
no `working` declared since). An agent that hands off and keeps working has a void record, so it
can still escalate, and the deadline counts from the alert.

Step 2 is one act, triggered by whichever comes first. A hand-off declared before it cancels it;
declared after, it only lifts the check (§5). Nothing repeats in the same episode.

**Nobody at the desk (the person's question).** No interrupt step is built. With the mid-turn
channel, steps 1 and 2 reach a Claude Code agent between two tool calls with nobody at the desk,
which covers the 2026-11-15 case. What remains is an agent inside one very long tool call, and,
once they publish a fill, Kimi and Grok agents. For them, at night, **the client's
auto-compaction ends the episode**, and master is told that the agent lost its context without a
hand-off (§3.5). An interrupt (Esc, then a fixed line typed into the pane) would be a new exception
to "the dispatcher types only the wake line and `/clear`" (decision 2026-10-10, amended
2026-10-28). It stays a question for the person (§9, Q4), with a recommendation against it.

## 3. The dispatcher (`dispatch/contextalert.go`)

### 3.1 The alert keeps its time

The alert's directive is sent with `FencedCmdOnceAt` instead of `FencedCmdOnce`, so
`context:<agent>:at` holds the entry's id (Redis time). The deadline is counted from it
(`bus.MarkAt`). An episode alerted by an older build has no `:at`: only `context_urgent` can
escalate it. Its end deletes `context:<agent>:at` with the other marks.

### 3.2 The escalation

In `contextAlerts`, for an agent whose fill is known and at or above `context_alert`, after
`alertContext`:

```
escalate when
  marks[context:<a>] == f.Session                    // this episode alerted
  and no hand-off stands for <a>                     // bus.Handoffs (once per pass) + VoidFor(snaps[a])
  and ( (urgent(a) > 0 and f.Pct >= urgent(a))       // urgent(a): §4.3, per agent
        or (deadline > 0 and MarkAt(marks, context:<a>) > 0
            and now - MarkAt(...) >= deadline) )      // now = the publication's At (Redis TIME)
```

The act, each entry with its own mark in one fenced script, episode = `f.Session`:

1. `context-nudge:<agent>`: step 2 in `{p}:nudge` (§3.4).
2. `context-escalate:<agent>`: `FencedCmdOnce` to the escalation target, **wake=yes**: `context:
   <agent> at <pct>%, alerted <n> min ago at <alert>%, no hand-off declared. <told mid-turn at
   21:31 | its client has no mid-turn channel: the person is told>. Send it no new work; do not
   type into its pane. Once it declares its hand-off the dispatcher clears it.` Skipped when the
   agent is the escalation target itself.
3. `context-person:<agent>`: `FencedNotifyOnceForPerson`: `<agent> context <pct>%, no hand-off
   after <n> min<, told mid-turn>: if you are at the desk, interrupt it (Esc) and tell it to hand
   off now.`
4. When (3) was sent: `Context.Notify` (desktop).

`ErrSwapInProgress` skips the agent; `ErrNotHolder` ends the pass. An unknown or aged fill never
escalates, never ends an episode. A paused project is not judged.

### 3.3 The end of the episode

On a fresh known fill below the alert, the marks deleted are `context:<a>`, `context:<a>:at`,
`context-notice:<a>`, `context-escalate:<a>`, `context-person:<a>`, `context-nudge:<a>`, and the
agent's field in `{p}:nudge`. The mark names are functions in `bus/context.go`.

### 3.4 Mid-turn delivery: `{p}:nudge` and the tool hook

**Per client**, verified in each client's hook documentation on 2026-11-15:

| Client | Event | Output that reaches the model | Channel |
|---|---|---|---|
| Claude Code (2.1.325) | `PostToolUse`, after each tool call | `hookSpecificOutput.additionalContext`: "wraps the string in a system reminder … Claude reads the reminder on the next model request" | **yes** |
| Codex (0.169) | `PostToolUse`, after Bash, `apply_patch`, MCP and local tools | `hookSpecificOutput.additionalContext`, "added as extra developer context" | **yes** (applies once Codex publishes a fill) |
| agy | `PreInvocation`, before each model call | `injectSteps` with a `systemMessage` | **yes** (idem) |
| Kimi | `PostToolUse` | observation-only: "the main flow is unaffected regardless of what the script returns" | no |
| Grok | `PostToolUse` | "for passive events, stdout is ignored" | no |

No client fires a hook *during* one tool call: a single long call is reached only when it ends.

**The record.** `{p}:nudge`, a hash, field = agent, value `{session, step, pct, alert, at}`
(`step` 1 or 2, numbers only, `at` Redis TIME), written by the dispatcher in the same fenced script
as the step's other entries. It never carries text: the hook renders a fixed sentence from it, so
no bus content (a message body, a report) ever reaches a model through this channel.

**The hook**, `agentbus roles turn tool`, installed for Claude Code as a `PostToolUse` hook next to
the existing turn hooks (`rolecfg`; Codex and agy rows when their fill exists). On each call:

1. Skip at once, printing nothing, when `AGENT_BUS_AGENT` or `AGENT_BUS_PROJECT` is missing, or the
   event is a subagent's (`agent_id` present, as `turnHook` reads it).
2. One script, within a 300 ms budget (it sits on every tool call): read the agent's `{p}:nudge`
   field; if its `session` equals the hook payload's `session_id` and its `step` is above the
   field `delivered` in the same value, set `delivered = step`, `delivered_at` = Redis TIME, and
   return it. This write is the agent's own, like its status; it is not a dispatcher write.
3. Print the client's JSON with the fixed sentence:
   - step 1: `agentbus: context <pct>% (alert at <alert>%). At your next stopping point commit WIP,
     publish your hand-off with agentbus report <agent> --to <target>, then declare it: agentbus
     handoff <agent> --report <its id>.`
   - step 2: `agentbus: context <pct>%, no hand-off yet. Stop starting new work now: commit WIP,
     publish your hand-off and declare it (agentbus handoff) before anything else.`
4. Any error (broker down, timeout, bad value): print nothing, exit 0. The hook never blocks a tool
   (no `decision`, never exit 2).

Each step is delivered once per session; the directive in the stream stays (it is what the agent
reads if the hook did not fire). `delivered_at` is what §3.2's master directive and the check
(§5) show as "told mid-turn at 21:31".

### 3.5 An episode ended by auto-compaction

When an episode ends (§3.3) **in the same session**, with no hand-off standing, the fill fell
because the client compacted (a `/clear` or a relaunch changes the session). The dispatcher sends
the escalation target one `wake=no` notice, mark `context-compacted:<a>` = `<session>|<alert at>`
(once per episode; not deleted with the others): `context: <agent> was compacted by its client at
about <last pct>% without a hand-off; it keeps working with a summary of its context. Check its
current step before sending it more.` When the transcript shows a `compact_boundary` with
`trigger: auto` after the alert (§4.2), the notice names its `preTokens`.

## 4. Below the client's own compaction

### 4.1 What Claude Code does (verified on 2.1.325, the installed binary's code)

- The compaction threshold is "effective_window minus the summary buffer, lowered further by
  `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` when set": **the override can only lower it** (#31806).
- It is read from `process.env.CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`: the process environment, which
  is why the `env` block of `settings.json` was reported ineffective (#63186).
- The window it applies to is set by `autoCompactWindow` (settings, "100000 to 1000000, or `auto`
  for the window tuned for the model"; `/autocompact` saves it) or `CLAUDE_CODE_AUTO_COMPACT_WINDOW`,
  which "takes precedence".
- The statusLine's `used_percentage` is `round(input tokens / context_window_size × 100)`.
- Each compaction is recorded in the transcript: `{"type":"system","subtype":"compact_boundary",
  "compactMetadata":{"trigger":"auto","preTokens":…}}`.

**Observed on this host**: our agents' automatic compactions on 2026-11-14/15 happened at 224k to
521k tokens (one at 967k) in Opus 5.5 and Sonnet 5.5 sessions, while coder's fill read 90% at
21:57. So the effective compaction window is often far below the 1M model window, and whether
`context_window_size` reports that window or the model's is **not established** by reading code.
The person's 200k and 1M figures are therefore cases to test, not facts to build on. The ladder
measures instead (§4.2) and bounds (§4.3).

### 4.2 Measure: the fill carries its window, the dispatcher reads the compaction point

- `quota.ContextReading` (and `bus.ContextFill`) gains `Window`, the statusLine's
  `context_window_size`.
- The dispatcher's transcript reader (the one that reads Claude transcripts for quota) also keeps,
  per session, the last `compact_boundary` with `trigger: auto`: `preTokens` and its time. The
  **observed compaction point** of an agent is that `preTokens`, **in tokens**, for the last
  automatic compaction of a session of that agent on the same `Window` (kept in `{p}:context` as
  `compact_tokens`; absent until one happened).
- A fill in tokens is `Pct × Window / 100`: `used_percentage` is a share of `context_window_size`
  (§4.1), so this conversion is exact.

### 4.3 Bound, compared in tokens

**Two windows, one unit.** A fill is a share of `context_window_size`; `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`
is a share of the compaction window (`autoCompactWindow`, `auto` by default, which the bus does not
know). A percentage of one is never compared with a percentage of the other: every comparison
below is in tokens.

- **The launcher exports**, in each Claude Code agent's process environment (`rolecfg/launch.go`,
  the role environment it already builds): `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=<n>` from
  `dispatch.context_compact_at` (default 92; 0 = not exported), and, only when
  `dispatch.context_compact_window` is set (W tokens; default 0 = the client's own window),
  `CLAUDE_CODE_AUTO_COMPACT_WINDOW=<W>`. The override can only lower the threshold, so with both
  set the client compacts **no later than n% of W tokens**.
- **What the agent runs with is published, not assumed**: the agent's status publish (as
  `claude_config_dir` is, login-expiry) carries the two values read from its own environment,
  `compact_at` and `compact_window`; a setting changed after its launch does not apply to it, and
  the bus knows.
- **The ceiling of an agent, in tokens**, the lowest known of: its observed compaction point
  (§4.2), and `compact_at / 100 × compact_window` when the agent published both. With
  `compact_at` alone (no window), the bound is not converted and does not count. None known: no
  ceiling, the steps are judged by the fill alone.
- **Per agent, at each pass**, a step's threshold in tokens is `t × Window / 100`:
  - `urgent(a)` is `context_urgent` when its tokens are below the ceiling, else 0 (the deadline
    still applies);
  - when the alert's tokens are at or above the ceiling, the agent will compact before any step:
    **`below-compaction`**. The dispatcher writes it (the check only reads, §5): in `contextAlerts`,
    for each agent with a known fill and a known ceiling, it sets the mark `context-below:<agent>`
    = `<Window>|<ceiling tokens>` and sends one `FencedNotifyOnceForPerson` with that mark and
    episode: `<agent> compacts near <240k> tokens, below the alert at <80>% of its <1000k> window
    (<800k>): lower dispatch.context_alert, or set dispatch.context_compact_window`. The mark is
    deleted (fenced) when a fresh reading puts the alert below the ceiling. Nothing changes a
    threshold by itself.
- **At start** only r2's rule stays (`context_alert < context_urgent`, else urgent off, logged
  once). The person's order `alert < urgent < compact_at` is checked per agent in tokens, above,
  because `compact_at` is a share of another window.
- **Per client profile** (`agentstate/clients/*.toml`), a new key `auto_compact`: `env` (the
  client honours a lowering override and a window in its process environment: Claude Code),
  `observed` (its compaction is read, not set), `unknown` (Codex, agy, Kimi, Grok today: no
  ceiling until one is verified).

**Before the build (coder, one check, no pane typed into):** on the installed Claude Code, a
throwaway headless session (`claude -p`, its own `CLAUDE_CONFIG_DIR`) launched with
`CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=50` and `CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000`, fed until it
compacts: its `compact_boundary.preTokens` must be ≤ 100k (50% of W), and its statusLine
`context_window_size` recorded. If the bound does not hold, both settings ship with default 0 and
the ceiling is the observed point only; the build's report says which.

## 5. The check `context` (`health/evaluate.go`, `health/health.go`)

Evidence per agent: `Escalated` (as r2: an escalation mark of the fill's session, no hand-off
standing, by `VoidFor` against `{p}:agents`), `Told` (the nudge's `delivered_at`), `Below` (the
mark `context-below:<agent>` for the fill's `Window`, §4.3). The check reads what the dispatcher
wrote; it never judges thresholds itself and never writes.

- **warn, reason `escalated`**, For you: `escalated: coder 91% (no hand-off 27 min, told mid-turn
  21:31)`;
- **warn, reason `below-compaction`**, For you: the sentence of §4.3's notice;
- warn, no reason, in hand: agents at or above the alert, none escalated (today);
- ok and unknown as today.

`Reasons: {"escalated": "interrupt the agent (Esc) and tell it: hand off now", "below-compaction":
"agentbus config"}`. The relay skips `context`'s For-you event (r2): the
dispatcher's per-agent notices are the only rings (§3.2 for `escalated`, §4.3 for
`below-compaction`). Never a fail.

## 6. Roles

- **The person**: told once per episode at step 2, and once per window for `below-compaction`.
- **Master**, woken by the escalation: sends that agent no new work, keeps its pending items for the
  next session, never types into its pane, does not resend the alert (the dispatcher's wake and the
  hook do). On a compaction notice (§3.5), checks the agent's current step before sending more.
  The `agent-bus-master` skill gains this paragraph.
- **Sentinel**: nothing new; never paged by this check.
- **The dispatcher**: §3; no new pane write. The hook (§3.4) writes nothing into any pane: the
  client itself adds its output to the model's context.

## 7. Settings (`config/keys.go`)

| Name | Env | Kind | Default | Rule |
|---|---|---|---|---|
| `dispatch.context_urgent` | `AGENT_BUS_CONTEXT_URGENT` | Float | 90 | 0 = off; 0 to 100; applied only above the alert and below the ceiling (§4.3) |
| `dispatch.context_handoff_deadline` | `AGENT_BUS_CONTEXT_HANDOFF_DEADLINE` | Duration | 20m | 0 = off; at least 1m |
| `dispatch.context_compact_at` | `AGENT_BUS_CONTEXT_COMPACT_AT` | Float | 92 | 0 = not exported; otherwise 50 to 99; read by the launcher (`agentbus up`, self-repair, relaunch) |
| `dispatch.context_compact_window` | `AGENT_BUS_CONTEXT_COMPACT_WINDOW` | Int (tokens) | 0 | 0 = not exported (the client's own window); otherwise 100000 to 1000000, the client's own range; read by the launcher |

`context_alert = 0` turns the ladder off, as today. A changed `context_compact_at` or
`context_compact_window` applies to agents
launched after the change (the environment of a running process does not change); busmon's C page
says so.

## 8. Tests and footprint

Tests (r2's kept, plus):
- **the 2026-11-15 replay**: alert at 21:30 at 80%, fill +0.37 points a minute (80% to 90% in 27
  min): the deadline escalates at 21:50 at about 87%, before 90%; the nudge's step 1 is delivered at
  the first tool call after 21:30, step 2 at the first after 21:50; one directive each.
- **a 200k-window agent**: `Window` 200000, observed compaction at 166k tokens: urgent 90% is
  180k ≥ 166k, so `urgent(a)` is 0, no step fires at or above 166k, the deadline still escalates;
  the alert (160k) applies.
- **one unit**: `Window` 1M, published `compact_at` 92 and `compact_window` 200000: ceiling 184k;
  the alert at 80% (800k) is above it → `below-compaction`, the mark written by the dispatcher,
  one notice, the check For you; the same agent with `compact_at` 92 and no window: no ceiling
  (the bound is never compared as 92% of the fill's window); observed at 240k on a 1M `Window`:
  `below-compaction`; a later reading with a ceiling above the alert deletes the mark.
- the check never writes (`EvaluateContext` is given marks, returns an outcome).
- the hook: delivers step 1 then step 2 once each per session; a subagent's event, another session,
  a missing variable, a down broker (budget 300 ms) print nothing and exit 0; its output is exactly
  the fixed sentence with numbers and names only (a `{p}:nudge` value carrying any other field is
  ignored); it never exits 2.
- compaction: same session, fill below the alert, no hand-off: one `wake=no` notice, once per
  episode; a new session (clear, relaunch): none; `compact_boundary` read into `compact_tokens`.
- launcher: a Claude Code agent's environment holds `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` and, when
  set, `CLAUDE_CODE_AUTO_COMPACT_WINDOW` from the settings; 0 exports nothing; another client never
  gets them; the agent's status publish carries the two values from its own environment.

Footprint, two PRs:
- **A (ladder, r2's footprint, plus the compaction notice):** `dispatch/contextalert.go`,
  `dispatch/context.go`, `bus/context.go`, `health/evaluate.go`, `health/health.go`,
  `cmd/agentbus/healthchecks.go`, `cmd/agentbus/quotacmd.go`, `cmd/agentbus/notifyfacts.go`,
  `config/keys.go`, `skills/agent-bus-master/SKILL.md`, an operator note fragment.
- **B (the hook and the ceiling):** `{p}:nudge` in `bus/context.go` and `bus/keykinds.go`;
  `cmd/agentbus/turnhook.go` (`turn tool`); `rolecfg` (the Claude Code `PostToolUse` hook; the
  launcher's export in `rolecfg/launch.go`); `quota/context.go` (`Window`) and the transcript
  reader (`compact_boundary`); the status publish's `compact_at`/`compact_window` (`bus/stream.go`,
  through `putSnapshot`); `agentstate/clients/*.toml` (`auto_compact`); `below-compaction` (the
  dispatcher's mark and notice, the check's reason); `config/keys.go` (`context_compact_window`). B starts with the pre-build check of §4.3.

## 9. Questions for the person

1. **Your answers to r2's questions** did not reach the bus: directive 1794777873906-0 ends at
   "### The person's answers (§8)". Please send them again (defaults 90% / 20 min; master woken at
   80% or not; your notice at step 2 only).
2. **`context_compact_at` 92 by default**, exported to every Claude Code agent: it can only make
   compaction earlier, never later. Alone it gives the bus no ceiling (§4.3: it is a share of a
   window the bus does not know). **`context_compact_window`**, default 0 (the client's own
   window): set it (e.g. 200000) only if you want the bus to know the bound exactly, at the cost of
   fixing the window the client compacts against. Agree with both defaults?
3. **`below-compaction`**: an agent that compacts below the alert is For you, and nothing changes a
   threshold by itself. Agree?
4. **The interrupt** (Esc, then a fixed line typed into the pane at `dispatch.context_interrupt`):
   recommended **not built**. With the mid-turn channel it would only cover one very long tool call
   and the clients without hooks, at the cost of a new pane-write exception. Keep it out?
