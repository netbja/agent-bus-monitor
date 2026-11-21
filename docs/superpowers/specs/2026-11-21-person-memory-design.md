# The person's long-term memory, for master and the concierge

Status: spec r2, for review (subject `person-memory`). Not built. Base: origin/dev `508f4290`
(r1 merged as #678). r2 records the person's answers (thread `1795219776854-0`, relayed in cmd
`1795219791074-0`). It adds the check of what the person's channels write (§4.7), which
changes the source rule (§4.3.1), the question an answer replies to (`--answers`), `purge`, and
the build steps (§10).
Author: architect, from the person's directive of 2026-11-21 (thread `1795218784752-0`), relayed by
master (cmd `1795218795667-0`). It is a read-only, time-boxed study of two checkouts, which were
not modified:
- `/data/AI-Docs/memory-graph`: MemoryGraph, MIT, read for ideas;
- `/data/AI-Docs/memory-mcp-server-go`: read for its Go shape. Its README carries an MIT badge,
  but the checkout has no LICENSE file; §10 step 6 checks the licence before the credit names it.

No code is copied from either.

## 1. The need, as the person decided it

Master, and later the multi-project concierge, remembers **what the person said or decided**
across sessions and projects. There are two layers:
- **Layer 1**, now: distilled facts. They are small Markdown files, one per topic, kept at host
  level in a local git repository.
- **Layer 2**, later and only if needed: an archive of the person↔master threads, with retention
  and a search in pure Go.

Project memory stays where it is, as Markdown in the project's git (`docs/memory/`).

The person's constraints:
- no new dependency, no graph or vector database, and no MCP server, so it is built into agentbus;
- only what the person stated, never an agent's inference;
- secrets are filtered before any write;
- the person can view, edit and delete from busmon;
- MemoryGraph is credited in the README.

## 2. What already exists

| Piece | What it is today | Use here |
|---|---|---|
| `docs/memory/` | The project's reviewed memory: decisions, lessons, open questions | Unchanged. A person's decision about one project still becomes a project note, through review (§4.6). |
| Claude Code's per-project memory (`~/.claude/projects/<dir>/memory/`) | One directory per project directory, shared by every Claude role launched there, master included. It holds agent-written drafts: preferences, lessons and hand-offs, mixed with the agents' own conclusions. | **Not a source.** It is per client and per directory, not host-level, and not limited to the person's words. Nothing is migrated automatically (§6, question 5). |
| `history` (`history/archive.go`) | Per project, the `cmd`, `report`, `status`, `notify`, `verdicts`, `health:log` and `swaps` streams, archived to one JSONL file per UTC day. Every field goes through `health.Redact`. It is kept for `history.keep_days` (90) or up to `history.max_mb` (256). It is read by `agentbus history --agent --thread --grep` and busmon's History page. | **This is most of layer 2 already** (§5). |
| `health.Sensitive` / `health.Redact` (`health/health.go:212`, `:229`) | The credential-shape detector: secret-looking `name=value`, URL userinfo, private-key blocks, known token prefixes, Bearer/Basic. `Redact` replaces the whole field on a hit. | **The secret filter reused** (§4.3), as a refusal rather than a redaction. |
| `notify.Scrub` (`notify/scrub.go`) | `health.Redact`, then URLs, emails and paths become placeholders | Not used: a memory keeps the person's words, so paths and URLs they state are content. |
| `internal/scrub` | **A test, not a filter.** `TestNoPrivateReferences` keeps private host, project and people names out of the tracked tree. | Not applicable at write time: the directive's "reuse internal/scrub" names the wrong package. The write-time filter is `health.Sensitive`. |
| `human=yes` on a bus entry (`bus.HumanCmd`, `bus/stream.go:1128`) | Marks a directive the person wrote from busmon. Agents and busmon tell it apart from an agent's. | Half of the provenance check: §4.7 shows it also marks texts agentbus composes in the person's name, so §4.3 requires `typed=yes` as well. |
| `agentcaller.Refuse` (`internal/agentcaller/agentcaller.go:73`) | Refuses a command run from an agent's process tree | Guards the person-only verbs (§4.4). |
| `config.BackupDir` (`config/backup.go`), `update/run.go:50` | `${XDG_DATA_HOME:-~/.local/share}/agentbus/<area>` | The same root for the memory (§4.1). |

## 3. What the study takes, and what it leaves

