# History: re-read the past (design, spec only)

Status: spec for review; no code yet.
Task: history-spec (master's directive of 2026-11-01, `1793524064032-0`; the person's fifth
distinctive feature, "relire le passé"): re-read past discussions, reports, asks and decisions,
with no database, no new daemon. Small and light.

## (a) What is lost today, and when

Every stream is capped at ~1000 entries (`XADD MAXLEN ~`); the verdict ledger at 10 000. What
this project's broker held on 2026-11-01 (measured, `XLEN`, oldest id, `MEMORY USAGE`):

| Stream | Entries | Reaches back | Memory |
|---|---|---|---|
| `{p}:cmd` (directives, replies, challenges, verdicts, asks) | 1006 | **37 h** | 639 KiB |
| `{p}:status` | 1034 | 98 h | 101 KiB |
| `{p}:report` (with `full`) | 697 | 277 h | 709 KiB |
| `{p}:notify` | 22 | 254 h | 3 KiB |
| `{p}:verdicts` (the audit) | 704 | 276 h (not yet capped) | 1032 KiB |
| `{p}:health:log` | 333 | 278 h | 25 KiB |
| `{p}:swaps` | 22 | 138 h | 4 KiB |

So **a thread is gone after about a day and a half**: `cmd` takes ~650 entries a day, and
`agentbus thread <id>` then finds nothing. Reports last about eleven days today, fewer on a busy
week. Statuses, about four days. The verdict ledger keeps the decisions (10 000 entries, years
at today's pace), but not the discussion that led to them. Asks keep their answers in `{p}:asks`
until withdrawn; the reply that woke the agent is a `cmd` entry and goes with the rest.

What already lasts: the shared memory and specs (Git), the verdict ledger, the swap ledger, the
plan (Git plus its facts).

## (b) The archive

**What.** One append-only JSONL file per project per day, written by the dispatch service from
the streams: `$XDG_STATE_HOME/agentbus/<project>/history/YYYY-MM-DD.jsonl` (the project's state
directory, `service.Paths.StateDir`, where its logs already live). The day is the UTC day of the
entry's stream id. Directory 0700, files 0600.

**Which streams.** `cmd`, `report`, `status`, `notify`, `verdicts`, `health:log`, `swaps`. One
line per entry, the entry as the stream holds it, plus where it came from:

```json
{"stream":"cmd","id":"1793524064032-0","f":{"from":"master","target":"coder","type":"directive","ref":"","command":"New feature from Bernard ..."}}
{"stream":"report","id":"1793525590943-0","f":{"agent":"coder","kind":"note","message":"Hand-off ...","full":"Hand-off (context ~90%) ..."}}
{"stream":"gap","id":"1793400000000-0","f":{"of":"status","from":"1793390000000-0","to":"1793400000000-0"}}
```

Nothing is added that the streams do not already hold. No hash is archived (agents, board, asks,
observed): they are state, not history, and the parts that matter reach the streams anyway (each
status is an entry; an ask's question and its answer are `cmd` entries).

**Writer.** A chore of the dispatch service, like the tokens pass: every minute, for each stream,
`XRANGE (cursor +` paged 500, appended to the day's file, then `fsync` once per pass. Within the
dispatcher's lease only: the chore checks `DispatchAlive` with its token before each pass and
holds an `flock` on `history/.lock` while it appends, so a second dispatcher (a lost lease, a
race at start) never writes the same file. These are file writes, not Redis writes: nothing new
is fenced because nothing new is written to Redis. Bounded: at most 5 s and 2 000 entries a pass;
what is left waits for the next pass.

**Cursor.** No new Redis key. A small sidecar, `history/cursor.json` (stream → last archived id,
0600), is replaced by a rename after each pass's `fsync`, so it never claims more than the files
hold. A stream that writes rarely (`notify`, two a day here) keeps its cursor all the same. A
missing or unreadable cursor starts from each stream's oldest entry; lines already in the day
files are then skipped by **(stream, id)** (the files of the days the stream still spans are read
once to collect them), so a restart writes no duplicate. Stream ids are unique within a stream,
not across streams: a `cmd` and a `report` entry can share an id, and both are kept
(reviewer-codex).

**On start, every time.** First, a day file whose last byte is not a newline (a crash in the
middle of an append) is truncated after its last newline: a torn line is never glued to the next
append. Then each stream's position is **max(cursor, the highest id in the day files the stream
spans)**, the files read once at start, whether `cursor.json` exists or not. So a crash between an
append and the cursor's rename (the cursor behind the files) re-reads nothing twice (foureyes).

**A deleted directory.** Deleting `history/` deletes `cursor.json` with it, so the next pass cannot
know what was missed. A missing cursor with **no day file at all** writes one line first:
`{"stream":"gap","id":"<now>","f":{"of":"*","from":"unknown","to":"<each stream's oldest id>"}}`,
read as `-- archive restarted: nothing archived before <date> --`. A missing cursor with day files
present rebuilds the cursor from them, as above. So a lost archive is never silent either.

**Gaps.** If a stream's oldest entry is newer than its cursor (the dispatcher was down longer than
the stream's reach), the chore writes one `gap` line, from the cursor to that entry, then goes on.
A reader says "not archived: 14:20 to 18:05". A gap is never silent.

**No secret, best effort.** The streams hold no secret by design; a report can still carry a
pasted one, and the archive would keep it for 90 days instead of the stream's few. So each string
field is checked before the line is written, and **on any hit the whole field value is replaced**
by `[redacted]`, never just the matched span: health's `sensitiveText` was built to refuse a
whole text, and used as a span replacer it leaves the secret (foureyes ran it on four inputs, all
four kept their secret: a private key's body, the token after `Bearer`, `REDIS_PASSWORD="…"`,
`redis://:p@host`). The shapes, in a new exported `Redact` that health's `SafeText` then uses too:

- `password`, `token`, `secret`, `api_key`, `authorization` followed by `:` or `=`, as a word
  **or as the end of an env-style name** (`REDIS_PASSWORD=`, `GH_TOKEN=`, `AWS_SECRET_ACCESS_KEY=`);
- `Bearer` or `Basic` followed by an actual token (`[A-Za-z0-9._~+/=-]{8,}`) anywhere, so prose
  such as "the Bearer token" or "Basic auth" in a health detail or a sentinel note is not refused
  by `SafeText` (foureyes);
- any URL scheme with userinfo, an empty user included (`https://u:p@`, `redis://:p@`,
  `rediss://u:p@`);
- `-----BEGIN … PRIVATE KEY-----`;
- the known token prefixes (`sk-ant-`, `ghp_`/`gho_`/`ghu_`/`ghs_`/`ghr_`, `xox?-`).

This is best effort: a secret in a shape not listed is archived as written. The person is told so
in the spec's question 6 and in `agentbus history --help`.

**Retention**, two keys in `config.toml` (`config/keys.go`, read by dispatch):

| Key | Env | Default | Meaning |
|---|---|---|---|
| `history.keep_days` | `AGENT_BUS_HISTORY_DAYS` | 90 | days of files kept; `0` = no archive (nothing written, nothing pruned) |
| `history.max_mb` | `AGENT_BUS_HISTORY_MAX_MB` | 256 | the directory's cap; the oldest days go first |

Pruning runs once a day in the same chore: delete whole day files older than `keep_days`, then
the oldest ones until the directory is under `max_mb`. Never today's file. The person can delete
the directory at any time; the next pass starts a new file and writes a `gap` line.

## (c) Reading it

### `agentbus history`

```
agentbus history [--agent A] [--thread T] [--kind K] [--since D] [--until D]
                 [--grep TEXT] [--limit N] [--json]
```

- Reads the archive files of the project, then the live streams for what is newer than the
  archive, merged by (stream, id): the same entry from both is printed once, two streams' entries
  with the same id both print. With no broker it still reads the
  files, and says `broker unreachable: archive only`.
- `--agent`: entries from or to it (`from`, `target`), and its own reports and statuses (their
  `agent` field). `--thread`: the entry whose id or `ref` is T, as
  `agentbus thread` does, across days. `--kind`: `directive`, `reply`, `verdict`, `report`,
  `status`, `notify`, `ask`, `health`, `swap`. `--since`/`--until`: a date, a time or a duration
  (`2026-10-30`, `3d`). `--grep`: a case-insensitive substring of the text. Default limit 200,
  newest last.
- Lines as `listen` prints them; `--json` one object per line, the archive's own form.
- It needs the project (below the project-required exit, like every project command) but not the
  broker.
- Gaps print as `-- not archived: Sat 14:20 to 18:05 (status) --`.

```
$ agentbus -p agent-bus-monitor history --thread 1793524064032-0
Sat 18:47  master → coder   directive  New feature from Bernard (5th distinctive feature) ...
Sat 18:49  coder  → master  reply      queued after pages step 4
Sat 21:30  master → coder   directive  Stall-guard spec merged (PR 228). NEXT ...
```

### busmon: History (`H`)

A reader page in the one list format (`listrow.go`), opened by `H` from the main screen (free
today), loaded in a command (file reads never in View):

```
╭─ History · 3 days · 2 140 entries · filter: coder ─────────────────── / filter · esc back ─╮
│   ── Sat 2026-11-01 ──                                                                       │
│   18:47  master → coder    directive  New feature from Bernard (5th distinctive feature)...  │
│ > 18:49  coder → master    reply      queued after pages step 4                              │
│   21:30  master → coder    directive  Stall-guard spec merged (PR 228). NEXT, one task...    │
│   ── not archived: 14:20 to 18:05 (status) ──                                                │
│   ── Fri 2026-10-31 ──                                                                       │
│   23:58  foureyes          verdict    busmon-notice 885ba49: APPROVE ...                     │
│   + 1 980 older  (o loads the previous 3 days)                                               │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
```

- The last 3 days by default, capped at 8 MiB read (a busy day file is read from its end, and the
  page says `older lines of Sat not loaded`); `o` loads three more days under the same cap, never
  the whole archive at once.
- `/` filters as Activity's filter does (agent, kind or text); Enter opens the entry in the
  reader's message page, `n`/`p` step through the list (A5's `readerStep`); `t` opens the
  selected entry's whole thread.
