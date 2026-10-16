# Resume after a quota reset, fallback when the wait is long (v0.15)

Status: rev 2, 2026-10-16. Rev 1 was challenged by Codex (8 points, §12). Builds on the quota spec
(`2026-10-15-quota-awareness-design.md`) and on asks (`2026-10-16-asks-and-readable-text-design.md`).

## 1. What happened

On hyperliquid, foureyes (Codex, profile `review`) had accepted `dca-open-plan-v7-review`.
The OpenAI limit stopped it in the middle of its run ("You've hit your usage limit"). The dispatch
service saw the limit, held the OpenAI agents, and let them go at the reset. But nobody restarted
foureyes:

- The dispatcher wakes an agent only for **unread** messages. foureyes had already drained and
  accepted its request, so it owed nothing. An interrupted turn is invisible to the dispatcher.
- master waited for a verdict that would never come. A person had to notice.
- Had it been the **weekly** limit, the wait would have been days. master can pop a role
  (`agent-spawn`), but only on the profile `roles.toml` gives it (the exhausted one), and not
  under the same name while the old agent is registered.

## 2. Scope of v0.15

In:

1. **Resume.** When a provider's hold ends, every agent of that provider with accepted or declared
   work is told to resume it, exactly once, and master is told.
2. **Fallback profiles** in `roles.toml`, and `agentbus roles fallbacks <role>`.
3. **Long hold → the person decides.** For a hold that will last long, the person gets the
   question, through master's ask or, when master itself is held, directly in busmon.
4. **`agentbus swap <role> --profile <p>`**: a manual, checked replacement of an agent by the same
   role on another profile, run by master after a yes or by the person.

Deferred (Codex §12.8): a durable swap record, a busmon `fallback` flag and an automatic
swap-back at the reset. In v0.15 the resume notice reminds master to swap back what it swapped,
and the agent's row already shows its client and provider.

Not goals: a swap without a person's yes; the dispatcher launching processes or closing panes;
choosing a model by cost.

## 3. Resume (dispatcher)

### 3.1 When

In `quotaPass`, when the hold of provider `p` ends:

- a usable reading under `HoldPct` while a hold is recorded (`quota-release`), or
- a recorded hold whose reset has passed with no usable reading (the hold expires).

The reset value `R` of that hold (Unix s, the `hold:<p>` mark) identifies the episode.

### 3.2 Who: an observable predicate

The agents with `Provider == p` that have, at that pass:

- a board entry they own in state **`accepted`**, or
- a declared state **`working`**.

Excluded on purpose:

- `requested`: an unread request still wakes the agent through the normal path, and a drained but
  unaccepted one is not observable.
- `blocked`: it needs a person or a dependency, not a resume.
- `done` and every other state.

### 3.3 What, exactly once

- To each such agent, one directive from `dispatch`. The text is bounded: at most 5 tasks named,
  then "and N more". The whole is cut to `CmdMaxRunes`.

  > Your provider's limit (openai, 5h) has reset. Work interrupted by it: dca-open-plan-v7-review.
  > Resume it where you stopped, then report as usual.

