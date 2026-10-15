# v0.13 — Quota awareness: stop before the limit, for any AI

Status: rev 2, 2026-10-15. Rev 1 (0056998) reviewed by Grok: approve with fixes
(`.superpowers/brainstorm/GROK-v0.13-spec-review.md`, local); every finding is applied below and
listed in §13. Rev 2 also makes the readers generic (Bernard: "any AI except a local one").
Research: `.superpowers/brainstorm/QUOTA-{codex,grok,kimi}.md` (local, not committed).

## 1. Problem

Agent teams run until a provider refuses them: Claude Code `You've hit your session limit ·
resets 3:45pm`, Codex `less than 5% of your 5h limit left`, Kimi `403 You've reached your 5-hour
usage limit`. Work stops mid-turn and the resume depends on someone noticing. The bus knows
Claude's windows only through `~/.cache/ccstatusline/usage.json`, a third-party status line's
cache, refreshed when the sentinel wakes (22 h stale when last checked); nobody reads the others.

## 2. Goals and non-goals

Goals:
- For every provider a team uses, know the used % of each window, its reset and the age of the
  observation, from first-party sources, with zero LLM tokens.
- **Any AI can be plugged in** without changing the policy or busmon: built-in readers where code
  is needed, declarative readers for "a file with these fields", and an external-command protocol
  anyone can implement (§5). A local model declares itself unlimited.
- Warn the coordinator early; stop waking the agents of a provider near its limit until its
  reset; pause the project when the coordinator's own provider runs out.
- Show it in busmon. No dependency on ccstatusline.

