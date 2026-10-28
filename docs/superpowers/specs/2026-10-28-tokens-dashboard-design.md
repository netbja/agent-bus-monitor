# Tokens dashboard: busmon `U` and `agentbus tokens` — design

Status: draft for review (coder, 2026-10-27). Task `tokens-spec`, branch `docs/tokens-spec`, review
subject `tokens-spec`. Spec only; no code in this change.

## What Bernard decided

Ask `1793108028807-0` (2026-10-27), relayed by master (`1793135892379-0`):

- **Both** a busmon panel (key `U`) and a text CLI, `agentbus tokens`.
- **All projects of the broker side by side**, the open one first: the account window is shared,
  and the question is "which project eats my quota".
- **Per project, and per agent** when a project is opened.
- **No cost in dollars** (the standing rule: a price table rots, and on a subscription the figure
  is fiction).
- Later feedback: he saw no graphs; he wants **simple small charts**, and the animation ideas
  (a shimmer or skeleton on a gauge whose reading is pending, a pulse on a working agent), **honest**:
  animate only what is really pending or working, ~10 redraws a second only while something
  animates, true colour only, like the `waiting` fade.

## What is known today, measured (read-only, 2026-10-27)

- **Claude Code transcripts** (`~/.claude/projects/<dir>/<session>.jsonl`, and a session's
  subagents under `<session>/subagents/`): every assistant line carries `message.usage`
  (`input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `output_tokens`), a
  `timestamp`, `sessionId`, `message.id` and `requestId`. **The same request's usage repeats on
  several lines** (one per content block): over the last 7 days, 8,181 of them. Counting without
  deduplicating by `(message.id, requestId)` multiplies the totals. This is the one trap.
- **Codex session logs** (`$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl`): `event_msg` of type
  `token_count` with `info.total_token_usage` (cumulative: `input_tokens`, `cached_input_tokens`,
  `output_tokens`, `reasoning_output_tokens`) and `last_token_usage`; the first line,
  `session_meta`, gives the session `id` and `cwd`. It also carries account identifiers
  (`creator_account_id`, `creator_user_id`): never read into any structure, never stored.
- **Kimi, agy, Grok**: their logs give quota hits, not token counts (`quota/kimi.go`,
  `quota/agy.go`). Their rows read `?`, never 0.
- **The account windows** are already collected each minute by the dispatch service
  (`{p}:budget`, `quota/`), but only the latest reading is kept: no history exists for a chart.
- **Today's numbers** for agent-bus-monitor (deduplicated): 5.1 M input + cache-write tokens,
  1.4 M output, 884 M cache reads. By proved session: foureyes 1.2 M / 206 k, master 744 k / 254 k,
  coder 345 k / 83 k; **2.9 M / 873 k belong to sessions no agent proves any more** (sessions
  before a `/clear` or a relaunch). Per day, 21 → 27 Oct: 3.6 M, 2.5 M, 1.7 M, 4.0 M, 0, 5.3 M,
  6.5 M (input + cache writes + output). hyperliquid: 0.17 M and 0.32 M on 21 and 22 Oct.

## Sources and attribution

- **A transcript maps to a project and an agent only through a proved session.** The proof is
  `AgentSnapshot.IdentSession` (set only with an identity proof: a status with a pid, or a bind),
  never `Session` (an unverified status may write it; reading it would let any process point
  busmon or the dispatcher at another session's file: the known security follow-up of
  `2026-10-27-context-fill.md`).
- **The session record, `{p}:sessions`** (new): because `IdentSession` holds only the *current*
  session, today's 2.9 M would stay unattributed forever. Each pass, the dispatch service adds the
  current `IdentSession` of every agent: field `<session>` → `{agent, provider, client, first,
  last}` (`first`/`last` from Redis `TIME`), fenced, pruned of sessions whose `last` is older than
  8 days, capped at 512 fields (oldest `last` dropped first). Sessions before the record started
  stay **unattributed**, and say so; the record is never back-filled from `cwd` or directory names,
  which prove nothing.
- **"Not attributed" is a cross-project fact, so no project's dispatcher computes it.** It means
  the tokens of sessions no project's record names; one project's dispatcher knows only its own
  record, and N dispatchers scanning every transcript would each count the others' sessions (N
  copies). Until the host dispatcher exists (decided, not built: `2026-10-26-host-dispatcher.md`),
  its only owner is **the person's own command**: `agentbus tokens --unattributed` reads every
  project's `{p}:sessions`, scans the host's transcripts itself (read-only, the same dedup, a
  budget of 1 GiB, `partial` beyond), and prints the remainder per provider, never assigned by
  transcript directory. busmon shows no such row; the `U` page says `not attributed: agentbus
  tokens --unattributed` in its footer. When the host dispatcher lands, it writes one host-wide
  total and both readers subtract the projects' attributed sums.
- **Codex** (follow-up): Codex agents have no proved session today (no `CLAUDE_CODE_SESSION_ID`;
  the shared app-server's environment is not theirs). Until the launcher records the rollout id it
  started (`AGENT_BUS_CODEX_SESSION`, bound like the pid), Codex tokens belong to no project: they appear
  only in `agentbus tokens --unattributed`, and a Codex agent's row reads `?` with the reason.
- **No credential is opened**: transcripts and session logs only, read as the same user, as the
  quota readers already do.

## The history store (written by the dispatch service, fenced)

| Key | Content | Writer, cadence | Size |
|---|---|---|---|
| `{p}:sessions` | session → agent, provider, client, first, last | dispatch, every pass that sees a proved session | ≤ 512 fields, 8 days |
| `{p}:tokens` | `<day>\|<agent>\|<provider>` → `{in, write, read, out, n}` (day in the host's local date; `in` = input, `write` = cache creation, `read` = cache read, `n` = requests) | dispatch, every 5 min, **replaced whole** from the in-memory sums (below) | 8 days × agents × providers: ≈ 100 fields |
| `{p}:quota:hist` | stream: `provider window pct at` | dispatch, when a window's reading changes or every 15 min | `MAXLEN ~ 2000` (3 providers × 2 windows × 24 h at 5 min fits) |

- **Incremental in memory, stateless across restarts.** Measured: transcripts modified within 8
  days weigh 269 MB for this project alone and 592 MB on the host, one file 84 MB; re-reading them
  every 5 minutes is out. The dispatch service keeps an **in-memory** map `(path, size, mtime) →
  (offset, per-day sums, the ids of the request still open at the offset)`. Transcripts are
  append-only, so each 5-minute pass reads only the bytes appended since the offset, up to the last
  complete line. A file that shrank, or changed identity (inode), is rescanned from 0. Nothing of
  this is persisted: after a restart the first passes rescan from 0, bounded per pass (64 files,
  128 MiB, newest first) and resuming from their offsets on the next pass, and `{p}:tokens`
  carries `partial: true` (busmon shows `~` before the numbers) until every file has been read
  once. Only the session files of `{p}:sessions` (plus their `subagents/`) whose mtime is within 8
  days are read. Each pass replaces `{p}:tokens` whole, from the in-memory sums, in one fenced
  script; a crash between passes writes nothing.
- **Paths are built only from valid ids.** Each session id from `{p}:sessions` passes
  `sessionIDPattern` before it is joined into a transcript path (the record is fenced, but the
  join does not trust it); a non-matching id is skipped and logged.
- **Codex**: per rollout file, in file order, each `token_count` event (`info` not null) adds
  `total_token_usage − the previous event's total_token_usage in the same file` to the day of its
  own `timestamp` (local time). The baseline is per file, never per day: a session that runs past
  midnight counts its first event after midnight against the last one before it, and a day with a
  single event counts that event's delta, not zero. The file's first event adds its
  `last_token_usage` (equal to the total for a new session; for a resumed rollout whose total
  carries earlier turns, only this turn, the earlier ones being in their own file). A total lower
  than the previous one (a counter reset) adds that event's `last_token_usage`. A repeated event
  (same total, seen on real rollouts) adds 0, which is why the per-event `last_token_usage` is not
  summed. Cumulative counters still survive a missed event in between. The reader keeps each
  file's last total with its offset, so an incremental pass needs no re-read; a file seen for the
  first time is read from its start (inside the pass budget) so its baseline is exact.
- **Not in `projectKinds`**, and ignored by `HasKeysBesidesDispatch`: none of these keys discovers
  or revives a project (as `{p}:context`, `{p}:observed`).
- **The account history is per project** (each project's dispatcher writes its own copy of the same
  account readings). busmon reads the open project's; a project without a dispatcher has no chart,
  and its windows show the latest `{p}:budget` reading only. One host dispatcher (decided, not built,
  `2026-10-26-host-dispatcher.md`) would remove the duplication later.

## What busmon shows (key `U`)

A reader page over the lower zone, like `P`, three sections:

1. **Account windows**, one line per provider: the existing gauge (`▇`/`░`), the percent, a
   **24-hour sparkline** of the window's percent from `{p}:quota:hist` (one cell per hour, the
   hour's last sample), the reset or the hold.
2. **Projects side by side**: every project `bus.Projects` lists, the open one first, then by
   today's tokens. Columns: today in (input + cache writes), today out (at 80 columns one `in+out` column), cache reads (wide only), a
   **7-day bar chart** (one cell per day, scaled to the largest day of all projects shown, so bars
   compare across rows), the 7-day total, and the project's share of today's tokens per provider (a
   bar at ≥160 columns). Projects without agents fold into one line.
3. **The open project by agent, today**: agent, provider, in, out, and the reason for `?`. A footer names
   `agentbus tokens --unattributed` for what no project claims. Enter on an agent opens its 7-day chart and its sessions.

**Sparklines** need the eight lower blocks; the closed glyph set has `▇` and `█`. The six others,
`▁▂▃▄▅▆` (U+2581–2586), join the set: single-cell, the same Unicode block as `▇`/`█`, checked by
`TestGlyphsAreSingleCell` and the alignment suite. A day with no tokens is a space, a day with
unknown tokens `?`.

**Animation (honest):**
- **Skeleton** on a gauge or a chart whose first reading is pending: the dispatcher is up, the key
  is absent, and busmon opened less than 2 minutes ago (after that it reads `?`, not a shimmer).
  The cells shimmer between two muted tones.
- **Pulse** on a working agent's `●`: the agent declared `working` **and** the observation sees its
  pane busy (both facts; one alone does not pulse).
- Driven by the existing fade schedule (`fadeMsg`, `fadeLevel`): ~10 redraws a second **only while
  something animates**, true colour only (the 16-colour and none palettes draw the still frame).
  `View` stays a pure function of the model and the stamp.

**Pending vs unknown:** pending shimmers only within its 2 minutes; unknown is `?` with a reason;
zero is `0`. They are never the same glyph.

## `agentbus tokens`

```
agentbus tokens [--project <p> | --all] [--days N] [--by agent|day] [--json]
```

Read-only; `--all` (the default when no project is given) needs only the broker, so the block sits
above the project-required exit, like `projects`. Output, today's data:

```
$ agentbus tokens
account   anthropic 5h 46% (resets 2h49) · 7d 8%   openai 5h 98% (resets 3h30) · 7d 15%   moonshot 7d 100% (held until Tue 03/11 20:41)

PROJECT             TODAY IN  TODAY OUT  CACHE READ  7 DAYS    7D TOTAL
agent-bus-monitor       5.1M       1.4M        884M  ▅▄▃▅ ▇█      23.6M
hyperliquid                0          0           0  ▁▁            0.5M
ai-tradex-solana           0          0           0                   0
busdev ks polymarket   no agents, no data

tokens = input + cache writes (IN) and output (OUT); cache reads apart; no cost, by rule.
$ agentbus tokens --project agent-bus-monitor --by agent
AGENT           PROVIDER   TODAY IN  TODAY OUT  NOTE
foureyes        anthropic      1.2M       206k
master          anthropic      744k       254k
coder           anthropic      345k        83k
reviewer-codex  openai            ?          ?  Codex session not proved
ui-kimi         moonshot          ?          ?  Kimi logs give no token counts
sentinel        anthropic         0          0
```

The remainder nobody claims is the person's own scan:

```
$ agentbus tokens --unattributed
not attributed today   anthropic 2.9M in · 873k out   openai ?   (sessions no project's record names; host scan, read-only)
```

`--json` prints the same facts with `partial`, `observed_at` (the scan's Redis time) and the reason
of every `?`. Exit 0 always for a read that succeeded, even with unknowns.

## Mock-ups

Generated by a script from today's numbers (exact widths). The 24-hour sparklines are illustrative
in shape (no history exists yet; each ends at today's reading); every other number is measured.

### 190 columns

```
╭─ Tokens  (U) ───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────── updated 42s ago · no cost, by rule ─╮
│ account windows (shared by every project on the account)                                                                                                                                   │
│   anthropic  5h ▇▇▇▇▇░░░░░  46%   last 24h ▁▁▂▃▄▅▆▅▃▂▁▁▂▃▅▆▇▇▅▃▂▃▄▄   resets 2h49                  7d 8%                                                                                   │
│   openai     5h ▇▇▇▇▇▇▇▇▇▇  98%   last 24h ▁▁▁▁▁▁▁▁▁▁▁▁▂▃▄▅▆▇███▇▇█   resets 3h30                  7d 15%                                                                                  │
│   moonshot   7d ▇▇▇▇▇▇▇▇▇▇ 100%   last 24h ▇▇▇▇████████████████████   held until Tue 03/11 20:41   5h 0%                                                                                   │
│                                                                                                                                                                                            │
│ projects (all on this broker; the open one first)  ·  tokens = input + cache writes + output; cache reads apart                                                                            │
│   project              today in   today out   cache read   7 days (21→27 Oct)   7d total   share of anthropic today                                                                        │
│   agent-bus-monitor ●     5.1M        1.4M        884M     ▅▄▃▅ ▇█               23.6M     ████████████████████ 100%                                                                       │
│   hyperliquid                0           0           0     ▁▁                     0.5M     0%                                                                                              │
│   ai-tradex-solana           0           0           0                                    0     0%                                                                                         │
│   busdev · ks · polymarket   no agents, no data                                                                                                                                            │
│                                                                                                                                                                                            │
│ agent-bus-monitor by agent, today  (sessions proved with the agent's process only)                                                                                                         │
│   ● foureyes         anthropic    1.2M in    206k out                                                                                                                                      │
│     master           anthropic    744k in    254k out                                                                                                                                      │
│   ● coder            anthropic    345k in     83k out                                                                                                                                      │
│     reviewer-codex   openai          ? in       ? out   Codex session not proved (follow-up)                                                                                               │
│     ui-kimi          moonshot        ? in       ? out   Kimi logs give no token counts                                                                                                     │
│     sentinel         anthropic       0 in       0 out                                                                                                                                      │
│                                                                                                                                                                                            │
│ not attributed (sessions no project proves): agentbus tokens --unattributed                                                                                                                │
╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

### 120 columns

```
╭─ Tokens  (U) ─────────────────────────────────────────────────────────────────── updated 42s ago · no cost, by rule ─╮
│ account windows (shared by every project on the account)                                                             │
│   anthropic  5h ▇▇▇▇▇░░░░░  46%  24h ▁▁▂▃▄▅▆▅▃▂▁▁▂▃▅▆▇▇▅▃▂▃▄▄  resets 2h49                                           │
│   openai     5h ▇▇▇▇▇▇▇▇▇▇  98%  24h ▁▁▁▁▁▁▁▁▁▁▁▁▂▃▄▅▆▇███▇▇█  resets 3h30                                           │
│   moonshot   7d ▇▇▇▇▇▇▇▇▇▇ 100%  24h ▇▇▇▇████████████████████  held until Tue 03/11 20:41                            │
│                                                                                                                      │
│ projects (all on this broker; the open one first)  ·  tokens = input + cache writes + output; cache reads apart      │
│   project              today in  today out  7 days   7d total  share                                                 │
│   agent-bus-monitor ●     5.1M       1.4M  ▅▄▃▅ ▇█    23.6M   100%                                                   │
│   hyperliquid                0          0  ▁▁          0.5M     0%                                                   │
│   ai-tradex-solana           0          0                 0      0%                                                  │
│   busdev · ks · polymarket   no agents, no data                                                                      │
│                                                                                                                      │
│ agent-bus-monitor by agent, today  (sessions proved with the agent's process only)                                   │
│   ● foureyes         anthropic    1.2M in   206k out                                                                 │
│     master           anthropic    744k in   254k out                                                                 │
│   ● coder            anthropic    345k in    83k out                                                                 │
│     reviewer-codex   openai          ? in      ? out  Codex session not proved (follow-up)                           │
│     ui-kimi          moonshot        ? in      ? out  Kimi logs give no token counts                                 │
│     sentinel         anthropic       0 in      0 out                                                                 │
│                                                                                                                      │
│ not attributed (sessions no project proves): agentbus tokens --unattributed                                          │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

### 80 columns

```
╭─ Tokens  (U) ────────────────────────────────────────────────────── 42s ago ─╮
│ account windows (shared by all projects)                                     │
│  anthropic 5h ▇▇▇░░░  46% ▂▃▅▆▇▇▅▃▂▃▄▄ 2h49                                  │
│  openai    5h ▇▇▇▇▇▇  98% ▂▃▄▅▆▇███▇▇█ 3h30                                  │
│  moonshot  7d ▇▇▇▇▇▇ 100% ████████████ held                                  │
│                                                                              │
│ projects, open one first (in + cache writes + out)                           │
│  project           in+out  7 days   7d                                       │
│  agent-bus-mon… ●    6.5M  ▅▄▃▅ ▇█  23.6M                                    │
│  hyperliquid           0  ▁▁        0.5M                                     │
│  ai-tradex-solana      0            0                                        │
│  busdev ks polymarket  no data                                               │
│                                                                              │
│ agent-bus-monitor by agent, today (proved only)                              │
│  ● foureyes        1.2M/206k                                                 │
│    master          744k/254k                                                 │
│  ● coder           345k/83k                                                  │
│    reviewer-codex     ?/?     not proved                                     │
│    ui-kimi            ?/?     no counts in logs                              │
│    sentinel           0/0                                                    │
│                                                                              │
│ not attributed (sessions no project proves): agentbus tokens --unattributed  │
╰──────────────────────────────────────────────────────────────────────────────╯
```

## Tests

With a disposable broker (`AGENTBUS_TEST_REDIS_URL`) and fixture files only, never the user's
`~/.claude` or `~/.codex`:

- **scan**: duplicate usage lines of one request count once (the 8,181 case); subagent files
  count for their session; a line without usage or of another type counts nothing; a malformed
  line is skipped; local-day bucketing around midnight; the 64-file / 256 MiB budget sets
  `partial`.
- **incremental scan**: appended bytes only (a fixture file grown between passes counts each
  request once); a half-written last line waits for the next pass; a shrunk or replaced file is
  rescanned; the first pass after a restart is bounded and resumes, `partial` until complete; an
  id failing `sessionIDPattern` builds no path.
- **unattributed**: `agentbus tokens --unattributed` subtracts every project's records, counts a
  session once whatever directory holds it, and no dispatcher writes such a total.
- **attribution**: only `IdentSession` enters `{p}:sessions` (a status naming another session
  without a proof does not); a session no record names is in no project's sums; nothing is attributed
  from `cwd` or the directory name; pruning at 8 days and the 512 cap.
- **Codex**: a session spanning midnight (the first event of day 2 counted against day 1's
  last), a day with one event (its delta, not 0), the first event of a file (`last_token_usage`),
  a resumed rollout whose first total carries earlier turns, a counter reset, a repeated event
  (adds 0), a missing event in between, `info: null` skipped, an incremental pass resuming from
  the kept total; and the account identifiers never appear in any output or key (asserted on the
  fixture's values).
- **fencing**: a dispatcher without the lease writes none of the three keys; `{p}:tokens` is
  replaced whole; none of them revives a deleted project.
- **busmon**: pending vs unknown vs zero render differently; the animation runs only while
  something is pending or working (and the fade schedule stops after); the 16-colour palette draws
  the still frame; goldens at 190, 120, 80; `TestShellAlignmentSuite` over the page; the six new
  glyphs in `TestGlyphsAreSingleCell`.
- **CLI**: `tokens --all` without a project; `--json` carries the reasons; `?` never prints as 0.

## Rollout

1. The session record and the scan (`{p}:sessions`, `{p}:tokens`), `agentbus tokens`: the numbers,
   no UI.
2. `{p}:quota:hist` and the busmon page with sparklines and bars.
3. The animations (skeleton, pulse), last, behind the same true-colour rule as the fade.

## Open questions for review

1. Cache reads stay apart (foureyes agrees): they dominate by two orders of magnitude and no source
   states their weight against a window.
2. 24 one-hour cells (foureyes: fine).
3. Settled by review: "not attributed" belongs to the person's `agentbus tokens --unattributed`
   until the host dispatcher exists; busmon shows no such row.
