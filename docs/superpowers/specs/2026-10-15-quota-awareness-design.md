# v0.13 — Quota awareness: stop before the limit, per provider

Status: draft for review (Grok), 2026-10-15.
Research: `.superpowers/brainstorm/QUOTA-{codex,grok,kimi}.md` (local, not committed) — Codex,
Grok and Kimi each verified their own CLI and Claude Code on this machine.

## 1. Problem

Agent teams run until a provider refuses them: Claude Code prints `You've hit your session limit ·
resets 3:45pm`, Codex `less than 5% of your 5h limit left`, Kimi `403 You've reached your 5-hour
usage limit`. Work stops mid-turn, and the resume depends on someone noticing. Today the bus
knows Claude's windows only through `~/.cache/ccstatusline/usage.json`, a **third-party** status
line's cache, refreshed only when the sentinel wakes (22 h stale when last checked). Nobody reads
Codex's or Grok's numbers.

## 2. Goals and non-goals

Goals:
- Know, for each provider the team uses, the used % of every window (5 h, 7 d, weekly…), its reset
  time, and how old that observation is — from first-party sources, with **zero LLM tokens**.
- Warn the coordinator early, then stop waking the agents of a provider near its limit, until its
  reset; pause the project (v0.12.5) when the coordinator's own provider runs out.
- Show it in busmon: per-provider gauges, an estimate of the time left at the current pace, and
  which agents are held.
- No dependency on ccstatusline.

Non-goals (v0.13):
- Kimi and agy readers. Kimi's numbers are only behind `GET auth.kimi.com/oauth/usage` with the
  account's OAuth token (undocumented); agy's `/usage` headless form is unverified. Both are
  designed for (§4.5) but not built.
- Interrupting a turn in progress. A provider hold stops new wakes; an agent already working
  finishes its turn. The early warning (§6) exists so it can finish cleanly.
- API-key billing and API rate limits (RPS/TPM): a different product from subscription windows.
- Several accounts of one provider on one machine (§9).

## 3. Rules that hold everywhere

1. **Unknown is not 0 %.** A missing, malformed or stale observation is *unknown*: it is shown as
   such and never triggers or lifts a hold on its own.
2. **Observed ≠ collected.** Every reading keeps `observed_at` (when the provider's number was
   true, as close as the source allows) apart from `collected_at` (when we read it). Rewriting an
   unchanged number does not make it fresher.
3. **Shared windows are not added.** Agents on one account share one window: one gauge, never a
   sum of per-agent percentages.
4. **The tightest window decides.** A provider's state is the worst of its windows; the weekly
   window can stop a team whose 5 h window has headroom.
5. **No credentials.** No reader opens a token, cookie or credential file; nothing secret is
   logged, cached or passed in argv.

## 4. Sources

### 4.1 Claude Code — `agentbus statusline` (replaces ccstatusline)