**From MemoryGraph** (its `docs/schema.md`, `docs/TOOLS.md`, `docs/temporal-memory.md`, README):

- **Taken:**
  - **Typed memories.** Its 12 coding-oriented types become 6 that describe what a person
    says: `decision`, `preference`, `constraint`, `problem`, `solution`, `fact`.
  - **Importance.** Its 0.0-1.0 float, documented as bands (0.8+ critical, 0.5-0.7 standard,
    0.3-0.4 minor), becomes those three named levels: `critical`, `standard`, `minor`. A float
    invites a precision nobody states.
  - **Typed relations.** Its 35 relation types in 7 families become 4 that a person's statements
    need: `supersedes`, `solves`, `applies-to` and `contradicts`.
  - **The discipline.** "Recall before work, store on decision" (its README: "You MUST use
    `recall` before any task") becomes master's skill rule (§4.5).
  - **Time.** Of the bi-temporal model, only *when it was said* (the bus entry's time) and
    *superseded by* (a later observation's id) are kept.
- **Left:**
  - Neo4j and the other backends, Cypher, and full-text and vector indexes;
  - `confidence`, `effectiveness` and `usage_count` (scores an agent would infer);
  - proactive suggestions and outcome learning (inference by design);
  - multi-tenancy and the cloud backend.

**From memory-mcp-server-go** (its `storage/interface.go` and `storage/jsonl.go`):

- **Taken:**
  - **The data shape:** an **entity** (a name and a type) holds **observations** (strings), and
    **relations** link entities (from, to, a type in active voice). Here an entity is a topic
    file, and an observation is one statement in it.
  - **Ranking without an index:** match priority, then a stable order. An exact name beats a
    partial name, which beats the type, which beats the content. It is enough for hundreds of
    observations, read from files in pure Go.
  - **Conflict detection** between observations of one entity. Here it is narrower: a new
    `decision` on a topic that already holds an active `decision` asks whether it supersedes
    the old one (§4.3).
- **Left:** SQLite and FTS5, synonym expansion, time-decay scoring, the MCP and HTTP transports,
  and OAuth.

## 4. Layer 1: the person's facts

### 4.1 Where and what

- **Directory:** `${XDG_DATA_HOME:-~/.local/share}/agentbus/memory/person/`, a git repository that
  agentbus creates on its first write. agentbus runs git as a command, as it already does
  (`mission.Git`), so no library is added. There is one directory per host, shared by every
  project on it.
- **One file per topic:** `<topic>.md`, where `<topic>` matches `^[a-z][a-z0-9-]{0,47}$`. Example
  topics: `language`, `merge-policy`, `notifications`, `project-demo`. The file is plain
  Markdown, readable in any editor and in Obsidian.

```markdown
---
topic: merge-policy
kind: practice            # person | project | tool | practice | other
relations:
  - applies-to: project-demo
---
# merge-policy

- id: 20261120-1795205976983-0
  type: decision
  importance: critical
  said: 2026-11-20 by the person, project agent-bus-monitor, entry 1795205976983-0
  quote: "yes, dev first"
  answers: entry 1795205970000-0 by master
  question: "Should PRs go to dev, with main taking only promotions?"
  supersedes: 20261116-1794800000000-0
```

`kind` and `relations` are the entity's. The bullets are its observations. An observation's `id`
is the date plus the source entry's id (the `said:` entry), so the same statement is never stored
twice. `answers` and `question` are present only when the person's words reply to a proposal
(§4.3.2). The question is shown as context, never as the fact.

### 4.2 Verbs

All the verbs sit under `agentbus mem person`, beside the existing `mem check` and `mem sync`. They
need no `--project`.

| Verb | Who | What |
|---|---|---|
| `remember <topic> --type T --importance I --from <project>:<entry> --quote "<words>" [--answers <project>:<entry> --answers-quote "<words>"] [--supersedes <id> \| --keep-both] [--kind K] [--relates <rel>:<topic>]` | master, the concierge, the person | Adds one observation, after the checks in §4.3. Each write is one git commit. |
| `recall <words> [--type T] [--project P] [--all] [--limit N] [--json]` | anyone | Ranks observations as in §3: exact topic, then partial topic, then type, then quote. Ties go to importance, then the newest. Superseded observations are hidden unless `--all` is given. |
| `show <topic>`, `list` | anyone | One topic in full; the topics with their counts. |
| `forget <id or topic>` | **the person only** | Deletes the observation or the topic file, then commits. It refuses from an agent's process tree (`agentcaller.Refuse`). |
| `purge` | **the person only**, with a typed yes | Rebuilds the repository from its current files as one new first commit, so a forgotten text leaves the disk's git history too. It names how many commits go, and asks first. It refuses from an agent's process tree. |
| `check` | anyone | Validates every file: the front matter, the ids, the `supersedes` targets and `health.Sensitive`. It names what is wrong. |

### 4.3 The checks on `remember`: only the person's words, never a secret

1. **The source is text the person typed.** `--from <project>:<entry>` must name an entry that
   carries `typed=yes`, the field §10 step 1 adds. **`human=yes` is not enough:** §4.7 shows that
   agentbus also writes `human=yes` on texts it composes itself in the person's name. The entry
   is read from that project's stream, or from its `history` archive once the stream has trimmed
   it. An entry without `typed=yes` is refused, whatever its text. The person typing in busmon's
   Memory page (§4.4) is the other source, recorded as `said: … from busmon`.
2. **The quote is in the person's own text.** `--quote` must appear in the entry's typed text:
   the body of a typed directive or broadcast, or the `said` field of an ask's answer, never the
   question part of `"Answer to …"`. Whitespace is compared collapsed, and case is kept. The quote
   is the fact. There is no free-text paraphrase field: a paraphrase is where an inference would
   enter (the person, Q1). The topic, type and importance are master's filing; the person sees
   and can change them (§4.4).
   - **The question it answers** (the person, Q1): most decisions are short replies to a proposal
     ("1 English, 2 any code", "yes"), which mean nothing alone. `--answers <project>:<entry>`
     names the entry the person replied to, and `--answers-quote` quotes it. The checks are the
     same: the entry exists (stream or history); the quote is in its body (`command` for a `cmd`
     entry, `full` for a `report`); and `health.Sensitive` refuses. That entry is an agent's (master's or the
     concierge's), so it is stored as `question:`, never as the fact. For an ask's answer,
     `--answers` may be left out: the ask's own question header is stored as the question.
3. **No secret is written.** If `health.Sensitive` matches the quote, the question, the topic or a relation, the
   command refuses and writes nothing. It does not redact and keep the rest: redacting the matched
   span would keep the rest of the secret. The refusal names the field, never the value.
4. **A decision that may replace another.** If the topic already holds an active `decision`, a new
   `decision` without `--supersedes` or `--keep-both` is refused. The refusal names the old
   observation, so master asks the person which one holds.
5. **Size:** a quote of at most 600 runes (`health.SafeText`'s bound) and no control characters.
   Each topic file holds at most 200 observations; past that, the person splits the topic.

The limit: `human=yes` is as strong as the bus's own marking. On one host, any process of the
same user can write to Redis, so it is an audit trail, not a proof. That is the same limit the
Ideas page states.

### 4.4 busmon: view, edit, delete

There is a **Memory** page. It follows the Ideas page's pattern, a table plus one key per action:
- Topics are listed with their observations.
- `enter` shows a topic.
- `e` opens the topic file in `$EDITOR`. That is the person's own act. On save, `mem person
  check` runs, and a failed check is shown with the file left as saved.
- `x` forgets an observation, after a confirmation.
- `a` adds one typed by the person (the busmon source, §4.3.1).

The files live on the host. When busmon runs elsewhere and reaches the bus through the SSH tunnel,
it cannot read them. The page then says so, with the host's name, and offers nothing (question 3).

### 4.5 Master's discipline (skill text)

(Unchanged from r1, plus: when the person's words reply to master's proposal, `remember` names
that proposal with `--answers`.)

In `skills/agent-bus-master/SKILL.md`, and in the concierge's skill once it exists:

- **Recall before work.** At boot, master runs `agentbus mem person recall --project <p> --limit
  20`. Before a task the person describes, it runs `recall <the task's words>`. A recalled
  `decision` or `constraint` is followed, or the conflict is put to the person. It is never
  silently overridden.
- **Store on decision.** When the person states a decision, a preference or a constraint, master
  runs `remember` with the person's entry and their exact words, in the same turn. Nothing else is
  stored:
  - no conclusion of master's;
  - no other agent's report;
  - no summary of a discussion.
- **Report it.** Master's reply tells the person what was remembered and under which topic, in
  one line, so a wrong filing is seen at once.

### 4.6 The boundary with project memory

A person's decision that binds one project's code still becomes a project note
(`docs/memory/decisions/`), reviewed and committed. The person-memory observation names the
project, and may name the note in its topic's relations. The person-memory holds what is about
the person: their preferences, their ways of working, and decisions across projects. A
project's docs are never copied into it.

### 4.7 The check: how the person's words reach the bus today

The person asked whether messages sent from ntfy and Discord reach the bus marked
`human=yes`. Read from the code at `508f4290`:

- **ntfy and Discord carry nothing into the bus.** Both are send-only. `notify/senders.go`
  publishes to an ntfy topic and posts to a Discord webhook (`Send` is the only method each sender
  has besides its name and kind). Nothing in the module reads from ntfy or Discord: there is no
  subscription to a topic and no bot. The module's only listeners are local unix sockets, such as
  `termrec`'s. A reply typed on the phone stays on ntfy's server or
  in the Discord channel. The notifications spec deferred two-way
  (`2026-11-03-notifications-design.md`, "Later, two-way", and its question 7). It set the rule
  now: an answer from a phone may enter the bus only as busmon's own writes do (`Bus.Answer`, or
  a `human=yes` directive), identified by a pairing made at the desk, never by a phone number
  alone. So today **these messages can never be remembered**, because they never arrive.
- **What would have to change** is a receiver, its own later spec, not this one. It would write
  through the same functions as busmon, with `typed=yes` and a `via=ntfy` or `via=discord` field,
  after a pairing made at the desk:
  - **ntfy:** reading a topic is an HTTP stream of JSON lines, which the standard library reads,
    so it needs no new dependency.
  - **Discord:** a webhook cannot receive messages. Reading the channel needs a bot token and
    Discord's gateway, a WebSocket that the standard library does not speak. It would need a new
    dependency or a hand-written client. That is a question for that spec.
- **Where the person's words do enter today, all with `human=yes`:**
  - busmon's input line and compose window: `HumanCmd`, from `cmd/busmon/send.go:98`, or
    `HumanNotify` for a broadcast (`:93`);
  - an ask's answer: `Bus.Answer`, `bus/asks.go:325`. Its body is `"Answer to <question>:
    <answer>"`: the question is the agent's text, and only the answer is the person's.
- **And `human=yes` on texts the person did not type:**
  - `agentbus resume-all`'s crash summary, through `HumanCmd` (`cmd/agentbus/resumeall.go:221`);
  - the resume directive after a suspend, "Project resumed after a suspend…"
    (`bus/suspend.go:159`);
  - busmon's close, "closed from busmon by …" (`HumanClose`, from `cmd/busmon/tea_dialogs.go:245`);
  - the idea move line (`bus/ideas.go:208`);
  - a gate closed by a person (`bus/gates.go:93`);
  - `PersonNotice` on the `notify` stream.

  These mean "written in the person's name", not "the person's words". That is why §4.3.1
  requires the new `typed=yes`.

## 5. Layer 2: not a new archive

The person↔master threads are already archived per project by `history` (§2): the person's
directives in `cmd`, and master's answers in `report` to the person. They are redacted, kept 90
days, and searchable with `agentbus history --agent <person> --grep <words>`. Layer 2 therefore
needs no new JSONL file. If the concierge needs it, it needs one thing: **a search across
projects**, `agentbus mem search <words>`. That reads every project's history directory on the
host, keeps the entries from or to the person, and ranks as `recall` does. It is not specified in
detail here: it is "later and only if needed" (question 4).

## 6. The person's answers (2026-11-21)

1. **Quote only, no paraphrase:** yes, plus `--answers` for the question replied to (§4.3.2).
2. **Forget and git history:** no rewrite by default; `mem person purge` on the person's yes
   (§4.2).
3. **busmon:** the Memory page is host-only first.
4. **Layer 2:** `history` plus a cross-project `mem search`, built only when the concierge needs
   it.
5. **Today's agent memory:** left as it is; nothing is migrated.
6. **Names:** the 6 types, the 3 levels and the 4 relations stay as they are.

**One question left, raised by §4.7:**

7. **Statements made before `typed=yes` exists.** They carry only `human=yes`, which also marks
   texts the person did not write. Recommended: they are not sources. A past decision is re-entered
   by the person from busmon's Memory page (`a`), or confirmed in a new reply that master
   remembers.

## 7. Files a build changes

- `bus/stream.go`, `bus/asks.go`, `cmd/busmon/send.go`: the `typed=yes` field and an ask
  answer's `said` field (§10 step 1);
- a new package `personmem/`: files, parsing, ranking and checks, with tests;
- `cmd/agentbus/memperson.go` and its registration in `commands.go` (the `mem` command family);
- a busmon Memory page and its help line, after the Ideas page;
- `skills/agent-bus-master/SKILL.md` (§4.5);
- `README.md`: a short "Ideas from" section crediting MemoryGraph (MIT) and memory-mcp-server-go
  (its licence as §10 step 6 confirms it) for the ideas in §3, stating that no code was copied;
- `docs/operator-notes.d/person-memory.md`.

No setting is needed: the directory follows `XDG_DATA_HOME`, as backups do.

## 8. Tests a build owes

- `remember` refuses:
  - an agent's entry;
  - a quote that is not in the entry;
  - a credential-shaped quote, topic or relation, with no file or commit written;
  - a second active `decision` without `--supersedes` or `--keep-both`;
  - an oversized quote.
- The same entry and quote stored twice gives one observation.
- `forget` refuses from an agent's process tree.
- `recall`'s order follows §3's priority and its tie-breaks. Superseded observations are hidden
  without `--all`.
- `check` finds a dangling `supersedes`, a bad id and a secret a hand edit added.
- No test writes under the real `XDG_DATA_HOME`: every test sets it to `t.TempDir()`.

- `typed=yes` is written by busmon's input line, compose window and broadcast, and by an ask's
  answer. It is written by none of the six machine-composed writers of §4.7: one test per writer.
- `remember` refuses an entry with `human=yes` and no `typed=yes`, such as a resume-all summary.
- For an ask's answer, a quote taken from its question part is refused.
- `--answers` refuses a missing entry, a quote not in its body, and a credential-shaped quote.
- `purge` refuses from an agent's process tree. After it, the repository has one commit holding
  the current files.

## 9. Not doing

- no database, no index file, no embeddings, no MCP server;
- no automatic extraction from conversations; MemoryGraph's `memory-save` prompt is left out;
- no scores an agent would compute;
- no sync between hosts: the repository is local. Copying it is the person's own git remote, if
  ever.

## 10. Build steps for coder

One PR per step, each from `origin/dev`, in this order. Steps 2 to 5 depend on step 1; steps 3 to 5 also depend on step 2.

1. **`typed=yes` (bus, additive).** Add an optional `typed` argument to the person-write paths:
   - busmon's `sendPlan.exec` (`cmd/busmon/send.go`) writes `typed=yes` on its `HumanCmd` and
     `HumanNotify` entries.
   - `Bus.Answer`'s script (`bus/asks.go:325`) writes `typed=yes` and a new field `said` holding
     exactly the chosen label or the free text.

   The other `human=yes` writers of §4.7 do not change. `bus.Entry` gains `Typed bool` and
   `Said string`. The `subscribe` JSON gains `typed` additively, within its version. This is a
   protocol change in `bus/`, the project's rule.
2. **`personmem/` (the files).**
   - Parse and write a topic file (§4.1), one git commit per write through `mission.Git`'s way of
     running git.
   - `check`, and `recall`'s ranking (§3).
   - The checks of §4.3, against an entry reader that is an interface: the stream, then the
     history archive.
   - Tests under `t.TempDir()` as `XDG_DATA_HOME`.
3. **`agentbus mem person …`** (`cmd/agentbus/memperson.go`): every verb of §4.2, with `forget`
   and `purge` refused by `agentcaller.Refuse`. `purge` asks for a typed yes, and `--yes` is
   accepted only outside an agent.
4. **busmon's Memory page** (§4.4), after the Ideas page's pattern, host-only.
5. **Master's skill and the docs:**
   - `skills/agent-bus-master/SKILL.md` (§4.5);
   - `docs/operator-notes.d/person-memory.md`;
   - the README credit (step 6).
6. **The licence check before the README credit:** MemoryGraph is MIT (its `LICENSE`).
   memory-mcp-server-go has no LICENSE file in the checkout, only a README badge. The credit
   names it "for ideas" without stating a licence, unless its repository's licence file is
   confirmed.

Not in these steps: layer 2's `mem search`, and the ntfy/Discord receiver (§4.7). Each needs its
own spec when the person asks.