- The narrow tier shows the same rows with the kind short (`dir`, `rep`, `ver`).

## (d) Backup and audit

- **Backup.** The archive is part of what the backup task saves (plan task `backup`: the config
  directory, secrets optionally encrypted): it adds `$XDG_STATE_HOME/agentbus/<project>/history/`
  to its list. **Treat it as potentially sensitive**: redaction is best effort, so a pasted secret
  in a shape it does not know may be in it, besides the team's discussions. The backup handles it
  like the secrets it already carries: encrypted when the backup encrypts secrets, and never
  copied to a destination less private than the config's (reviewer-codex).
- **Audit.** The verdict ledger stays the audit source of truth. The archive is a reading aid: a
  plain file on one host, not tamper-evident, never consulted to decide "was this approved?".
  `verdicts` lines in it are copies, for reading a decision with its discussion.

## (e) Cost and risk

- **Disk.** From the measured rates: `cmd` ~0.4 MiB a day, reports ~0.2 MiB, statuses ~0.03 MiB,
  the rest small: about **0.7 MiB a day**, ~60 MiB for 90 days per project, under the 256 MiB cap.
- **CPU and Redis.** One `XRANGE` per stream a minute, paged; nothing on the write path of the
  bus. The chore has its own deadline and never runs in the reconciliation pass.
