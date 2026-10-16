# Resume after a quota reset, fallback when the wait is long (v0.15)

Status: draft, 2026-10-16. Builds on the quota spec (`2026-10-15-quota-awareness-design.md`)
and on asks (`2026-10-16-asks-and-readable-text-design.md`).

## 1. What happened

On hyperliquid, foureyes (Codex, profile `review`) had accepted `dca-open-plan-v7-review`.
The OpenAI limit stopped it in the middle of its run, with "You've hit your usage limit" on its
screen. The dispatch service did its part: it saw the limit, held the OpenAI agents, and let them
go at the reset. But nobody restarted foureyes:

- The dispatcher wakes an agent only for **unread** messages. foureyes had already drained and
  accepted its request, so it owed nothing. An interrupted turn is invisible to it.
- master waited for a verdict that would never come. A person had to notice and relaunch it.
- Had the limit been the **weekly** one, the wait would have been days long. master can pop a
  role (`agent-spawn`), but only with the client its `roles.toml` profile names (the same,
  exhausted one), and not under the same agent name while the old one is registered.

## 2. Goals

1. **Resume.** When a provider's hold ends, every agent of that provider that had work in
   flight is told to resume it, and master is told. No person has to notice.
2. **Fallback.** When a hold will last long, the person is asked, through an ask in busmon,
   whether to replace the held agents by the same role on a fallback profile until the reset.
   Nothing changes model or cost without a person's yes.
3. **Swap back.** At the reset, master is told that a role runs on its fallback, and how to
   swap it back.

Not goals: an automatic swap without a person's answer; the dispatcher launching processes (it
still never runs code and never creates panes); choosing a model by cost.

## 3. Resume (dispatcher)

### 3.1 When

In `quotaPass`, at the transition that ends a hold for provider `p`:

- a usable reading under `HoldPct` while a hold is recorded (`quota-release`), or
- a recorded hold whose reset has passed with no usable reading (the hold expires).

### 3.2 Who

The agents of `p` (snapshot `Provider == p`) with **work in flight**:

- a board entry they own whose state is not `done` (`accepted`, `requested` but already drained,
  `blocked`, any free-form state), or
- a declared state of `working`.

An agent with none of these is left alone: it has nothing to resume, and an unread message would
wake it through the normal path anyway.

### 3.3 What

- To each such agent, one fenced directive from `dispatch` (it creates an obligation, so the
  normal wake follows):

  > Your provider's limit (openai, 5h) has reset. Work interrupted by it: dca-open-plan-v7-review
  > (accepted 1h ago). Resume it where you stopped, then report as usual.

  The task list names the board tasks found in §3.2, or says "your declared work: <message>".
- To the escalation target (master), one fenced `wake=no` notice listing who was told to resume.
  master is not woken for it: the verdicts it waits for come through the normal path.
- Once per hold. A mark `resumed:<p>` = the hold's reset (Unix s) in `{p}:quota`, written in the
  same pass, prevents a second notice if the release flaps.

The quota pause of the coordinator's provider already ends with the resume directive to master
(`FencedResume`). Resume adds the other agents of that provider.

## 4. Fallback profiles (roles.toml)

A role may list the profiles it can run on when its own provider is out:

```toml
[roles.foureyes]
profile = "review"                  # Codex
fallback_profiles = ["opus"]        # Claude Opus, in order of preference
```

- Schema v2 only. Each name must be a defined profile, distinct from `profile`, and must
  resolve like any profile. A profile on the **same provider** as the role's own is refused,
  because it would be held too.
- `agentbus roles fallbacks <role>` prints the role's profile and its fallbacks, with client,
  provider and model, one per line (and `--json`).
- The bootstrap template gets `fallback_profiles` on foureyes (`review` → `opus`) and on coder
  (`opus` → `review`), commented, so a new project sees the option.
- The 4-eyes rule is reviewer ≠ author **by agent name**, so a swap never breaks it. Still, when
  the fallback puts the reviewer on the **same provider** as the author of the work it reviews
  (coder), `roles fallbacks` and the ask say so, because the review loses its model diversity.

## 5. Long hold → a question for the person

### 5.1 Trigger (dispatcher)

When a hold for `p` is first recorded and its reset is more than `LongHold` away (default 2 h;
`AGENT_BUS_QUOTA_LONG_HOLD` in the service env, `0` disables it), the dispatcher sends **master**
one fenced directive:

> openai is held until Mon 20/10 03:00 (61h, weekly window). Held agents with work in flight:
> foureyes (dca-open-plan-v7-review). If their roles have fallback profiles
> (`agentbus roles fallbacks <role>`), ask the person whether to swap them until the reset.

It is sent once per hold (mark `longhold:<p>` = reset). The dispatcher does not read `roles.toml`:
the role manifest belongs to the launcher and master, not to the service.

### 5.2 master's part (skill)

On that directive, master:

