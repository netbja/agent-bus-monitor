# Swap history, swap back, asks answered elsewhere, architect guidance, model drift (v0.15.1)

Status: rev 1, 2026-10-17, for Codex review. Builds on v0.15
(`2026-10-16-quota-resume-and-fallback-design.md`), which deferred the durable swap record, the
`fallback` flag and the swap back (§2 there).

## 1. What happened

- **No memory of a swap.** `agentbus swap` replaces an agent and prints a line. Nothing records
  that foureyes now runs on opus instead of review, since when, on whose decision, or that it
  should go back. The resume notice only reminds master in words.
- **An ask left open by a direct answer.** On 2026-10-17 master asked "GO d'exécution" with
  `agentbus ask`. Bernard answered "oui GO" with `i @master`, a plain directive. master acted on it
  but never closed the ask; busmon kept counting it as a person's pending decision, fading and
  ringing, for 14 hours.
- **When to pop architect is unstated.** The master skill says `agent-spawn architect` for
  "design work" and nothing about when, when not, or when to close it. master runs on Sonnet; the
  heavy decisions it should hand to an Opus role are not named.
- **Model drift nobody sees.** hyperliquid's agents were launched before `roles.toml` moved to
  the 5.5 models. On 2026-10-17 coder still ran `claude-opus-4-8`, master and deploy
  `claude-sonnet-5`, sentinel `claude-haiku-4-5`, while the manifest said `*-5-5`. A provider can
  also rename or retire a model id; an agent launched on it then fails or runs something else.
  Nothing shows the difference between the configured, launched and used model.

## 2. Scope

In:

1. A durable swap ledger `{p}:swaps` and the current fallback state `{p}:fallback` (§3).
2. `agentbus swap <role> --back` and one notice to master when the role's own provider is back
   (§4).
3. Asks answered elsewhere: master withdraws, busmon offers the panel (§5).
4. Master skill: when to pop and close architect, how the role picks the model (§6).
5. Model drift: aliases in the examples, the launched model recorded, `model≠` in busmon,
   `agentbus roles doctor`, the sentinel's daily check (§7).

Not goals: any automatic swap, relaunch or model change; reading a model list that costs tokens;
a price table.

## 3. Swap ledger and fallback state

### 3.1 `{p}:swaps` (stream, append-only)

One entry per event of a swap, written by `agentbus swap` itself (no lease; a person or master
runs it):

