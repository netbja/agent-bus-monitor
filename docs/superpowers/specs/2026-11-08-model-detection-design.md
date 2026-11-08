# Model detection: keep the team up to date with each provider's models (model-detection)

Status: spec for review (subject `model-detection-spec`), v0.29, a priority for the person. No
code on this branch. Base `origin/main`. The facts come from the code and from a live run on
2026-11-07 (`agentbus models`, `agentbus roles doctor`, the clients' catalogue files and
versions: Claude Code 2.1.319, codex-cli 0.166.0, grok 1.0.77, Kimi Code 2.2.0, agy 1.5.3).

## 1. What already exists

A lot of this was built in v0.15.2 (spec 2026-10-17, swaps history and model drift):

| Piece | What it does | Where |
|---|---|---|
| Model table | One reader per client, run daily; it never calls a model and never opens a credential. Kept in `~/.cache/agentbus/models.json` | `models/` |
| Readers | openai: `$CODEX_HOME/models_cache.json`; anthropic: Claude Code's aliases plus `additionalModelOptionsCache` and the org default in `~/.claude.json` (**partial**); xai: `grok models`; moonshot: `~/.kimi-code/config.toml`; google: `agy models`, with other makers' rows tagged `family` | `models/readers.go` |
| Refresh | `agentbus models --refresh`; daily from the dispatcher's health chores, under a file lock, skipped when under 24 h old, and **only on a host that has services installed** | `healthchores.go` `modelsRefresh` |
| Tiers | frontier, workhorse, fast, legacy, from the source's words, then `models.toml` rules, then the built-in rules. A profile may name a `tier`, resolved through the table | `models.TierMap`, `Resolve` |
| Drift | `roles doctor` compares configured, launched (the bind) and used (Claude transcripts) per **running** agent. Its verdicts: `ok`, `drift`, `unknown model`, `gone` (missing from a fresh, non-partial list), `newer` (only with evidence: Codex's `upgrade` field, or a `models.toml` family order). It publishes them, and busmon polls them | `cmd/agentbus/doctor.go`, `bus/doctor.go` |
| Health | the `model-drift` check runs `doctorRows` | `healthchecks.go` |

What the live run shows (2026-11-07 23:30):

```
roles doctor: architect, coder, foureyes → opus → used claude-opus-5-5 · master sonnet → claude-sonnet-5-5
              sentinel haiku → claude-haiku-5-5 · reviewer-codex gpt-6-astra · all "ok"
models: anthropic partial (5 rows: 4 aliases + claude-fable-5-1[1m]) · google 6 · moonshot 6 (no tier)
        openai 8 (gpt-6.1-terra untiered) · xai 4 (grok-4.7 listed, grok-4.6 the default)
```

## 2. The gaps

| # | Gap | Evidence |
|---|---|---|
| G1 | **Nothing says a model appeared.** Refresh replaces a provider's rows. No first-seen date is kept, so a new frontier model in a list reaches nobody | `Table.Refresh` |
| G2 | **"newer" almost never fires.** Only Codex gives evidence, and `models.toml` does not exist on this machine. grok lists `grok-4.7` while `roles.toml` pins `grok-4.6` (profile `grok`), and nothing is said | live table; `catalogueVerdict` |
| G3 | **A catalogue's own age is unknown.** `ReadAt` is when *we* read it, not when the source was refreshed. Codex states `fetched_at` (fresh, 22:26 today); Kimi's `config.toml` was last written 2026-10-26, 12 days ago, and is still called fresh | files |
| G4 | **Unused profiles are never judged.** `roles doctor` only looks at agents that have a snapshot. `agy-review`, `grok`, `kimi`, and the fallback `claude-opus-5` of `claude-opus-fallback` (while `opus` runs 5-5) are never checked | `doctorRows` |
| G5 | **Claude is partial.** Its new models appear as an alias that resolves to a new id, not as a new row. That move is seen per agent in the USED column, but it is never recorded as news | doctor USED |
| G6 | **Untiered rows.** All 6 moonshot rows, `gpt-6.1-terra` and `gpt-oss-120b-medium` have no tier. A `tier` profile on moonshot cannot resolve | live table |
| G7 | **Refresh needs services.** A host with no services installed never refreshes, except by hand | `modelsRefresh` |
| G8 | **No reader for an adapter client** (`[adapters.*]`, a local LLM). DeepSeek has an icon but no client | `Readers` |
| G9 | **Agents do not know where to look.** No skill says "read `agentbus models`", so a master may write a model id from memory | the skills |

## 3. Design

### 3.1 The table remembers (G1, G5)

All additive JSON, so an older table still loads:

- **`Provider.Seen`**: `map[id]{first, last int64}`. Refresh sets `first` the first time an id is
  listed and `last` on every listing.
- **`Table.Events`**: the newest 200 changes, each `{at, provider, kind, id, from}`. The kinds:
  - `added`: an id listed for the first time. It is written only when the provider already had a
    successful read before. The first read of a provider fills `Seen` silently, so installing a
    client does not raise 30 events.
  - `removed`: an id that was listed and is gone. Only for a non-partial provider, and only when
    both reads succeeded and are fresh. `gone` in `roles doctor` uses the same rule.
  - `default`: the source's default row changed (grok's `*`, Kimi's `default_model`, Claude's org
    default).
  - `alias`: a Claude alias now resolves to another id (G5). The evidence is the used model of the
    transcripts, which `doctorRows` already reads. The table keeps `Aliases: map[alias]{id, at}`
    (for example `opus → claude-opus-5-5`). A new id for an alias writes an `alias` event with
    `from`. It is written under the same lock as the refresh: the doctor health task takes it, and
    skips if it cannot get it.
