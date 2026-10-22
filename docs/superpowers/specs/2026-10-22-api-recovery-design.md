# Recovering from API errors without the person — design

Status: draft for review (coder, 2026-10-22), revision 2 after foureyes' review of `b4cb92e`. Task `api-recovery-spec`, branch
`docs/api-recovery-spec` from `origin/main` `61031f7`. Spec only; no code in this change.

## The incident

In jenkins-owasp, Bernard saw the master pane stuck three times on `Request timed out`, which means
the Claude API could not be reached. Each time:

1. The dispatcher typed its wake line (`[agentbus#…] … N message(s) waiting … subscribe --drain
   master`).
2. The turn ended on `Request timed out`.
3. Nothing recovered until he stepped in.

That project is not on this host's broker and no local transcript holds a timeout. The account
below is therefore read from the code and from Claude Code's documentation, not from that pane.
Capturing its screen is the first step of the implementation (see "Fixtures").

## How it is handled today

**The wake rules.** `dispatch/decide.go` `Decide`, rules 1–9; `dispatch/loop.go` defaults
`Retry` 60 s, `EscalateAfter` T = 10 min, `MaxTries` 3.

- **Each wake is a try.** `loop.prompt` reserves the wake record with `Tries+1`. A try is undone
  only by *progress*: the group's last-delivered id advanced, or its pending count fell
  (`Progressed`). An agent whose turn died on an API error drains nothing, so the try stands.
- **Retries are spaced by `Retry`.** Rule 5 (`within retry window`) defers the next prompt by
  60 s. The pane, back at an empty prompt under the error, is *ready* again for the client
  signature (`Signatures["claude"].Ready` matches the empty `❯` box). Rule 9 therefore prompts
  again.
- **After 3 tries, rule 4 escalates** (`tries exhausted without progress`): that is the "3 wakes
  per backlog". The stuck agent here *is* the escalation target (master), so `recipientCannotAct`
  turns the escalation into one `notify` for the person (`FencedNotifyForPerson`, busmon marks it
  unread). No directive is sent.
- **Then nothing.** The record is `escalated=true` with no `EscCause`: rule 4 is not a pane cause
  (only rules 7–8 record one, `paneCauses`). Rule 3 waits for *progress*, and progress never comes
  while the API is down and nobody prompts. When the API comes back, nothing wakes master: the
  next wake comes only from a *new* entry that progress would reveal, or from the person.
- **During the client's own retries the pane reads `working`.** herdr's status and the claude busy
  signature (the spinner line, `Busy: (?m)^[ \t]*[·✢✳✶✻✽*][ \t]+\S+…`) both say so. Rule 8
  (`agent working`) waits 4×T = 40 min, then escalates `agent working` with `EscCause`. This
  escalation *is* re-armed once the pane is ready again, which is the first thing it is after an
  error.

**What the dispatcher sees of a pane.** `Herdr.Inspect` reads the last 40 lines twice, 200 ms apart
(`pane read --source recent --lines 40`, with `--ansi` for claude and codex). From them it keeps
only `Ready`, `Busy` and an opaque `ScreenHash`: *the text itself is never retained*. Nothing in a
signature recognises an error, so a screen ending in an error above an empty input box is simply
`idle`, `Ready`. The observe pass (`observe.go`, every 15 s) publishes the same facts to
`{p}:observed`, which holds no error class.

**Where errors are already read.** The quota reader (`quota/claude.go`) reads Claude transcripts:

- `transcriptRejections` turns a `quotaLimits.status=rejected` entry into a held window.
- `servedAt` gives the time of the newest real answer: an assistant turn that is not
  `isApiErrorMessage` and not model `<synthetic>`.

Local transcripts show what an error turn carries (`isApiErrorMessage: true`, `error`,
`apiErrorStatus`, `apiErrorParams`, `errorDetails`). The `error` value is `rate_limit` (429,
`You've hit your session limit · resets …`) or `authentication_failed` (`Login expired · Please run
/login`). No timeout or 5xx turn exists locally; whether Claude Code writes one, and with which
`error` value, is to be checked at the next occurrence (see "Fixtures").

## What the clients already do (first line of defence; from their documentation)

**Claude Code** (installed 2.1.306; code.claude.com/docs/en/errors and /env-vars):