Non-goals (v0.13): built-in Kimi and agy readers (they can come through §5.3 — Kimi's numbers
need its OAuth token, agy's headless `/usage` is unverified); interrupting a turn in progress;
API-key billing and RPS/TPM limits; several accounts of one provider on one machine (§11).

## 3. Rules that hold everywhere

1. **Unknown is not 0 %.** Missing, malformed or too old (§7.1) is unknown: shown as such, never
   warns, never holds, never releases.
2. **Observed ≠ collected.** `observed_at` is when the provider's number was true, as close as the
   source allows; `collected_at` when we read it. Rewriting an unchanged number is not fresher.
3. **Shared windows are not added.** One account, one gauge — never a sum of agents.
4. **The tightest window decides**, recomputed every pass over all windows of the provider.
5. **A window whose reset is already past is unknown for policy** (a stale 99 % never re-holds; a
   0 % is never invented).
6. **No secrets.** No reader opens a token, cookie or credential file. Nothing from a source but
   allowlisted numeric/enum fields reaches a cache, a log or the bus: never transcript text,
   `tokenHash`, a settings signature, request ids, status-line stdin, `settings.json`, a Grok log
   line or an app-server's stderr. No secret in argv.

## 4. The reading (the one contract)

Every reader produces, per provider:

```json
{"provider": "claude", "account": "",
 "windows": [{"name": "5h", "used_pct": 82.0, "resets_at": 1792110570, "minutes": 300},
             {"name": "7d", "used_pct": 54.0, "resets_at": 1792697370, "minutes": 10080}],
 "observed_at": 1792100000000, "source": "statusline", "unlimited": false, "unknown": false}
```

- `used_pct` 0–100 (a reader converts its source: Kimi's `used_ratio` 0–1 ×100, never twice).
- `name` is the reader's window name; `minutes` when known. Canonical names: `5h`, `7d`, `weekly`,
  `monthly`, `daily`, or a model window like `7d-opus`. busmon shows the tightest.
- `unlimited: true` (local model): no gauge, no policy. `unknown: true`: nothing usable.
- Times: `resets_at` Unix seconds, `observed_at` Unix ms.

## 5. Readers

`quota/` package: `type Reader interface { Provider() string; Read(ctx) (Reading, error) }`.
Readers are configured in `~/.config/agentbus/quota.toml` (built-ins enabled by default when their
CLI is on PATH; the file adds or overrides).

### 5.1 Built-in: Claude Code — `agentbus statusline`

Claude Code passes `rate_limits` on the stdin of its `statusLine` command for Pro/Max, after the
first API response ([docs](https://code.claude.com/docs/en/statusline)): `five_hour`,
`seven_day` (also `seven_day_opus`, `seven_day_sonnet`, `seven_day_overage_included`,
gateway `spend_limit`), each `used_percentage` (0–100, no conversion) and `resets_at` (Unix s);
any window may be absent.

`agentbus statusline [--then <command>]`, set as Claude Code's `statusLine`:
- Reads all of stdin into memory once. Parses only if ≤ 64 KiB; over that, it parses nothing and
  writes nothing, but still replays the original bytes to `--then`. If stdin cannot be kept, it
  exits 0 and writes nothing.
- Writes `~/.cache/agentbus/quota-claude.json` (0600, dir 0700, atomic rename, file lock).
  **Merge per window name:** an older `resets_at` never replaces; a later one replaces; the same
  `resets_at` with a *different* percent replaces (up or down) and sets `observed_at` to now; the
  same `resets_at` and percent is a no-op (keeps `observed_at`). A window absent from this stdin
  leaves the stored one untouched.
- Prints the status line: `--then` gets the same bytes on its stdin and its output is printed;
  without it, a short built-in line. Never fails the status line; no network, no bus.
- Install: `agentbus statusline --print-install` prints the `statusLine` JSON to paste (chaining
  the current command with `--then`). Nothing edits `~/.claude/settings.json`. The chained
  ccstatusline keeps calling its OAuth endpoint until the user removes it; the dispatcher never
  reads its cache.

Backstop (a limit already hit): Claude transcripts carry `quotaLimits` /
`apiErrorParams.rate_limit_info` with `status: "rejected"`, `rateLimitType`, `resetsAt`. The
reader looks only at transcripts modified in the last 6 h, keeps only those three fields, ignores
a `resetsAt` in the past, and turns a live rejection into that window at 100 % until `resetsAt`
— unless the status-line file holds the same window with a later `resets_at` or a newer
`observed_at`.

### 5.2 Built-in: Codex — `account/rateLimits/read`, JSONL fallback

Primary, documented ([docs](https://learn.chatgpt.com/docs/app-server#6-rate-limits-chatgpt)):
start `codex app-server` (fixed argv, no shell, stderr discarded), `initialize`, `initialized`,
`{"method":"account/rateLimits/read"}`; read `rateLimitsByLimitId`, else `rateLimits`:
`usedPercent`, `windowDurationMins`, `resetsAt`. Bounded to 10 s, process group killed, no model
turn. Window names from minutes (300 → `5h`, 10080 → `7d`, else `<n>m`).
Fallback: newest `event_msg`/`token_count` `payload.rate_limits.{primary,secondary}`
(`used_percent`, `window_minutes`, `resets_at`) across `$CODEX_HOME/sessions/**/*.jsonl`
(default `~/.codex`) modified in the last 7 days; `observed_at` = the outer `timestamp`;
truncated last line and missing windows tolerated. The server is labelled experimental and the
JSONL schema is not a contract: both fail to unknown.

### 5.3 Declarative: a file with fields — used for Grok

```toml
[providers.grok]
kind = "jsonl-last"                     # or "json"
path = "~/.grok/logs/unified.jsonl"
match = { "msg" = "billing: fetched credits config" }
observed_at = "ts"                      # RFC 3339 or Unix
[[providers.grok.windows]]
name_from = "ctx.config.currentPeriod.type"   # USAGE_PERIOD_TYPE_WEEKLY → weekly, MONTHLY → monthly
used_pct = "ctx.config.creditUsagePercent"    # float 0–100
resets_at = "ctx.config.currentPeriod.end"
```

`jsonl-last` reads from the end of the file until one complete matching line is found (bounded
to the last 8 MiB; a tail without a complete matching line is unknown — rotation is not known).
A missing field (Grok's free-tier shape has no percent) or an unknown period type is unknown. A
`ratio` option multiplies a 0–1 field by 100. Grok ships as a built-in config entry; it uses no
Grok-specific code. Its percent is published as is; whether it covers prepaid credit is unknown.

### 5.4 External: any command — the plug for every other AI

```toml
[providers.kimi]
kind = "command"
argv = ["/home/me/bin/kimi-quota"]      # fixed argv, no shell
every = "5m"                            # minimum interval, cached machine-wide
```

The command prints one JSON reading (§4) on stdout and exits 0; anything else is unknown. Bounded
to 10 s, process group killed, stderr discarded, its output parsed against the contract and
nothing else kept. This is how Kimi (its `/oauth/usage` with its own token, in the user's own
script), agy (its `/usage`, once verified), or any future AI plug in — the credential stays in the
user's script, never in agentbus. `kind = "unlimited"` declares a local model.

### 5.5 Collection

- The `dispatch` service collects in its own goroutine every 60 s. File readers are re-read right
  before a hold decision if older than 60 s; command and Codex readers are cached machine-wide
  (`~/.cache/agentbus/quota-<provider>.json`, lock, their `every` / 5 min TTL), so N projects share
  one call — and a "fresh" Codex reading can still be up to 5 minutes old.
- Published to `{p}:budget` (provider → JSON) through a **fenced** write (lease checked in the same
  script), additively: `windows`, `observed_at`, `collected_at`, `unknown`, `unlimited`, `source`.
  Legacy `session_pct`/`weekly_pct` and their resets are written only when a `5h` / `7d` window
  exists — never a 0 placeholder (until busmon §8 ships, the old single gauge is wrong for a
  provider with no 5 h window). Only providers used by an agent of the project are published.
- `agentbus refresh` keeps per-agent `{p}:usage` and stops reading ccstatusline.
- `agentbus quota [--json]`: every configured reader, read now, without the bus — "how much is
  left" for the operator and for agents.

## 6. Agent → provider

`rolecfg` already puts `AGENT_BUS_CLIENT` and `AGENT_BUS_PROVIDER` in every launched agent's
environment (clients `claude-code`, `codex`, `kimi-cli`). `bind` and `status` store both in
`AgentSnapshot` (`client`, `provider`), like pane and session, through `putSnapshot`.
`provider` is the key into the readings; a fixed client → provider table is only a fallback for a
client rolecfg does not name (e.g. `grok`, `agy` launched by hand). An empty provider is unknown:
the agent is shown, never held — including every agent bound before v0.13, until its next launch.

## 7. Policy (the dispatcher, deterministic)

### 7.1 Inputs

For each provider: its windows whose reading is known, observed within **`AGENT_BUS_QUOTA_MAX_AGE`
(default 30 min)**, and whose `resets_at` is in the future. Everything else is unknown for policy
(busmon still shows the aged number with `?`).

### 7.2 Warn

Per provider, **per window name and that window's `resets_at`**: at ≥ `AGENT_BUS_QUOTA_WARN` (85)
one directive to the escalation target — `Quota claude 86% (5h, resets 01:30): finish what is in
flight, start nothing new.` Recorded in `{p}:quota` (hash; field `warn:<provider>:<window>` =
`resets_at`), written by a fenced script, so the 5 h and the weekly warn are separate and each
fires once per window.

### 7.3 Hold

Each pass, a provider is **held** when any of its usable windows is ≥ `AGENT_BUS_QUOTA_HOLD` (95)
or a live backstop rejection (§5.1) applies. It is recomputed every pass — no sticky release rule:
when the tightest usable window drops below the hold threshold (a reset seen, a corrected reading),
it is no longer held. An unknown reading neither holds nor releases: a provider held on the last
pass with now-unknown readings stays held until the reset time recorded with the hold
(`hold:<provider>` = `resets_at` of the window that held, fenced), then is released.

A held provider's agents are not prompted and not escalated about: the check runs after
`HoldsDispatch` and before `Prompt`, and before `escalate`, independently of the pause (a pause
read error is fail-open in `paused()`; the hold is the gate that still runs). Their queued
directives wait; nothing is dropped. busmon shows them `held`.

Pace (used % per minute) is computed for display only (§8); it never warns, holds or releases.

### 7.4 Coordinator's provider: project pause

When the escalation target's provider is held, the agents of that provider are held (§7.3) **and**
the project is paused, so that its other agents are not woken with nobody to coordinate them —
the blast radius is the whole project, by design. The pause is written by a new fenced
compare-and-set, `FencedQuotaPause`: under the `{p}:dispatch` token, it writes `{p}:pause` (reason
`quota <provider>`, by `dispatch`, until = that window's reset + 2 min) only if no pause exists or
the existing one is a quota pause of the same provider; it never replaces a pause set by a person
or by master. At `until`, `FencedResume` ends it as today; if the provider is still held then, its
agents stay held by §7.3. An early end (a reading below the hold threshold) is a separate fenced
delete of that exact record (`FencedQuotaUnpause`). An escalation target with an empty provider
never triggers the pause.

### 7.5 Fencing and keys

Every write of this feature — `{p}:budget`, `{p}:quota`, the warn directive, the quota pause — goes
through a script that checks `GET {p}:dispatch == token`; a collector that lost the lease writes
nothing. `quota` joins `projectKinds` (next to `pause`). Warn, hold and pause stay per project; only
the readers' caches are machine-wide.

## 8. busmon

- Header: one gauge per published provider, tightest usable window first:
  `claude 5h 82% ~25m` · `codex 5h 96% held 03:10` · `grok wk 59%`; an unknown or aged reading is
  muted with `?`; an unlimited provider shows nothing. `~25m` is the pace estimate (display only),
  from the readings busmon itself has seen in the window. Replaces the single `anthropic used`
  gauge.
- Agents panel: flag `held` on agents of a held provider; the detail overlay names the provider,
  window, reset, source and observation age.
- The quota pause shows as any pause (`paused · resumes in 1h52 (quota claude)`).

## 9. Skills

- master: on a quota warning, finish and park in-flight work, dispatch nothing new; on a hold or a
  quota pause, nothing to do; never schedule its own resume.
- sentinel: the session-budget nudges move to the dispatcher; it keeps the context and all-quiet
  nudges.
- every agent: `agentbus quota` instead of guessing.

## 10. Tests

- Contract: each reader's output validated against §4; bad JSON, missing fields, non-numeric → unknown.
- Claude merge: older reset never replaces; same reset, lower percent replaces and can release;
  same reset and percent keeps `observed_at`; an omitted window keeps the stored one; parallel
  writers under the lock; stdin over 64 KiB → no parse, no write, bytes replayed to `--then`;
  `--then` gets the same bytes; the status line never fails.
- Backstop: a `resetsAt` in the past is ignored; a rejection does not outrank a fresher status-line
  window; only the three allowlisted fields are kept; only recent transcripts are scanned.
- Codex: RPC fixture (both shapes), JSONL fixtures (truncated line, missing window, a session file
  in an older day's directory), RPC failure → JSONL → unknown.
- Declarative (Grok fixtures): paid line, free-tier line (unknown), non-weekly period, period end in
  the past (unknown), no complete line in the tail (unknown), float percent.
- Command reader: valid output, garbage, timeout (killed), non-zero exit, `unlimited`.
- Policy: max age; reset in the past; warn once per window and reset (5 h and weekly separate);
  hold and release recomputed each pass; held-then-unknown stays held until the recorded reset;
  pace never acts; held agent not prompted or escalated, others are; coordinator's provider → quota
  pause; the quota pause refuses to replace a person's pause; a collector whose lease token no
  longer matches writes no budget, quota or pause; a provider with no 5 h window → no legacy
  `session_pct`.
- Dispatcher cases on the isolated test Redis (`AGENTBUS_TEST_REDIS_URL`) only, never `:6380`.
- busmon: gauges, `?`, `held` flags, across tiers and palettes (alignment suite).

## 11. Limits

- Sources move only while their CLI works; an idle provider ages into unknown for policy (§7.1),
  which can let a provider shared with another tool (Grok's pool with Cursor, any account used on
  another device) run out without a warning.
- One account per provider per machine: the readers do not tell two accounts apart.
- Codex's app-server is experimental; the declarative and command readers are only as stable as
  what they read.
- Claude's own auto-continue after a reset can resume an interactive session regardless of a hold.
- A hold stops new wakes, not a turn in progress; the warn comes first for that reason.

## 12. Rollout

`./setup.sh update`; `agentbus statusline --print-install` and paste it into Claude Code's
settings once per machine; optional readers in `~/.config/agentbus/quota.toml`; `agentbus up <p>`
again so agents are launched with their provider recorded; restart dispatch. ccstatusline can then
be removed by the user.

## 13. Changes from rev 1 (Grok review)

1. Merge: same reset + different percent replaces up or down; omitted window untouched (F1).
2. Backstop: past resets ignored, bounded scan, allowlisted fields, never outranks a fresher reading (F2).
3. Grok: rotation unknown, float percent, period type mapped not forced, past period end unknown,
   prepaid unmodeled, no raw line logged (F3).
4. Policy: hold/release recomputed each pass from the tightest usable window; warn keyed by window
   and reset; pace display-only; past resets unknown; pause blast radius stated, per-agent hold
   kept under a pause, fenced quota pause that never replaces another pause, early unpause separate (F4).
5. Every write fenced; `quota` in `projectKinds`; Codex TTL vs freshness stated (F5).
6. Legacy percents written only when the window exists (F6).
7. Stdin buffered and replayed; over-cap bytes still passed to `--then`; no settings editing (F7, F13).
8. Codex stderr discarded (F8). 9. Kimi unit (0–1) noted; section reference fixed (F9).
10. Numeric max observation age for policy (F10). 11. Provider from rolecfg's `AGENT_BUS_PROVIDER`,
    stored in the snapshot (F11). 12. Named leftovers in the secrets rule (F12).
13. Generic readers: declarative file readers and an external-command protocol (§5.3, §5.4).
