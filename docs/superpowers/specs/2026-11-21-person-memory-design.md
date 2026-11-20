# The person's long-term memory, for master and the concierge

Status: spec r1, for review (subject `person-memory`). Not built. Base: origin/dev `4a7853c9`.
Author: architect, from the person's directive of 2026-11-21 (thread `1795218784752-0`), relayed by
master (cmd `1795218795667-0`). It is a read-only, time-boxed study of two checkouts, which were
not modified:
- `/data/AI-Docs/memory-graph`: MemoryGraph, MIT, read for ideas;
- `/data/AI-Docs/memory-mcp-server-go`: MIT, read for its Go shape.

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
| `human=yes` on a bus entry (`bus.HumanCmd`, `bus/stream.go:1128`) | Marks a directive the person wrote from busmon. Agents and busmon tell it apart from an agent's. | **The provenance check** (§4.3). |
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

- id: 20261120-1795206037042-0
  type: decision
  importance: critical
  said: 2026-11-20 by the person, project agent-bus-monitor, entry 1795205976983-0
  quote: "PRs go to dev; main takes only promotions"
  supersedes: 20261116-1794800000000-0
```

`kind` and `relations` are the entity's. The bullets are its observations. An observation's `id`
is the date plus the source entry's id, so the same statement is never stored twice.

### 4.2 Verbs

All the verbs sit under `agentbus mem person`, beside the existing `mem check` and `mem sync`. They
need no `--project`.

| Verb | Who | What |
|---|---|---|
| `remember <topic> --type T --importance I --from <project>:<entry> --quote "<words>" [--supersedes <id>] [--kind K] [--relates <rel>:<topic>]` | master, the concierge, the person | Adds one observation, after the checks in §4.3. Each write is one git commit. |
| `recall <words> [--type T] [--project P] [--all] [--limit N] [--json]` | anyone | Ranks observations as in §3: exact topic, then partial topic, then type, then quote. Ties go to importance, then the newest. Superseded observations are hidden unless `--all` is given. |
| `show <topic>`, `list` | anyone | One topic in full; the topics with their counts. |
| `forget <id or topic>` | **the person only** | Deletes the observation or the topic file, then commits. It refuses from an agent's process tree (`agentcaller.Refuse`). |
| `check` | anyone | Validates every file: the front matter, the ids, the `supersedes` targets and `health.Sensitive`. It names what is wrong. |

### 4.3 The checks on `remember`: only the person's words, never a secret

1. **The source is the person's own entry.** `--from <project>:<entry>` must name a `cmd` entry
   with `human=yes` (§2). The entry is read from that project's stream, or from its `history`
   archive once the stream has trimmed it. An entry from an agent is refused, whatever its
   text. The person typing in busmon (§4.4) is the other source, recorded as `said: … from
   busmon`.
2. **The quote is in it.** `--quote` must appear in the entry's body. Whitespace is compared
   collapsed, and case is kept. The quote is the fact. There is no free-text paraphrase field:
   a paraphrase is where an inference would enter. The topic, type and importance are master's
   filing; the person sees and can change them (§4.4).
3. **No secret is written.** If `health.Sensitive` matches the quote, the topic or a relation, the
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

## 5. Layer 2: not a new archive

The person↔master threads are already archived per project by `history` (§2): the person's
directives in `cmd`, and master's answers in `report` to the person. They are redacted, kept 90
days, and searchable with `agentbus history --agent <person> --grep <words>`. Layer 2 therefore
needs no new JSONL file. If the concierge needs it, it needs one thing: **a search across
projects**, `agentbus mem search <words>`. That reads every project's history directory on the
host, keeps the entries from or to the person, and ranks as `recall` does. It is not specified in
detail here: it is "later and only if needed" (question 4).

## 6. Questions for the person

1. **Quote only?** An observation is the person's exact words, with master's filing (topic, type,
   importance) and no paraphrase field. Recommended: yes. A paraphrase is where an inference gets
   in; the person can still rewrite the file in busmon (`e`).
2. **Forget and git history.** `forget` deletes the observation and commits. The old text stays in
   the local repository's history. Should `forget` also rewrite that history, by rebuilding the
   repository from the current files? Recommended: no, by default, plus a `mem person purge` that
   does so on the person's yes, for a statement that must leave the disk.
3. **busmon away from the host.** Should the Memory page be host-only, or should the dispatch
   service mirror a read-only copy into Redis so that a remote busmon can show it? Recommended:
   host-only first. A copy in Redis is one more place for the person's words to live.
4. **Layer 2:** confirmed as `history` plus a cross-project `mem search`, built only when the
   concierge needs it? Recommended: yes.
5. **Today's agent memory:** leave Claude Code's per-project memory as it is, the agents' drafts,
   with nothing migrated? Recommended: yes. Master may `remember` a past statement only from its
   bus entry, through the same checks.
6. **Names:** the 6 types, the 3 importance levels and the 4 relations of §3. Are these the right
   words for what you say? Add or drop any.

## 7. Files a build changes

- a new package `personmem/`: files, parsing, ranking and checks, with tests;
- `cmd/agentbus/memperson.go` and its registration in `commands.go` (the `mem` command family);
- a busmon Memory page and its help line, after the Ideas page;
- `skills/agent-bus-master/SKILL.md` (§4.5);
- `README.md`: a short "Ideas from" section crediting MemoryGraph (MIT) and memory-mcp-server-go
  (MIT) for the ideas in §3, stating that no code was copied;
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

## 9. Not doing

- no database, no index file, no embeddings, no MCP server;
- no automatic extraction from conversations; MemoryGraph's `memory-save` prompt is left out;
- no scores an agent would compute;
- no sync between hosts: the repository is local. Copying it is the person's own git remote, if
  ever.