- **Never blocks the bus.** A full disk, an unwritable directory or a slow file system: the pass
  logs the error once per kind and skips; the next pass resumes from the cursor (and writes a gap
  if the stream moved past it). Health gains nothing for this in step 1 (a question below).
- **Privacy.** Reports and directives now outlive the stream: up to 90 days on disk, 0600, on the
  dispatcher's host. Redaction catches credential shapes only, not personal text.
- **One host.** The archive lives where the dispatch service runs. `agentbus history` on another
  machine (through the SSH tunnel) sees only the live streams, and says so.

## Not in this spec

No database, no index (a day file is read whole; 3 days are ~2 MiB), no search server, no
archive of the hashes, no upload anywhere.

## Tests (plan)

- Writer (Redis opt-in through `AGENTBUS_TEST_REDIS_URL`, files in `t.TempDir()`): a pass
  appends each stream's new entries once; a second pass appends nothing; a restart reads
  `cursor.json` and writes no duplicate; with `cursor.json` deleted, a restart rebuilds from the
  day files and still writes no duplicate; a crash between the append and the cursor's rename
  re-reads those entries and skips them by id; a stream trimmed past the cursor writes one `gap`
  line; a lost lease writes nothing; a second writer blocked by the `flock`; files 0600 in 0700;
  the 2 000-entry and 5 s bounds leave the rest for the next pass; a line with `token: abc...`,
  each of foureyes' four inputs (a private key with its body, `Authorization: Bearer <jwt>`,
  `REDIS_PASSWORD="…"`, `redis://:p@host`) and `GH_TOKEN=…` leaves no secret: the whole field is
  `[redacted]` (and `SafeText` refuses the same text); a field with no hit is written unchanged;
  a torn last line is truncated on start; a stale `cursor.json` behind the files appends nothing
  twice; a `cmd` and a `report` entry with the same id are
  both archived and both printed; the whole `history/` directory deleted, the next pass writes the
  "archive restarted" line before anything else.
- Retention: older than `keep_days` deleted, today never; over `max_mb` the oldest go; `0` writes
  and prunes nothing.
- `agentbus history`: each filter (`--agent` matching a report's and a status's `agent`), the merge with the live streams by (stream, id) (no duplicate), no
  broker (archive only, said), gaps printed, `--json` round-trips the archive's lines.
- busmon: goldens of the page (wide, narrow), `o` loading three more days, the filter, Enter and
  `t`; no file read in View (the existing purity tests).

## Questions for Bernard

1. **Retention.** 90 days and 256 MiB per project by default, or longer?
2. **Statuses.** Archive them (~250 a day here, the "who was doing what" timeline), or only the
   conversation (`cmd`, `report`, `notify`) and the ledgers?
3. **Key.** `H` for the History page, or an F-key (`F6`)?
4. **Health.** Should a failing archive (disk full, unwritable) be a health warning, or only a log
   line as proposed for step 1?
5. **Other hosts.** Is "the archive lives on the dispatcher's host" enough, or should
   `agentbus history` on the laptop reach it (through the SSH tunnel, as a file copy)?
6. **Redaction is best effort.** A pasted secret in a shape the patterns do not know stays in the
   archive for its retention. Acceptable, or should reports (the usual carrier) be archived only
   as their one-line preview, never the `full` text?