- `agentbus models --changes [--since 7d]` lists the events, newest first.

### 3.2 The source's own age (G3)

`Reader.Read` also returns `sourceAt`. Each reader states it from evidence only:

- Codex: `fetched_at` in `models_cache.json`.
- grok and agy: the time of the read, because they ask their server live.
- Kimi: the read's time (the person, §6 Q5: its list is read daily with the others). The first
  draft used `config.toml`'s mtime, which Kimi Code rewrites only on its own update.
- Claude: unknown. Its options cache lives in a file that changes for many other reasons, so the
  file's mtime proves nothing.

`Provider.SourceAt` is stored. `agentbus models` prints `source 12d old` next to the read time.
A source older than `models.source_max_age` (a new setting, registered in `config/keys.go`, default 7 d) makes `gone` and `newer`
withhold their verdict, as a read older than `MaxAge` already does. The health check (§3.5) names
it: `moonshot's catalogue is 12 days old (Kimi Code rewrites it when …): open kimi once, or
update it`. What refreshes Kimi's file is §6 Q5.

### 3.3 "newer" with evidence, "newer?" with a hint (G2)

`newer` keeps its rule: the source says so, or `models.toml` orders the family. A second, weaker
signal is added and **never acted on by anything**:

- **`listed-newer`**: the same provider lists an id with the **same stem and the same suffix** and
  a **higher version number**. The rule runs in three steps (foureyes, reviewer-codex: a version
  must never absorb a date):
  1. Both ids go through `BaseID` first, which drops Claude's context suffix (`[1m]`) and a
     trailing release date. `BaseID` gains the `-YYYY-MM-DD` form next to `-YYYYMMDD`. Two ids
     with the same base are the same model (`sameModel`) and are never "newer".
  2. Each base is split as `^(.*?)(\d{1,4}(?:[.-]\d{1,4})*)(.*)$`. A version component has at most
     4 digits, so an 8-digit date can never become one.
  3. The prefixes and suffixes must be equal, and the version is compared as a tuple of numbers.
  Examples, all checked against the rule:
  - `grok-4.6` → `grok-4.7` (counts);
  - `gpt-6-sol` → `gpt-6.1-sol` (counts);
  - `claude-opus-5` → `claude-opus-5-5` (counts);
  - `model-4-20251001` → `model-5-20251001` (counts: version 5 > 4, the dates are dropped);
  - `gemini-3.8-flash-high` → `gemini-4-flash-high` (counts);
  - `grok-4.7-build-fast` is not a candidate for `grok-4.6` (the suffix differs);
  - `kimi-code/k3` → `kimi-code/k3.1-pro` does not count (the suffix differs);
  - `claude-haiku-4-5` ↔ `claude-haiku-4-5-20251001`, in both directions, does not count (the
    same model);
  - `claude-haiku-4-5-20250101` → `…-20251001` does not count (only the date differs);
  - `m-2025-01-01` → `m-2025-06-01` does not count;
  - `claude-fable-5-1[1m]` → `claude-fable-5-1` does not count;
  - `gpt-4o` ↔ `gpt-4.1` does not count (the suffixes differ);
  - `gemini-3.8-flash-low` → `-high` does not count (the suffixes differ).
- It is shown as `newer?` in `roles doctor`, the profile check (§3.4) and the health detail, with
  the words `listed, not stated as a successor`. It never makes the `model-drift` check fail. It
  warns only in the catalogue check (§3.5).