- **It retries transient failures before showing an error:** up to `CLAUDE_CODE_MAX_RETRIES`
  times (default 10, capped at 15) with exponential backoff. That covers server errors,
  overloaded (529), request timeouts before any output, dropped connections and temporary 429
  throttles. `Request timed out` is therefore already the *end* of those retries. Each attempt is
  bounded by `API_TIMEOUT_MS` (default 600000).
- **`CLAUDE_CODE_RETRY_WATCHDOG=1`** (v2.1.186+) is documented for "unattended sessions such as
  eval harnesses, CI jobs, or remote workers".
  - It retries 429 and 529 indefinitely, backing off up to 5 min between attempts, or until the
    limit resets.
  - On v2.1.199+ it raises the default retries for other transient errors (server errors,
    timeouts, dropped connections) to 300, "roughly three hours of backoff".
  - It still fails at once on a spend-limit or exhausted-credits 429.
  - `CLAUDE_CODE_RETRY_WATCHDOG_MAX_WAIT_MS` (v2.1.295+) bounds the time each request waits out
    429/529 under the watchdog.
- **The final messages are documented**, and they tell a down API from a quota apart:
  - API down: `Request timed out`, `API Error: 500 …` (any 5xx, `502 Bad Gateway`…),
    `API Error: Repeated 529 Overloaded errors`, `API Error: No response from API`,
    `Connection lost before a response was produced`.
  - Usage limits: `You've hit your session|weekly|Opus|Sonnet limit`, `Request rejected (429)`,
    `Server is temporarily limiting requests`, spend limits.
  - Authentication: `Not logged in · Please run /login`, `Claude.ai login expired`, …

**Codex** (installed codex-cli 0.163.0; `codex-rs/model-provider-info`): each model provider has
`request_max_retries` (default 4, capped by `MAX_REQUEST_MAX_RETRIES`), `stream_max_retries` (a
dropped stream) and `stream_idle_timeout_ms` (300000). They are set under `[model_providers.<id>]`,
or on the command line as `-c model_providers.openai.request_max_retries=N`. The cap values and
the default of `stream_max_retries` are read from the code at implementation time, not assumed
here.

**Kimi, Grok, agy:** no documented setting was checked. They keep today's behaviour, and their
error lines are captured as fixtures before any class is added for them.

## Options

| | Option | Verdict |
|---|---|---|
| A | Client settings only (the watchdog env for Claude, retries for Codex) | Necessary, not sufficient: a long outage still ends the turn, and the dispatcher still burns its 3 tries and goes silent |
| B | A plus the dispatcher classifies an API error, does not count it as a try, retries the **same wake line** with backoff and a cap, then one escalation and a notify per provider outage, and re-arms when the provider answers again | **Recommended** |
| C | The dispatcher types `try again` or replays the agent's last message | No: the only sanctioned input is the fixed wake line (decision 2026-10-10); message content is never typed |
| D | Relaunch the agent on an API error | No: it does not bring the API back, loses the session context, and relaunch never types into an existing pane anyway (v0.21) |

## Recommended design (B)

### 1. The first line: the launcher sets the clients' own retries

`rolecfg` launch defaults, overridable per profile and announced on stderr like other options:

- **claude-code:** `CLAUDE_CODE_RETRY_WATCHDOG=1` and `CLAUDE_CODE_RETRY_WATCHDOG_MAX_WAIT_MS=3600000`
  (one hour per request on 429/529), unless the environment or the profile already sets them.
  Every agent is an unattended session in the documentation's sense.
- **codex:** raise `request_max_retries` and `stream_max_retries` for the profile's provider.
  **Unverified:** whether `-c model_providers.openai.request_max_retries=N` patches the *built-in*
  provider or defines a partial new one (no `base_url`, no `name`). The implementation verifies it
  first, against codex's own config code and a dry run showing the effective provider. If it is not
  honoured as an override, Codex keeps its defaults: no partial provider is written, and no copy of
  the user's config. The dispatcher layer below covers it either way. The caps are read from the
  code at that time.
- A profile option `client_retries = false` turns this off (for a person who wants failures to
  surface at once).
- A long client retry keeps the pane `working`: section 3 keeps that from escalating as a stall.

### 2. Classify the error, never keep its text

A class has an **evidence level**, and only the strong one may act beyond its own agent.