1. Runs `agentbus roles fallbacks <role>` for each held agent with work in flight.
2. If at least one has a fallback, creates **one ask** (`agentbus ask master --file`), one
   question per role. Example question `foureyes`:
   - header "foureyes until Mon 03:00"
   - question "OpenAI is out until Monday 03:00. Replace foureyes (Codex) until then?"
   - options:
     - "Swap to opus (Claude Opus)" — recommended when the wait is longer than a day;
       description: cost on the Anthropic account, and the same-provider note of §4 if it applies.
     - "Wait for the reset".
3. On "swap", runs `agentbus swap <role> --profile <fallback>` (§6), then tells the swapped
   agent what to resume (the §3.3 text).
4. On "wait", does nothing. Resume (§3) will restart the agent at the reset.
5. With no fallback anywhere, it reports the wait to the person in its status (`waiting`, or an
   ask with the single choice "OK"), never silence.

## 6. `agentbus swap <role> --profile <profile>`

Run by master (or a person) inside the project's herdr session. Steps, each checked before the
next:

1. Resolve `<role>` in the project's `ROLES_TOML`; `<profile>` must be the role's own profile
   (swap back) or one of its `fallback_profiles`. `--force` accepts any defined profile and says so.
2. Read the agent's snapshot (agent name = role). Its pane must be live and its identity verified
   (pid and start time, as the dispatcher checks before typing). It must not be the caller itself:
   master cannot swap itself this way.
3. If the agent's pane shows a running turn (observed busy, §v0.14.3) or it declared `working`
   less than 2 minutes ago, refuse without `--now`: a swap kills that turn.
4. Close the old pane (`herdr pane close`), then wait until its pid is gone.
5. Launch the role on the chosen profile: `agent-spawn <role> <project> --profile <profile>`.
   `agent-launch`/`agentbus roles launch` accept `--profile` and resolve that profile instead of
   the role's. The new agent keeps the name `<role>`, binds its new pid (a verified identity
   replaces the old one), drains its queue and reads the board as at any boot.
6. Record the swap in `{p}:swaps` (hash, role → JSON `{from, to, by, at, provider_from}`;
   `at` from Redis `TIME`), so busmon and the dispatcher can tell it runs on a fallback.
7. Print what was done. The cmd consumer group, the board and the asks are keyed by agent name,
   so nothing is lost or moved.

Swapping back is the same command with the role's own profile, which clears `{p}:swaps[role]`.

## 7. Swap back at the reset

When a hold ends (§3.1) and `{p}:swaps` holds a role whose `provider_from` is that provider, the
dispatcher adds to its §3.3 notice to master:

> foureyes runs on its fallback (opus) since Fri 14:40. openai has reset: swap back with
> `agentbus swap foureyes --profile review` when its current work is done.

master swaps it back when the agent is idle, without asking: returning to the configured
profile is not a new decision. Agents swapped by hand with `--force` are only reported.

## 8. busmon

- An agent on a fallback shows the flag `fallback` (attention) and, in its detail, the row
  `Profile  opus (fallback for review since 14:40, openai held until Mon 03:00)`.
- The resume notices appear in Activity as directives from `dispatch`, like the quota warnings.

## 9. Safety rules kept

- The dispatcher only writes fenced bus entries. It never reads `roles.toml`, never launches,
  never closes a pane.
- Panes are closed and launched only by `agentbus swap`, run by master or a person, after an
  identity check, and never on a running turn without `--now`.
- No model or cost change without a person's yes, except the swap back to the configured profile.
- Tests use fakes for herdr and `agent-spawn`, the isolated Redis, and never the user's session.

## 10. Tests

- **dispatch:**
  - a release sends resume directives only to the provider's agents with work in flight, plus one
    notice to master;
  - nothing is sent twice when the release flaps;
  - an expired hold with no reading resumes too;
  - a long hold sends one master directive, and none under `LongHold` or when it is 0;
  - a swap recorded for the provider is mentioned at the release.
- **rolecfg:**
  - `fallback_profiles` is validated (unknown profile, same profile, same provider, schema v1);
  - `roles fallbacks` prints them and flags the same-provider review;
  - launch accepts `--profile`.
- **swap (fake herdr, fake agent-spawn):**
  - refuses an unknown profile, a profile not listed (without `--force`), a dead or unverified
    pane, a busy agent (without `--now`) and the caller itself;
  - in order: closes the pane, waits for the pid, spawns with the profile, records `{p}:swaps`;
  - swap back clears the record.
- **busmon:** the `fallback` flag and the detail row.
- **Skill:** master's section "When a provider is out for long" (§5.2), and its handling of the
  swap-back notice (§7).

## 11. Open questions for review

1. Should resume also wake agents of other providers that were waiting on a held agent (master
   waiting for foureyes)? Proposal: no. Their wait ends when the held agent delivers.
2. `LongHold` default 2 h: long enough to skip a 5-hour window near its end, short enough to
   catch the weekly one.
3. Should master be allowed to swap without asking when the person pre-authorised it
   (`auto_fallback = true` per role)? Proposal: not in v0.15. Ask first, see how it is used.