- Table-driven tests take every example above as a fixture, in both directions where the text
  says so. One more case: a family order in `models.toml` wins over the hint.

### 3.4 Every profile is judged, not only the running agents (G4)

`agentbus models check [--json]` reads `roles.toml` and judges **every profile's model and every
`fallback`**, used or not, against the fresh table:

| Verdict | When |
|---|---|
| `ok` | listed (or an alias, or a tier that resolves) |
| `gone` | not listed by a fresh, non-partial source |
| `newer` | evidence names a successor that is listed |
| `newer?` | §3.3 |
| `not listed (partial)` | anthropic: the id is not in Claude's short list, which proves nothing. It is shown, never judged |
| `untiered` | a `tier` profile whose provider has no row of that tier |
| `unknown` | the provider's read failed, or its source is too old |

`roles doctor` keeps its role (what **runs**). `models check` is about what is **configured**. They
share `catalogueVerdict`, which is extended with `newer?`, so the two never disagree on a model.

### 3.5 A health check that brings the news (G1–G4, G7)

There is a new check, **`model-catalogue`**, beside `model-drift`. Its statuses use the existing
`health` scale:

- **warn**: a configured model is `gone`; a configured model has a `newer` or `newer?`; a source
  is older than its max age; a provider's read has failed for more than 2 days.
- **info line, ok status**: `added`, `default` and `alias` events of the last 24 h, for example
  `new: xai grok-4.7 (2d) · anthropic opus now claude-opus-5-5`.
- The check runs the refresh first, under the lock, if the table is over 24 h old. **G7 is
  closed:** the refresh no longer waits for services, and `agentbus models` (any form) also
  refreshes a table older than 24 h before it answers. That automatic refresh is bounded
  (foureyes): **10 s in all**, with each reader under the same deadline (an explicit `--refresh`
  keeps the current 20 s per reader). It is skipped when another process holds the lock. A
  reader that fails or times out keeps its provider's previous rows; `Refresh` already never
  empties them. The answer then comes from the table as it is, with each provider's age and the
  line `refresh incomplete: <provider> <reason>`. It never blocks past the deadline and never
  answers empty because of a failed refresh. The health check runs the same bounded refresh
  inside its own task deadline.
- A client update (the `client-updates` check, which compares client fingerprints) forces a
  refresh of that client's provider, because a new client brings a new catalogue.

### 3.6 Who acts, and how: never an automatic profile change

- **Nothing writes `roles.toml` on its own.** A test (§4) checks that no code under `models/`,
  `cmd/agentbus/health*` or `dispatch/` opens `roles.toml` for writing.
- **sentinel proposes.** Its daily review already reads health. When `model-catalogue` warns,
  sentinel sends master **one report per finding**, carrying the exact line to change and the
  evidence:
  `profile grok: model grok-4.6 → grok-4.7? (grok models lists grok-4.7 since 2026-11-05, not
  stated as a successor; grok's own default is still grok-4.6)`. It sends nothing for `ok`, and
  never repeats a finding it already reported. The de-duplication key is the finding, not an
  event (foureyes): `(profile, field, model, verdict, candidate)`, for example `(grok, model,
  grok-4.6, newer?, grok-4.7)`. `gone`, `newer?` and an old source are derived on every pass, so
  an event id could not de-duplicate them. The keys live in sentinel's annotation. A finding that
  disappears (the profile was changed) is dropped from it, so a later return is reported again.
- **The person decides.** master turns the proposal into an ask in For you, with the diff,
  as PRs to merge already are. On yes, the change goes to coder as a normal step
  with 4-eyes. A `gone` model on a running role is urgent: its next launch will fail, so the ask
  says so first.
- **busmon** shows the info line's news once, as a notice, in the same way as other health news.
  It is not a For you row: For you holds decisions, and the ask is the decision.

### 3.7 Tiers for untiered rows (G6)

No guessed defaults. `agentbus models` prints `-` for the tier, and `models check` names the
provider: `moonshot: 6 models have no tier; a tier profile cannot resolve there (models.toml
[[tier]] rules)`. Which Kimi model is frontier is the person's call (§6 Q3). Once answered, the
rules go into the built-in `DefaultTiers`, the way grok's and Gemini's did.

### 3.8 How an agent learns the list (G9)

- **agent-bus-master skill:** "Never write a model id from memory. `agentbus models --json` is
  this machine's table; `agentbus models check` judges the profiles; `agentbus models --changes`
  says what moved. What you run on yourself: the `USED` column of `agentbus roles doctor --agent
  master` (a new `--agent` filter)."