Claude Code passes a documented JSON object on the stdin of the `statusLine` command, including
`rate_limits` for Pro/Max subscribers after the first API response of a session
([docs](https://code.claude.com/docs/en/statusline)):

```json
"rate_limits": {
  "five_hour": {"used_percentage": 23.5, "resets_at": 1738425600},
  "seven_day": {"used_percentage": 41.2, "resets_at": 1738857600}
}
```

Also seen in 2.1.301: `seven_day_opus`, `seven_day_sonnet`, `seven_day_overage_included`, and a
gateway `spend_limit` (`used_percentage`, `resets_at`, `used_usd`, `limit_usd`, `period`).
`used_percentage` is already 0–100 (do not convert again); `resets_at` is Unix seconds; a window
may be absent.

New command **`agentbus statusline [--then <command>]`**, set as Claude Code's `statusLine`:
- Reads stdin (bounded, 64 KiB), extracts `rate_limits` only (every window present, kept by name).
- Merges into `~/.cache/agentbus/quota-claude.json` (0600, directory 0700, atomic rename) under a
  file lock. **Merge rule per window:** a reading replaces the stored one if its `resets_at` is
  later, or equal with a higher-or-equal `used_percentage` (usage only grows within a window). So
  a stale session cannot overwrite a fresher one, and parallel sessions converge.
  `observed_at` = the time this process received a changed value; an unchanged value keeps the
  previous `observed_at`.
- Prints the status line: with `--then`, it runs that command with the same stdin and prints its
  output (to keep the user's current display); without, a short built-in line
  (`model · ctx 412k · 5h 23% · 7d 41%`). Never fails the status line: any error is swallowed
  after writing nothing.
- Must stay fast (it runs on every refresh, debounced 300 ms): no network, no bus access.
- `agentbus statusline --install [--then <current>]` writes `statusLine` into
  `~/.claude/settings.json` after a timestamped backup, chaining the existing command by default.
  Opt-in only: `setup.sh` prints the command, it never edits Claude's settings itself.

Freshness: the file moves only while a Claude session runs and receives responses. That is
enough — an idle provider consumes nothing — but a reading older than its window is *unknown*.

Backstop (limit already hit): transcripts `~/.claude/projects/*/*.jsonl` carry
`apiErrorParams.rate_limit_info` / `quotaLimits` with `status: "rejected"`, `rateLimitType`
(`five_hour`, `seven_day`) and `resetsAt` (Unix s). A rejected entry newer than the stored
reading sets that window to 100 % until `resetsAt`.

### 4.2 Codex — `account/rateLimits/read`, JSONL fallback

Primary: the documented app-server request
([docs](https://learn.chatgpt.com/docs/app-server#6-rate-limits-chatgpt)). The collector starts
`codex app-server` over stdio, sends `initialize`, `initialized`, then
`{"method":"account/rateLimits/read"}`, reads `rateLimitsByLimitId` (else `rateLimits`):
`usedPercent`, `windowDurationMins`, `resetsAt` (Unix s), and exits. Bounded to 10 s, killed
with its process group. No model turn; authentication stays Codex's own. At most once every
5 minutes per machine (§5), backing off on any error.

Fallback when the RPC fails or the app-server is unavailable: the newest
`event_msg`/`token_count` record across `$CODEX_HOME/sessions/**/*.jsonl` (default `~/.codex`)
modified in the last 7 days (an October 13 file can hold October 15 events), field
`payload.rate_limits.primary|secondary` (`used_percent`, `window_minutes`, `resets_at`).
`observed_at` = the record's outer `timestamp`. Tolerates a truncated last line and missing
windows. The schema is not a public contract: a parse failure is *unknown*, not an error loop.

### 4.3 Grok — the billing line of its log

`~/.grok/logs/unified.jsonl`, the newest line with message `billing: fetched credits config`:
`ctx.config.creditUsagePercent` (0–100), `ctx.config.currentPeriod.type`
(`USAGE_PERIOD_TYPE_WEEKLY`), `currentPeriod.end` (RFC 3339) → one window `weekly`.
`observed_at` = the line's timestamp. A line without `creditUsagePercent` (free tier shape) is
*unknown*. Read from the end of the file (it is not rotated and reaches several MB).

### 4.4 Not built: Kimi, agy

The reader interface (§5) takes them later: Kimi's `/oauth/usage` (`limit_5h`, `limit_7d`,
`limit_month_*`: `used_ratio`, reset) needs a decision on using its OAuth credential; agy needs
its headless `/usage` verified not to reach the model. Their reactive signals (Kimi's 403
`provider.auth_error` in `wire.jsonl`) can feed the hold in a later version.

## 5. Collection

- Package `quota/`: `type Reader interface { Provider() string; Read(ctx) (Reading, error) }`,
  readers `claude` (the statusline file + transcript backstop), `codex`, `grok`.
- `Reading{Provider, Windows []Window{Name, UsedPct float64, ResetsAt time.Time, Minutes int},
  ObservedAt, CollectedAt, Source, Unknown bool}`.
- **Who collects:** the `dispatch` service (`agentbus service run dispatch`), in its own goroutine,
  every 60 s, and right before a wake decision when the last reading is older than 60 s. Readers
  only read local files, except Codex's RPC, which is cached machine-wide in
  `~/.cache/agentbus/quota-codex.json` with a 5 min TTL, under a lock, so N projects share one
  call.
- **Published** to `{p}:budget` (existing hash, provider → JSON), extending `BudgetSnapshot`
  additively: `windows` (name, used_pct, resets_at, minutes), `observed_at`, `collected_at`,
  `unknown`. `session_pct`/`weekly_pct`/`*_reset` stay filled from the 5 h / weekly windows for
  existing readers. Only providers used by an agent of the project (§6) are published.
- `agentbus refresh` keeps per-agent usage (`{p}:usage` from transcripts) and stops reading
  ccstatusline; the sentinel no longer needs to refresh budgets.
- `agentbus quota [--json]` prints the readings (provider, window, %, resets in, observed ago,
  source) without the bus — the operator's and the agents' way to ask "how much is left".

## 6. Policy (the dispatcher, deterministic)

Each agent's provider comes from its client, recorded at launch: `agent-launch` already knows the
client (`rolecfg`); `bind` and `status` store `client` in `AgentSnapshot` (`claude-code`, `codex`,
`grok`, `kimi`, `agy`, empty = unknown). Mapping client → provider is a fixed table.

Per provider, from its tightest window:
- **Pace:** from the readings within the current window, used % per minute (least squares over the
  last 30 min, ≥ 3 points, else none) → `minutes_left` until 100 %, or unknown.
- **Warn** at ≥ 85 % or `minutes_left` < 30: one directive to the escalation target —
  `Quota claude 86% (5h, resets 01:30, ~25 min at this pace): finish what is in flight, start
  nothing new.` Once per provider and window reset (recorded in `{p}:quota`, a hash
  provider → `warned_reset`), fenced like every dispatcher write.
- **Hold** at ≥ 95 %, `minutes_left` < 10, or a limit-hit signal: the dispatcher does not prompt any
  agent of that provider until the window's reset + 2 min (escalations about them are deferred
  too). Recorded in `{p}:quota` (`held_until`), shown in busmon. If the **escalation target** is
  of that provider, the dispatcher sets the project pause (v0.12.5,
  `reason: "quota <provider>"`, `until` = reset + 2 min) instead, so the resume directive arrives
  by itself.
- **Release:** at `held_until`, or earlier when a newer reading of that window is below 85 % (a
  reset seen early).
- **Unknown** readings never warn, hold or release. An agent with an unknown client is never held.
- Thresholds: `AGENT_BUS_QUOTA_WARN` (85), `AGENT_BUS_QUOTA_HOLD` (95), recorded in the service env
  file like the escalation target.

Directives already queued for a held agent wait in its queue; nothing is dropped.

## 7. busmon

- Header: one gauge per published provider, tightest window first:
  `claude 5h 82% ~25m` · `codex 5h 96% held 03:10` · `grok wk 59%`; an unknown or stale reading is
  muted with `?`. Replaces today's single `anthropic used` gauge.
- Agents panel: flag `held` (attention) on agents of a held provider; the detail overlay names the
  provider, window, reset and source.
- The pause from §6 shows as any pause (`paused · resumes in 1h52 (quota claude)`).

## 8. Skills

- master: on a quota warning, finish and park in-flight work, dispatch nothing new; on a hold,
  nothing to do (the dispatcher holds the agents); never schedule its own resume.
- sentinel: Duty 2's session-budget nudges move to the dispatcher; the sentinel keeps the context
  nudge and the all-quiet nudge.
- every agent: `agentbus quota` answers "how much is left" without spending a turn on guessing.

## 9. Limits and open points

- Sources move only while their CLI works; an idle provider shows its last reading, aged.
- One account per provider per machine: the readers do not tell two Claude accounts apart (two
  `CLAUDE_CONFIG_DIR`s would share one file). Documented, not solved.
- Codex's app-server is labelled experimental; the JSONL fallback is version-sensitive. Both fail to
  *unknown*.
- Claude's own auto-continue after a reset can resume an interactive session regardless of a
  hold; the hold only governs wakes.
- A 60 s collection cannot stop an agent mid-turn: thresholds keep a margin, and the warning
  comes first.

## 10. Tests

- Fixtures per source (numbers only): statusline stdin with/without `rate_limits`, partial
  windows, `spend_limit`; Codex RPC response and JSONL records (truncated line, missing windows,
  resumed-session file in an older directory); Grok billing lines (paid and free shapes).
- Merge rule: an older-reset or lower-percentage reading never replaces a newer one; parallel
  writers under the lock.
- Pace and `minutes_left`; warn once per window; hold, release on reset and on an early lower
  reading; escalation target held → project pause; unknown never acts.
- Dispatcher integration on the test Redis with fake readings: held agent not prompted, others
  are; pause set for the coordinator's provider.
- busmon header and flags across tiers and palettes (alignment suite).
- `agentbus statusline`: never fails the status line, writes nothing on bad input, `--then` passes
  stdin through; `--install` backs up and chains.

## 11. Rollout

`./setup.sh update`; `agentbus statusline --install` once per machine (it keeps the current status
line through `--then`); `agentbus up <p>` again so `agent-launch` records each agent's client
(an agent without one is shown, never held); restart dispatch. ccstatusline can then be removed
by the user.