| field | value |
|---|---|
| `role` | the role swapped |
| `event` | `begin`, `done`, `failed`, `back` |
| `from_profile`, `to_profile` | profiles; `from_provider`, `to_provider`, `to_model` |
| `by` | the caller (`AGENT_BUS_AGENT` or the person's name) |
| `reason` | free text, ≤ 200 runes (`--reason`; master puts the ask id and answer) |
| `old_pid`, `old_pane`, `new_pid`, `new_pane` | identities, when known |
| `episode` | the hold's reset (`hold:<provider>` value) when the old provider was held |
| `error` | on `failed`: the step and its message |

`MAXLEN ~ 1000`, like the other streams; it is an audit of recent swaps, not the only proof (the
`roles.toml` and git keep the configuration). `agentbus swaps [--role r] [--json]` lists it, newest
first.

### 3.2 `{p}:fallback` (hash, role → JSON)

Written by `agentbus swap` **in the same script as the `done` entry** when the new profile is not
the role's own (`profile` in `roles.toml`): `{own_profile, own_provider, profile, provider,
since (Redis TIME), by, reason, episode}`. Deleted, with the `back` entry, when a swap ends on the
role's own profile. A swap from one fallback to another rewrites it and keeps `own_*`.

Neither key is in `projectKinds` (they never make a project); `busmon --delete` removes them with
the rest.

### 3.3 busmon

- Flag `fallback <profile>` on the agent's row (after `swapping`, before `busy`).
- Detail row "Fallback: on opus (anthropic) since 14:02, by master — own profile review (openai);
  back with: agentbus swap foureyes --back".

## 4. Swap back

### 4.1 `agentbus swap <role> --back`

Same as `--profile <its own profile>`: same preflight, intent, proof and resume. Refused with a
clear message when `{p}:fallback` has no entry for the role **and** the running profile (the
snapshot's provider/client) already matches its own profile ("already on its own profile").
`--back` and `--profile` are exclusive.

### 4.2 Notice when the own provider is back

In `quotaPass`, when the hold of provider `q` ends (the v0.15 release paths), for every role whose
`{p}:fallback` entry has `own_provider == q`: one `wake=no` directive to master, once per episode
(`FencedCmdOnce`, mark `back:<q>:<role>` = the episode):

> openai is back. foureyes runs on its fallback opus since Fri 14:02: swap it back when its
> current work is done (agentbus swap foureyes --back).

An own provider never held during the swap (a swap for another reason) gets no notice; the
busmon flag remains the reminder.

## 5. Asks answered elsewhere

### 5.1 master's side (skill)

Rule added to "When you need the human's decision": when you act on a person's answer that came
outside the ask (a directive, `@master …`), withdraw the ask first
(`agentbus ask <you> --withdraw <id>`), so busmon stops showing a pending decision.

### 5.2 busmon's side

When the person types `@<agent> …` (or `@all`) and that agent has an open ask, busmon does not
send at once. The input line shows: "master has an open question (GO d'execution). Enter: send
as a message · ! : answer in the question panel · Esc: cancel". Enter sends it as before. `!` opens
the ask panel with the typed text in the free answer of the first open question (it is not sent
until the person submits the panel). For `@all`, the hint names the agents with open asks; Enter
sends to all. One extra key, only when an ask is open, never a block.

## 6. Master skill: roles, models, architect

New section "Choosing who works on it":

- The model follows the role (`roles.toml`); master never names a model. To get a stronger model,
  give the task to a role that runs one (`agentbus roles fallbacks` and the manifest show which).
- Pop **architect** (`agent-spawn architect <project>`) when the task is: a design or architecture
  choice; a spec touching several modules or services; a disagreement between coder and foureyes
  that survived one round; a cross-cutting risk (security, data loss, deployment, money). Not for
  a routine change: that stays with coder.
- master runs on a lighter model than architect: on a heavy decision, have architect instruct it
  (options, risks, recommendation) and put the decision to the person with an ask, rather than
  deciding alone.
- Close architect once its recommendation is delivered and recorded (a report, a memory note if
  durable): `agentbus shutdown architect`, so it stops drawing on the quota.
- Same rule for security (pop for a security review, close after the verdict) and docs (popped by
  the conductor's `complete`, or by master).

## 7. Model drift

### 7.1 Three facts, no token spent

| Fact | Source | Stored |
|---|---|---|
| configured | `roles.toml` resolved for the role's current profile | read on demand (`rolecfg`) |
| launched | the launcher's `AGENT_BUS_MODEL` | new `AgentSnapshot.model`, written by `bind` (launcher) and `status` from the env, like `client`/`provider` |
| used | the last assistant `message.model` in the agent's transcript (Claude); Codex's session log `model` when present | `{p}:usage` (exists for Claude) |

Rules:

- `launched` is compared to `configured` only by equality of the configured string; an alias
  (`opus`) is never "drift" against the id it resolves to.
- `used` is compared to `launched` only when `launched` is an exact id; with an alias, `used`
  is shown, not judged. `<synthetic>` (an error turn) is ignored.
- Drift is: `launched ≠ configured` (the manifest moved since launch), or a `used` model that
  differs from an exact `launched` id, or a recent transcript error naming an unknown or
  retired model.

### 7.2 Where it shows

- busmon: flag `model≠` (attention colour, low priority, after `busy`), detail rows "Model:
  launched claude-sonnet-5 · roles.toml now claude-sonnet-5-5 (relaunch: agentbus swap master
  --back)" and "Used: claude-sonnet-5 (transcript, 3m ago)".
- `agentbus roles doctor [--json]`: one line per registered agent: role, profile, configured,
  launched, used, verdict (`ok`, `drift`, `unknown model`, `not reported`). Exit 0 when all ok, 3
  on drift. It reads only Redis, the manifest and transcripts the usage reader already reads; it
  never calls a model.
- Sentinel, on its daily wake: run `roles doctor`; on drift, one report to master naming the
  roles (no repeat while unchanged: the sentinel keeps the last verdict line in its report).
  master asks the person whether to relaunch each role, when it is idle, with
  `agentbus swap <role> --back` (§4.1 accepts a role on its own profile whose model drifted; the
  "already on its own profile" refusal does not apply when the doctor reports drift).

### 7.3 Aliases by default

- `examples/roles.mixed.toml`, `roles.toml` and the bootstrap template use Claude Code's aliases
  (`opus`, `sonnet`, `haiku`), which follow the latest model of each family. An exact id stays
  allowed, for a project that wants to pin one; the comment above `model` says so.
- Codex: keep the configured id (no alias is documented); the doctor's `unknown model` verdict is
  the guard. Kimi: native aliases already.
- `rolecfg` accepts aliases as today (non-empty string, no leading `-`).

## 8. Safety rules kept

- Only `agentbus swap` and agent launches change a running agent; the dispatcher still launches
  and closes nothing. Neither the ledger nor the doctor triggers a swap.
- Ledger and fallback writes happen in the swap's own scripts; the dispatcher's only new write is
  the §4.2 notice, fenced and once per episode.
- busmon's §5.2 hint changes nothing on the bus until the person chooses.
- Tests: fake herdr and agent-spawn, isolated Redis (:6392), never the user's session or broker.

## 9. Tests

- **bus:** ledger append and listing; fallback set/clear with the `done`/`back` entry in one
  script; `model` in the snapshot (bind/status, merge rules: an unverified status never erases
  it).
- **swap:** `--back` resolves the own profile; refused when already on it without drift;
  allowed with drift; `failed` entry on every failure path; `--reason` recorded.
- **dispatch:** the back notice once per episode, only for roles whose own provider was held;
  none when no fallback entry.
- **busmon:** `fallback` and `model≠` flags and detail rows; the `@agent` hint with an open ask
  (Enter sends, `!` opens the panel prefilled, Esc cancels), none without an ask.
- **roles doctor:** alias vs id, launched vs configured, used vs launched, `<synthetic>` ignored,
  unknown-model error, exit codes.
- **skill:** master sections (§5.1, §6), sentinel daily doctor.

## 10. Next: a model table per provider (v0.15.2, reviewed with this spec)

Bernard's request (2026-10-17): model names should not be hard-coded in `roles.toml`; a table per
provider should keep itself current. Every client already keeps a free local catalogue; none is
read by calling a model.

### 10.1 Sources (one reader per client, like the quota readers)

| Provider | Source | Read |
|---|---|---|
| openai (Codex) | `~/.codex/models_cache.json`, refreshed by Codex itself | `slug`, `display_name`, `description` ("Latest workhorse…", "Frontier…", "Fast…", "Older…") |
| xai (Grok) | `grok models` (fixed argv, 10 s bound) | the list and the default marker |
| moonshot (Kimi) | `~/.kimi*/config.toml` `[models.*]`, `default_model` | ids and aliases |
| google (agy, Antigravity) | `agy models` (fixed argv, 15 s bound) | id and label (`gemini-3.8-flash-high	Gemini 3.8 Flash (High)`); agy also serves other families (Claude, GPT-OSS) on the Google account: such rows keep `provider = google` (whose quota they draw on) and add `family` (`anthropic`, `openai`) |
| anthropic (Claude) | Claude Code's aliases (`opus`, `sonnet`, `haiku`, `fable`); the model options cached in `~/.claude.json` (only the model keys: `additionalModelOptionsCache`, `orgModelDefaultCache`; never the account fields); the resolved id last seen per alias in transcripts | alias → id, label |

No reader opens a credential file (`auth.json`, `.credentials.json`, `credentials`,
`oauth_creds.json`, `google_accounts.json`). A missing or
unreadable source is a note, never an error that stops the others.

### 10.2 The table

`~/.cache/agentbus/models.json` (machine scope, like the quota cache), one row per model:
`provider, client, id, label, tier, default, source, seen_at`. `tier` is `frontier`,
`workhorse`, `fast` or `legacy`: from the description when it says so, else from
`~/.config/agentbus/models.toml` (a person-maintained map of id patterns → tier), else empty.
`agentbus models [--provider p] [--json]` prints it; `agentbus models --refresh` rereads every
source. The sentinel refreshes it on its daily wake; `setup.sh update` refreshes it too.

### 10.3 Roles name a tier, not an id

A schema-2 profile may set `tier = "frontier"` instead of `model`. At launch, `rolecfg` resolves
it to the table's `default`-or-first row of that provider and tier and passes the id to the
client; `AGENT_BUS_MODEL` records the resolved id, and `AGENT_BUS_MODEL_TIER` the tier. An exact
`model` always wins; `model` and `tier` together are refused. With no matching row, the launch
fails with the table's state and the fix (`agentbus models --refresh`, or set `model`).

### 10.4 Never behind the person's back

The table changes by itself; a running agent never does. The doctor (§7.2) adds two verdicts:
`newer` (a newer row exists for the agent's tier; for an exact id, a row of the same family
ranked newer) and `gone` (the launched id is no longer in its provider's table). busmon shows
`model≠` for both; master asks the person; `agentbus swap <role> --back` relaunches.

## 11. Rev 2 — Codex review of rev 1 (2026-10-17), what changes

These rules replace the corresponding text above where they differ.

1. **Closing one role (§6).** `agentbus shutdown` is a team-wide broadcast, so the skill must not
   use it for architect. New `agentbus retire <role> [--now]`: the swap's preflight (verified
   identity, not the caller's pane, no open challenge or mission, no running turn unless `--now`),
   then the swap intent, `herdr pane close`, the old pid gone, a `retire` ledger entry, the intent
   deleted. It launches nothing. The skill tells master to ask architect for its final report,
   then `agentbus retire architect`.
2. **Ledger lifecycle (§3).** `begin` is written in the same script as the swap intent (`SET NX
   PX` + `XADD`, refused together). The commit point is the proof: `done` (with the fallback
   set/clear) is written then, in one script. Every failure path writes `failed` with the step.
   A resume that cannot be sent after the proof writes a `warning` entry (the swap stays done)
   and exits 1 with the command that sends it. A crash between `begin` and a terminal entry
   leaves an intent that expires in 3 min: `agentbus swaps` shows such a `begin` without a
   terminal entry and without a live intent as `interrupted`, and the next `swap` or `retire` of
   that role first appends `failed (interrupted)` for it.
3. **`--back` and the back notice (§4).** `--back` is self-contained: it resolves the role's own
   profile and compares its configured `model` (or tier, v0.15.2) with the snapshot's launched
   `model`; it relaunches when the profile differs, when the models differ, or when a fallback
   record exists, and refuses only when all three agree ("already on its own profile and
   model"). The fallback record stores `held_episode`: the `hold:<own_provider>` value at the
   swap's `begin`, empty when that provider was not held. The §4.2 notice goes only when the
   released episode equals `held_episode`.
4. **Asks answered elsewhere (§5.1).** Withdrawal becomes question-scoped:
   `agentbus ask <agent> --withdraw <id> [--question <qid>]` marks one question withdrawn (one
   Lua script, like the others); the ask closes when no question remains open. The skill: on a
   direct answer, withdraw exactly the questions it answers, citing the message id in a reply on
   the ask's thread; leave the others open.
5. **busmon hint (§5.2).** Typing stays literal (`!` is just a character). The check happens on
   Enter: when a recipient (the named agent, or the registered agents `@all` reaches) has an open
   ask, the input line shows "master has an open question. Enter: send as a message · Tab:
   answer in its question panel · Esc: back to editing". Tab opens the ask panel of that agent,
   with the typed text as the free answer of its first open question; with several recipients
   holding asks, Tab first shows a chooser listing only them. Nothing is sent before Enter or the
   panel's own submit.
6. **The launched model and the snapshot merge (§7.1).** `model` is part of the identity: it is
   written only together with a verified identity (pid and start) that `putSnapshot` accepts as
   current (`bind`, or a verified `status` newer than `ident_ts`). An unverified or delayed
   status never sets or erases it. Tests: an unverified status with a non-empty model, a delayed
   verified status, a bind.
7. **The used model (§7.1).** v0.15.1 reports `used` for Claude Code only: the last assistant
   entry of the agent's transcript whose `message.model` is set and is not `<synthetic>`, with
   its time (a separate reader from the usage-bearing one). Other clients show `used: not
   available` until a reader bound to their session exists (v0.15.2 or later). A `used` older
   than 24 h is shown as stale and never judged.