- **agent-bus skill (peers):** one line, the same pointer, for an agent asked about a model.
- **sentinel skill:** §3.6, the proposal format.
- **AGENT-BUS-GUIDE:** the `models --json` fields (`seen`, `source_at`, `events`), versioned like
  the rest: additive only.

### 3.9 Not in this spec (G8)

There is no reader for an adapter client. The follow-up is an optional `models = ["<exe>",
"<args>"]` in an `[adapters.*]` table: a fixed argv whose output is `id<TAB>label` lines, the agy
format. It is §6 Q4. DeepSeek stays an icon until there is a client.

## 4. Tests

The seam is `models.Table.Refresh` with fake readers and a fake clock, and `cmd/agentbus` with a
temp `models.json` and a temp `roles.toml`, as the existing tests use. No test reads a real
client file or calls a client.

- `models`:
  - `Seen` is filled silently on the first read, `added` on a later one, and `removed` only when
    both reads are fresh and non-partial;
  - a failed read changes neither `Seen` nor `Events`;
  - `Events` is capped at 200;
  - `alias` is written once per move;
  - an old table without the new fields loads and saves;
  - `sourceAt` per reader: Codex's `fetched_at`, Kimi's mtime through `Env.Stat`, and Claude
    unknown;
  - `listed-newer` table cases (§3.3), positive and negative.
- `agentbus models check`: a golden for a manifest with an unused profile, a fallback, a tier
  profile on an untiered provider, a gone model, a `newer` and a `newer?`
  (`testdata/models-check.golden`).
- `agentbus models --changes` golden; `models` auto-refresh when the table is over 24 h old, and
  no refresh when it is younger; a reader that hangs past the 10 s deadline (a fake `Run` that
  blocks) gives the old rows, the age and `refresh incomplete`, in under 11 s; a held lock skips
  the refresh.
- sentinel's de-duplication key: the same `newer?` twice is proposed once; the finding is
  proposed again after it disappears and returns.
- The health check: each warn cause, the info line, no warn for `ok` alone, and a forced refresh
  after a client fingerprint change.
- **No profile writer:** a source-scanning test, like `TestNoStructWriterOfTheBoard`: no
  `os.WriteFile`, `os.Create` or `OpenFile` with a write flag names `roles.toml` (or the manifest
  path variable) outside `rolecfg`'s init/migration code.
- The sentinel and master skill lines are checked by the existing skill-text tests, if any. If
  there are none, they are reviewed by eye.

## 5. Steps (each its own subject for 4-eyes)

1. `models`: `Seen`, `Events`, `SourceAt`, `Aliases`, and the `listed-newer` rule (pure, with the
   tests above).
2. `agentbus models --changes`, `models check`, the auto-refresh, and the `roles doctor --agent`
   filter.
3. The `model-catalogue` health check, the `models.source_max_age` setting, the refresh without services, the refresh after a client
   update, and the busmon notice text (only the text, because coder is rewriting
   `cmd/busmon`; the wiring comes after).
4. Skills (master, peer, sentinel), AGENT-BUS-GUIDE, operator-notes.

## 6. Questions for the person

1. **Where should news go?** Recommended: a busmon notice for `added`/`alias`, and a sentinel
   proposal turned into an ask (For you) only when a **configured** model is concerned. Do you
   also want an ask for every new frontier model, even one no profile uses?
2. **Should `newer?` exist?** It flags `grok-4.6 → grok-4.7` today, which only a version
   comparison sees. The recommendation is yes, and it never fails a check.
3. **Kimi tiers.** Which of `k3`, `k3.1-pro`, `k3-256k`, `kimi-for-coding` and the highspeed ones
   are frontier, workhorse and fast for you? **Answered (the person, 2026-11-08): one tier per
   family, the highspeed variants not told apart.** Every Kimi id is `frontier`
   (`DefaultTiers`), and a tier profile on moonshot resolves to the family's default,
   `kimi-code/kimi-for-coding`, the non-highspeed coding model (`models.FamilyDefault`), before
   Kimi's own `default_model`. `newer?` does not compare Kimi's variants with each other.
4. **Adapter readers (§3.9)** now, or later?
5. **Kimi's catalogue** was last written 2026-10-26. Does Kimi Code refresh it at each start, at
   login, or only on update? If you know, the health check can say how to refresh it; otherwise
   it says "open kimi once" and we measure. **Answered (the person, 2026-11-08): it refreshes
   daily with the others.** Kimi's `source_at` is the daily read's time, as grok's and agy's
   (§3.2 no longer uses `config.toml`'s mtime); its hint is `agentbus models --refresh`.