- **`transcript` (strong; Claude Code, when the snapshot has a `Session`).** The newest entry of
  the agent's own transcript with `isApiErrorMessage: true`, newer than the wake record's `At`,
  gives the class from its `error` field: `rate_limit` → `limit`, `authentication_failed` →
  `auth`. Values seen at the next timeout or 5xx are added from the fixture. Only `type`,
  `timestamp`, `isApiErrorMessage`, `error` and `apiErrorStatus` are read, as in `servedAt`; no
  credential file is opened. The client writes this entry itself: an agent cannot forge it by
  printing a string.
- **`screen` (weak).** The turn tail can be the agent's own output: a printed fixture, a test log,
  a report quoting an incident. So:
  - For Claude Code, a screen class **never counts by itself**. Without a transcript entry to
    match, it reads as today: no class.
  - For a client without a transcript reader, a screen class may change only **that agent's own
    backoff** (section 3). It never opens a provider outage on its own (section 4).
  - Screen regexes are **anchored to the client's own error rendering**, never to bare words: the
    marker and ANSI colour the client prints its errors with (for Claude Code, the turn's result
    prefix and its error colour), read from the captured fixtures. The same words in plain agent
    output do not match.

`Signature` gains `APIErrors []ErrSig` (`Class`, `Re`), matched on the raw (ANSI) **turn tail**:
at most 8 non-empty lines directly above the current input region (`inputRegion`'s start).

- **The turn tail only.** An error further up the scrollback, above a later turn, is never read.
  A new turn pushes its own output into the tail.
- **One class, no text.** `Inspection` gains `APIError string` (the class only),
  `APIEvidence` (`transcript` or `screen`) and `APIRetrying bool` (the client's own retry label
  while it waits, anchored the same way). The screen text is still never retained or published.

| Class | Claude Code wording (documented) | Meaning |
|---|---|---|
| `timeout` | `Request timed out`, `API Error: No response from API` | API down or unreachable |
| `overloaded` | `Repeated 529 Overloaded errors`, `… is experiencing high load`, `Server is temporarily limiting requests` | capacity, not a quota |
| `server` | `API Error: 5xx …`, `502 Bad Gateway` | API down |
| `network` | `Connection lost before a response was produced`, `The response stalled before a response was produced` | transport |
| `limit` | `You've hit your … limit`, spend limits, `Request rejected (429)` | quota: the quota system's case |
| `auth` | `Not logged in · Please run /login`, `… login expired` | only the person can fix it |

The first four are **transient**. The regexes are written from captured fixtures (section 6), with
the documented wording as their starting point; Codex's classes are added from its fixtures only.

### 3. Decide: an API error is not a try

New rows, between rule 3 and rule 4, evaluated on an inspection. The record gains `api_tries`,
`api_class`, `api_evidence` and `api_at`.

**The cost is bounded.** The loop inspects before `Decide` only for an agent that is owed
something, whose record shows a prompt was made (`state` `prompted`/`unobserved`, `Tries > 0`),
and only once `SinceWake ≥ Retry`, i.e. when rule 5 would let a prompt through anyway. That is at
most one extra inspection per such agent per `Retry` (60 s). One inspection is 4 herdr calls
(agent get, process-info, two reads 200 ms apart), each bounded by `herdrCallTimeout` (3 s). An
observation from the observe pass younger than 15 s, with the same pid, stands in for it.

| Rule | When | Action |
|---|---|---|
| 3a | `APIRetrying`, or herdr `working` with the client's retry label | defer `api retrying (client)`; no escalation by rule 8's 4×T. After `APIStall` (3 h, the watchdog's own horizon) the normal rule 8 applies |
| 3b | `APIError` transient (claude: `transcript` evidence; others: `screen`), pane verified, `api_tries < APIMaxTries` (6) | prompt with the **same wake line** once `SinceWake ≥ backoff(api_tries)`: 1, 2, 4, 8, 16, 30 min, about an hour in total. Before that, defer `api <class>, backing off`. `Tries` is **not** incremented; `api_tries` is |
| 3c | `APIError` transient and `api_tries ≥ APIMaxTries` | escalate once, `EscCause = "api <class>"`; record it as a candidate for the provider outage, which opens only on the evidence of section 4 |
| 3d | `APIError` = `limit` | defer `usage limit`, no try counted, no escalation: the quota reader holds the provider (`dispatch/quota.go`). If no hold appears within T, escalate `api limit` as today |
| 3e | `APIError` = `auth` | escalate at once, as a `notify` for the person naming the pane (`run /login there`); no retry. A screen-only `auth` misread costs one notify, nothing else |

- **Every prompt keeps the present guards.** Ready, unfocused, pid-verified (`identityOK`), the
  reservation (`ReserveWake`), the swap intent and the pause. Rule 3b only changes *when* and
  *whether it counts*, never what is typed.
- **Progress resets the API state**, like `Tries` (`ResetTries` clears `api_*` too).
- **Re-arming:** an `api <class>` escalation is re-armed when the provider has answered since it
  was raised (section 4), not merely when the pane is ready. A ready pane under an error is the
  very state that must not re-arm it.

### 4. A provider outage: one episode, no hammering

An outage holds back every agent of a provider, so it opens only on evidence an agent's own output
cannot fake. An agent at rule 3c opens one when either:

- its class has `transcript` evidence; or
- a **second** agent of the same provider reached rule 3b or 3c with its own class within the
  last 15 min (two panes printing the same quote at the same time is not a case this guards
  against).

A single screen-only class affects that agent alone: its backoff, its escalation at the cap. The
outage is a fenced record in `{p}:quota` under `apidown:<provider>`: class, evidence, since
(Redis `TIME`) and episode.

- **While it is open, nobody hammers the provider.**
  - No agent of that provider is prompted, except one **probe**: the oldest owed agent, at most
    every 15 min.
  - No rule 4 or rule 8 escalation is raised for those agents.
  - busmon shows `api down <provider>` in the header, as it shows `held`.
- **One message per episode.**
  - One directive to the escalation target (`FencedCmdOnce`, mark `apidown:<p>:<episode>`).
  - When the target is on the same provider, or is the stuck agent, one `notify` for the person
    instead (`FencedNotifyOnceForPerson`), with the class and the provider's status page.
- **It closes when the provider has answered since `since`.** Evidence of an answer:
  - for Claude, `Reading.AvailableAt`/`servedAt` from any of its agents' transcripts (already
    collected every minute);
  - for any client, a progress on any agent of that provider;
  - or a probe whose turn ended without a classified error.

  On close, the escalated agents are re-armed (rule 3), and the target gets one `wake=no` notice,
  as quota releases do (`resumed:` marks, once per episode).
- **Health check `api-errors`** (`health/`):
  - `warn` while an agent shows a transient class, while a client has been retrying for more than
    T (`APIRetrying`, rule 3a: a 3-hour silent retry is visible here), or while an outage is open;
  - `ok` otherwise;
  - `unknown` when no observation is fresh.

  It never goes to `fail`: a `fail` wakes sentinel, which most likely runs on the same provider.
  The person's notify is the channel. busmon F5 lists it with its static remediation ("check the
  provider's status page; nothing to restart").

### 5. A down API is not a quota hold

| | Quota hold (`dispatch/quota.go`) | API outage (this spec) |
|---|---|---|
| Evidence | a window ≥ threshold, or `quotaLimits.status=rejected` with a reset | a transient class (timeout, 5xx, 529, connection) with transcript evidence, or from two agents of the provider (section 4) |
| Ends | the window reads under threshold, is renewed, or its reset passes; or the API answered since (v0.14.6) | the provider answered since `since`; a successful probe; progress |
| Prompts meanwhile | none | one probe per 15 min |
| A 429 throttle | only when it carries the quota headers or a reset (a limit) | `Server is temporarily limiting requests` reads `overloaded` (transient) |

A 529 is explicitly "not your usage limit" (Claude Code's documentation), so it never opens a
hold. A `limit` class never opens an outage.

### 6. Fixtures first

`dispatch/testdata/screens/` gains captured screens, each with a README line naming the client
version and the date:

- `claude-api-timeout.txt`, `claude-api-529.txt`, `claude-api-500.txt`,
  `claude-api-connection.txt`, `claude-limit.txt`, `claude-auth.txt`;
- `claude-retrying.txt`: the watchdog's retry label during a turn;
- `claude-old-error-new-turn.txt`: an error above a later successful turn;
- `claude-quoted-error.txt`: the same words as normal agent output (a printed fixture, a report)
  just above the prompt;
- `codex-stream-error.txt`, `codex-request-error.txt`.

Until a class has a captured fixture, it is not shipped: no signature is written from the
documentation alone. The jenkins-owasp pane is the first capture to ask for: master or the person
runs `herdr pane read <pane> --source recent --lines 40` there at the next occurrence.

## Test plan (fake herdr, test broker only)

- **Signatures:** each fixture gives its class. An empty or typed input box with no error gives
  none. An error above a later turn gives none. A modal still vetoes `Ready`. Classification
  never changes `Ready`/`Busy`.
- **Misreads:** the error words inside normal agent output (`claude-quoted-error.txt`, a printed
  fixture, a test log) give no class. A Claude screen class with no matching transcript entry
  counts no API try. One agent's screen-only class (a client without a transcript) changes only
  its own backoff and opens no outage; a second agent's class within 15 min does open one.
- **Transcript class:** fixture JSONL with `isApiErrorMessage` entries. A class only from entries
  newer than `At`; other fields never read (a field filled with a secret-like value never reaches
  the output).
- **`Decide` table:** each new row (3a–3e) and its order against rules 3–9. A transient error
  never increments `Tries`. The backoff schedule from `SinceWake`. The cap leads to one
  escalation with cause `api <class>`, re-armed only by a "served since" input. `limit` defers
  without a try; `auth` escalates at once to the person.
- **Loop, fake herdr (the jenkins case):** an owed master whose pane shows the timeout screen
  after each of 3 prompts, then a clean screen on the 4th. It is prompted 4 times on the backoff
  schedule, never escalated, and the record ends cleared by progress. The same with the error
  persisting: exactly one notify for the person (master is the target), no directive, `api_tries`
  stops at the cap, and nothing is typed after it except one probe per 15 min.
- **Outage:** a transcript-evidenced class at the cap opens it alone. Two agents on one provider,
  both erroring. One episode, one message, no prompt to
  the second while it is open. It closes on a served-since reading, re-arms both, and sends one
  `wake=no` notice. A second outage is a new episode.
- **Safety invariants kept:**
  - every prompt still goes through `ReserveWake` and `identityOK`, and no text but `WakeText` is
    ever passed to `Prompt`;
  - a focused pane is never prompted;
  - a paused project prompts nobody;
  - every write is fenced (a lost lease writes nothing).
- **Launcher:** the claude env defaults are set only when absent; `client_retries = false` drops
  them; codex gets the `-c` overrides for the profile's provider; dry-run output shows them.
- **busmon:** the `api down <provider>` header item; the health row, including a long `APIRetrying`.
- **Inspection cost:** the fake herdr counts calls. No extra inspection inside the retry window,
  and none for an agent owed nothing; a fresh observation stands in for one.

## Open questions for review

Agreed in review: 1 (Config plus flags through the env file), 2 (no) and 3 (keep).

1. **The probe cadence and cap** (6 retries up to 30 min apart, then one probe per 15 min while
   the outage stays open). Proposal: as stated, as `Config` fields with `agentbus dispatch` flags
   (`--api-max-tries`, `--api-probe-every`), set per project through the services' env file like
   `--escalate-to`.
2. **Should rule 3b also apply when no wake of ours preceded the error** (the agent's own turn
   failed)? Proposal: no. The dispatcher retries only what it typed. An agent whose own work failed
   on the API is the person's or master's call, shown by the health warn.
3. **`CLAUDE_CODE_RETRY_WATCHDOG` makes a usage-limit turn wait until its reset inside the
   client.** The quota hold already stops prompts meanwhile, so this is harmless, but the
   resume directive then arrives to an agent that resumed by itself. Proposal: keep it; the
   directive is idempotent.

Changes from revision 1 (foureyes, review of `b4cb92e`):

- **Evidence levels.** For Claude Code, a transcript entry is required to count an API try and
  to open an outage. A screen-only class changes only its own agent's backoff, unless a second
  agent of the provider shows a class too.
- **Anchored regexes.** Screen regexes are anchored to the client's own error rendering, with
  tests where the words appear in normal output.
- **Health warn** now covers a long client retry (`APIRetrying`).
- **Codex's built-in provider override** is marked unverified, with a fallback.
- **Inspection cost** is stated and throttled to the retry window.