- To the escalation target (master), one `wake=no` notice: who was told to resume, plus, when it
  applies, "if you swapped a role during this hold, swap it back when its work is done (`agentbus
  swap <role> --profile <its profile>`)".
- **Exactly once per recipient and episode**, crash-safe. Each message is written by one new
  fenced script, `FencedCmdOnce`. In a single script it:
  1. checks the lease;
  2. checks the per-recipient mark `resumed:<p>:<agent>` in `{p}:quota` against `R`;
  3. if the mark differs, XADDs the entry and sets the mark.

  A pass that crashes halfway leaves the sent ones marked and resends only the others. A flapping
  release finds every mark set and sends nothing.
- One recipient's failure (validation, a transport error) is logged and does not stop the others.
  A lease error still ends the pass.
- The marks of an episode are removed when the next hold for `p` is recorded, so the next episode
  starts clean.

## 4. Fallback profiles (roles.toml)

```toml
[roles.foureyes]
profile = "review"                  # Codex
fallback_profiles = ["opus"]        # Claude Opus, in order of preference
```

Validation (schema v2 only):

- each fallback must be a defined profile that resolves, distinct from `profile`;
- at most **3** fallbacks, so that an ask question (6 options at most) fits them plus "Wait";
- a fallback on the **same provider** as the role's own profile is refused, because it would be
  held too.

`agentbus roles fallbacks <role> [--json]` prints the role's profile, then each fallback with its
client, provider and model. When a fallback would put a reviewer role (foureyes, security) on the
provider of `coder`'s profile, it adds "same provider as coder: the review loses its model
diversity". The 4-eyes rule itself (reviewer ≠ author by name) is never broken by a swap.

The bootstrap template shows `fallback_profiles` commented on foureyes and coder.

## 5. Long hold → the person decides

### 5.1 Trigger (dispatcher)

When a hold for `p` is first recorded and its reset is more than `LongHold` away (default 2 h;
`AGENT_BUS_QUOTA_LONG_HOLD` in the service env; `0` disables), once per episode (mark
`longhold:<p>` = `R`, written by `FencedCmdOnce`):

- **`p` is not the coordinator's provider:** one directive to master:

  > openai is held until Mon 20/10 03:00 (61h, weekly window). Held agents with work in flight:
  > foureyes (dca-open-plan-v7-review). If their roles have fallback profiles
  > (`agentbus roles fallbacks <role>`), ask the person whether to swap them until the reset.

- **`p` is the coordinator's provider** (Codex §12.1): master is paused and held; it cannot ask
  anyone. The dispatcher writes a `notify` from `dispatch` instead, which busmon shows in Activity
  and, while the quota pause lasts, in the header:

  > The project is paused until Mon 03:00 (anthropic, weekly). To continue before, swap master
  > to another provider from a terminal: `agentbus roles fallbacks master`, then
  > `agentbus --project hyperliquid swap master --profile <fallback>`.

  busmon's pause segment adds "long pause, see Activity" for a pause over `LongHold`.

The dispatcher does not read `roles.toml`. The role manifest belongs to the launcher, to master and
to the person.

### 5.2 master's part (skill)

On the directive of §5.1, master:

1. Runs `agentbus roles fallbacks <role>` for each held agent named.
2. If at least one role has a fallback, creates one ask with one question per role, **at most 6**.
   If more roles are concerned, it asks first for those with work in flight, and asks for the rest
   after this ask is answered. The question id is the role name, so each answer maps to one role.
   - question: "OpenAI is out until Monday 03:00. Replace foureyes (Codex) until then?"
   - options: one per fallback ("Swap to opus — Claude Opus", recommended if the wait is longer
     than a day, its description noting the cost and the same-provider remark), then
     "Wait for the reset".
3. On a swap answer, runs `agentbus swap <role> --profile <fallback>` (§6). If the swap refuses,
   it reports why in the thread of the ask (`agentbus reply`), so the person sees it.
4. On "Wait", does nothing: resume (§3) restarts the agent at the reset.
5. With no fallback anywhere, it tells the person the expected wait in its status.

## 6. `agentbus swap <role> --profile <profile> [--now]`

Run inside the project's herdr session, by master or by a person. Each step is checked before the
next; nothing is closed before everything that can be checked has been.

### 6.1 Preflight (nothing changed yet)

1. Resolve `<role>` in the project's `ROLES_TOML`. `<profile>` must be the role's own profile
   (swap back) or one of its `fallback_profiles`. `--force` accepts any defined profile and says
   so. The profile must resolve, and its client executable must be on `PATH`.
2. The agent (name = role) must have a snapshot with a live pane and a verified identity (pid
   and start time, as the dispatcher checks). The caller must not run inside that pane: an agent
   cannot swap itself. A person in a terminal can swap master.
3. Refuse when the agent is part of an open review (Codex §12.4):
   - an open 4-eyes challenge in which it is the gated author (`{p}:gate:<role>` not empty), or
     the reviewer (any gate meta naming it as reviewer);
   - a mission attempt where it is the reviewer with the review open (`ReviewOpen`), or the
     assignee of an attempt in progress.

   The refusal lists them. v0.15 has no transfer of an open review: it must be closed first
   (verdict, `mission reassign`), which is audited where it already is.
4. Refuse when the pane shows a running turn (`{p}:observed` busy) or the agent declared
   `working` less than 2 minutes ago, unless `--now` (a swap kills that turn).

### 6.2 Handoff

5. Write the **swap intent** `{p}:swap:<role>`, a Redis string with `SET NX PX 180000`. Its value
   is `{to_profile, by, old_pid, old_start}`. A second swap of the same role is refused while it
   exists.
6. The dispatcher treats an agent with a swap intent as **not promptable**. `Decide` gets a rule
   before the pane inspection: "swap in progress → wait". The prompt path re-reads the intent
   right before `Prompt` and gives up if it appeared.
   - Residual race: a wake line typed into the old pane in the instant before it closes is lost
     with the pane. The wake record then stays `prompted` or `unobserved` with no progress. The
     identity change (new pid) resets its tries, so the next pass wakes the new pane.
   - Nothing is lost from the queue. The consumer group is keyed by the agent name, and an entry
     the old process did not ACK stays pending for the new one.
7. Close the old pane (`herdr pane close`) and wait, up to 15 s, for the old pid to be gone. The
   old background `subscribe` dies with its process group, and its receiver lease expires by TTL.

### 6.3 Launch and proof

8. Launch the role on the profile: `agent-spawn <role> <project> --profile <profile>`.
   `agent-launch` and `agentbus roles launch` accept `--profile`, which resolves that profile
   instead of the role's. In swap mode (`AGENT_LAUNCH_REQUIRE_BIND=1`), a failed `bind` is fatal
   instead of a warning (Codex §12.5).
9. Wait, up to 60 s, for the **proof of replacement**. The snapshot of `<role>` must show:
   - a pid different from the old one, alive, with a matching start time;
   - the pane of the new tab;
   - a client and provider equal to the chosen profile's.

   This is the launcher's own pre-exec `bind`, the one Codex needs because its commands run under
   a shared app-server.
10. Delete the swap intent, print what was done, and (when run by master) report it. The
    resumption of the agent's work is the §3 text, sent by the caller as a directive once the
    proof holds, so the dead old process can never consume it.

### 6.4 Failure and recovery

- **Before step 7** (preflight, intent refused): nothing changed. Print the reason.
- **Spawn or proof fails after the close:** delete the intent and print a loud error with the
  recovery command: `agent-spawn <role> <project>` on the role's own profile. Exit non-zero. The
  role is down until then; the dispatcher escalates its unread messages as for any dead pane.
- **The caller dies mid-swap:** the intent expires after 3 minutes, and the dispatcher resumes
  normal handling.

## 7. busmon

- The quota pause segment keeps its reason and adds "long pause, see Activity" past `LongHold`.
- Resume directives and the `dispatch` notify appear in Activity like the quota warnings.
- An agent under a swap intent shows the flag `swapping`.

## 8. Coordinator swapped during its own quota pause

When master is swapped to another provider while a quota pause for `p` is in place, the next
`quotaPass` sees the coordinator's provider differ from `p`. It then ends that quota pause
(`FencedQuotaUnpause(p)`): the pause existed only because the coordinator was out. The resume
directive of `FencedResume` goes to master as usual.

## 9. Safety rules kept

- The dispatcher only writes fenced bus entries (`FencedCmdOnce` is one more). It never reads
  `roles.toml`, launches anything or closes a pane.
- Panes are closed and launched only by `agentbus swap`. It runs after an identity check and a
  review check, never on a running turn without `--now`, and only with proof before it reports
  success.
- No model or cost change without a person's yes. Swapping back to the configured profile is
  not a new decision.
- Tests use fake herdr and `agent-spawn`, the isolated Redis (:6392), and never the user's session.

## 10. Tests

- **dispatch:**
  - a release resumes only the agents with `accepted` tasks or `working`, never `blocked` or
    `requested`;
  - a crash between two recipients (injected error) resends only the missing one;
  - a flapping release sends nothing more;
  - an expired hold resumes too;
  - a long hold sends one directive to master for another provider, and one notify for the
    coordinator's provider; none under `LongHold` or when it is 0;
  - a swap intent blocks prompting;
  - a coordinator whose provider changed ends the quota pause.
- **bus:** `FencedCmdOnce` (lease, mark, idempotence) and the swap intent (`NX`, expiry).
- **rolecfg:**
  - `fallback_profiles` is validated (unknown, same profile, same provider, more than 3, schema v1);
  - `roles fallbacks` prints them and flags the same-provider review;
  - launch accepts `--profile`, and a bind failure is fatal with `AGENT_LAUNCH_REQUIRE_BIND`.
- **swap (fake herdr, fake agent-spawn):**
  - every refusal of §6.1 and a second concurrent swap;
  - the order intent, close, wait for the pid, spawn, proof, intent deleted;
  - a spawn failure and a missing proof leave no intent and print the recovery.
- **busmon:** the `swapping` flag and the long-pause hint.
- **Skill:** master's section "When a provider is out for long" (§5.2).

## 11. Open questions (answered in review)

- Resume wakes only the held provider's agents; master's waiting ends when the resumed agent
  delivers (agreed).
- `LongHold` defaults to 2 h and is configurable (agreed).
- No `auto_fallback` before the consent and recovery path has been used for real (agreed).

## 12. Codex review of rev 1 (2026-10-16) and what changed

1. A held coordinator cannot ask anyone → §5.1 sends a `notify` to the person instead, the person
   can swap master from a terminal (§6.1.2), and §8 ends the pause once master's provider changes.
2. Per-hold mark not crash-safe; unbounded text → per-recipient `FencedCmdOnce` (§3.3), bounded
   text, a failure does not stop the others.
3. Swap races the dispatcher's prompt → swap intent, a `Decide` rule, a re-check before `Prompt`,
   the residual race named, the queue kept by name, the resume sent only after the proof (§6.2-6.3).
4. Open reviews keyed by name → refused while open (§6.1.3), no transfer in v0.15.
5. Spawn is not proof → preflight before the close, a fatal bind in swap mode, proof by the new
   identity and profile, failure and recovery (§6.3-6.4).
6. Ask limits → at most 3 fallbacks, at most 6 roles per ask, then sequential asks keyed by role
   (§4, §5.2).
7. Resume predicate → `accepted` or `working` only, `blocked` and `requested` excluded (§3.2).
8. Scope → durable swap record, `fallback` flag and automatic swap-back deferred (§2).
