# busmon UX: waiting, @-completion, readable reports, unread marks — design

Status: draft for review (coder, 2026-10-21). Task `busmon-ux-spec`, branch `docs/busmon-ux-spec`
from `origin/main` `56a1699`. Spec only; no code in this change. Items numbered as Bernard gave
them (5–8), from his screenshots of busmon's agent overlay for `master`.

Every colour below is a theme role and every mark a glyph of the closed set in `theme.go`; no new
glyph is introduced.

---

## 5. "awaiting bernard" declared as `idle`

### Current behaviour

- master declared `idle` with the message `lot 1 delivered; awaiting bernard`. Bernard expected
  `waiting`.
- The master skill already says the opposite of what he expected: "`waiting` means a person must
  decide **now** … Declare it only while one of your asks is open. Waiting for a future instruction,
  or for a date, is `idle`" (`skills/agent-bus-master/SKILL.md`, "When you need the human's
  decision"). The peer skill says the same: "`waiting` … Use it only for the human (usually master
  does); waiting for a peer is `idle`" (`skills/agent-bus/SKILL.md`).
- busmon shows `idle` as declared and never relabels it (`stateLook`, `cmd/busmon/agents.go`): an
  idle agent whose message says it awaits the person draws no attention at all.

So master followed the rule; the rule leaves a gap. "Lot 1 delivered; awaiting bernard" is not
"nothing to do": it is "your turn" — a review, a go, a merge. That is a decision, and decisions are
asks.

### Options

| | Option | Verdict |
|---|---|---|
| A | Skill: when the next step is the person's (a review, a go, a merge), master opens an ask (it then reads `waiting`); `idle` only when nothing is expected of the person | **Yes**: the fix at the source |
| B | busmon hint: an `idle` agent whose status message names the person gets the flag `for you` | **Yes**, as a safety net |
| C | `agentbus status <a> idle <msg>` prints a stderr note when the message names a person | Later, if A and B do not suffice; the name list is busmon's knowledge, not the CLI's |
| D | Relabel such an agent `waiting` in busmon | No: busmon never overwrites the declared state (CLAUDE.md, "Observed is not declared") |

### Recommendation

- **Skill (A)**, master skill, same section, one rule added: *"Delivered and the next step is the
  person's (review, go, merge, test)? That is a decision: open an ask with the options (`go`,
  `changes`, `later`), which declares you `waiting`. Declare `idle` only when nothing is expected of
  the person."* The peer skill gains: *"Need the person? Tell master; never declare `waiting`
  yourself without an open ask."*
- **busmon hint (B)**: in `agents.go`'s flags, an agent declared `idle` whose status message
  contains the person's name as a whole word, case-insensitive, gets the flag `for you`
  (`RoleAttention`). busmon already knows the name: `humanIdentity`
  (`--as`/`AGENT_BUS_HUMAN`/`busmon.toml`, `human.go`). **The hint is off while the identity is
  the default `pilot`** (`defaultHuman`, `human.go`): `pilot` is a bus word (the pilot lease,
  "piloted by"), so matching it would misfire for anyone who has not set a name. No bell, no fade, no count in the header:
  those stay `waiting`'s, so a hint can never ring. The agent detail adds a muted line under the
  status message: `It names you but declared idle: no question is open.`

---

## 6. `@agent` completion in the line and the compose window

### Current behaviour

Completion exists, but the two inputs differ and the compose text has none:

- **The one-line input** (`i`, `:`): `u.input.SetAutocompleteFunc` (`ui.go`) calls
  `agentCompletions` (`render.go`): it offers `@<name> ` for every agent busmon has ever seen
  (`u.st.agents`, the dormant and unregistered ones and yourself included) and **never `@all`**.
- **The compose window** (`I`, `Ctrl+O`): only its `to:` field completes, through
  `recipientCompletions` (`compose.go`) over `u.agentNames()` (`human.go`: visible, registered,
  not yourself) plus `all`. The text area (`tview.TextArea`) has no completion: an `@ma` typed at
  the start of the text is not completed. Opening the window from the line moves a leading `@name`
  into `to:` (`splitRecipient`), but typing it in the window does not.

### Recommendation

1. **One candidate list:** `u.agentNames()` plus `all`, in both inputs. While `a` (show all) is
   on, the list also offers the dormant agents the panel then shows, so writing to one stays easy;
   `@all` still reaches only the visible ones, as today. `agentCompletions` and
   `recipientCompletions` become one function: `@` + prefix → `@<name> ` (space included, like the
   line today), `all` listed first. `@` alone lists everyone.
2. **The same keys everywhere:** tview's list opens as you type; `Tab` or `Enter` takes the
   highlighted entry, `Esc` closes the list and keeps the text. In the line, `Enter` with the list
   open completes and does **not** send: the completion is taken before `inputDone`
   (`ui.go`) runs, and `keys.go`'s ModeInput table documents the key.
3. **Compose text:** when the text is only `@<prefix>`, `Tab` makes it the recipient: completed
   into `to:` when one name matches (the focus stays on the text), else typed in `to:`, whose list
   then offers the choices. **The text is never read as a recipient when sending**: `ui.send` takes
   recipient and body as fields, and `TestComposeNeverRebuildsTheRecipient` keeps that invariant
   (amended at implementation, `feat/busmon-ux`). The `to:` field also completes a bare prefix
   (`ma`), since it holds only a recipient; an empty `to:` opens no list, so Tab there still moves
   to the text.
4. The footer and help (`keys.go` drives both) gain `Tab complete @name`.

---

## 7. "Last report" is a wall of text

### Current behaviour

- The overlay already renders Markdown: `agentDetail` (`detail_agent.go`) passes the agent's
  newest report to `renderMarkdown` (`markdown.go`), using the **full** retained text when there is
  one (`lastReport`: `e.Full`, else the preview).
- The wall comes from the input: a report written as one paragraph has no line breaks to render.
  `structureFlat` rescues only three patterns (a shouted first sentence, `Label:` runs, `(1) (2)`
  items). Free prose stays one block. The full text is also kept only when it differs from the
  one-line preview (CLAUDE.md, "`report` keeps a bounded preview AND the full text"), so a long
  single-line report has no full text at all.
- The peer skill has a section "Write reports people can read" (headings, lists, short paragraphs).
  The **master skill has none**, and the screenshot was master's report.

### Options

| | Option | Verdict |
|---|---|---|
| A | Skills: one report shape for every role | **Yes** |
| B | `agentbus report` prints a stderr note for a report over 300 characters with no line break: `busmon renders Markdown: a summary line, then - items` | **Yes**, cheap, teaches at the moment it matters; the report is still sent |
| C | Make `structureFlat` split long prose into sentences | No: guessing structure from prose turns abbreviations and code into false breaks |

### Recommendation

- **The shape (A)**, in the agent-bus skill's section and copied into the master skill:

  ```markdown
  <one-line summary: what happened>

  ## Done
  - <item>, with `ids` and commits in code

  ## What remains for you
  - <the person's next step, or "nothing">
  ```

  "What remains for you" is the line busmon's overlay is opened for. When the remaining step is a
  decision, it is an ask (item 5), and the report says `see the open question`.
- **The CLI note (B)** in `cmd/agentbus` (`report`), stderr only, exit unchanged.
- busmon itself needs no change for this item. The overlay's existing heading, `What <agent> needs
  from you`, applies only to `waiting` agents, and stays so.

---

## 8. Unread marks in Activity for what is addressed to the person

### Current behaviour

- Activity rows (`activityRow`, `activity.go`) carry time, from, to, type and text; nothing marks
  what the person has or has not read.
- busmon already keeps local, per-project, per-person state in `~/.cache/agentbus/busmon/<p>.json`
  (`localcache.go`: `last_visit`, `snoozes`; mode `0600`, written atomically).
- What counts as answered already exists for `waiting`: `waitingAnswerAt` (`waiting.go`) finds the
  first later human message to the agent, or a human reply in a thread it started.

### What "addressed to the person" means

An Activity entry is **for the person** when it is one of:

1. an `ask` (`target=people`; the ask's own panel stays the place to answer it);
2. a `cmd` whose target is the person's identity name (an agent that writes to `bernard`
   directly; `people` is only the ask target, kind 1), and never while the identity is the default
   `pilot`;
3. the newest report of an agent at the moment it declared `waiting` (the text the overlay shows as
   `What <agent> needs from you`), or declared `idle` with the `for you` hint (item 5);
4. a `notify` marked **`for=person`**. Today a notify entry carries only `from` and `message`,
   and the dispatcher writes three kinds from `dispatch`: the escalation for the person when the
   target cannot act (`loop.recipientCannotAct`, `dispatch/loop.go`), the long-hold notice for
   master's own provider (`dispatch/resume.go`), and the trim-gap notice (`dispatch/loop.go`),
   which is not for the person. Text matching cannot tell them apart, so the first two fenced
   writes gain an additive field `for=person` (and `bus.Event` a `For` field read by
   `ParseEntry`). **This is a protocol addition** on the `notify` stream: additive by key, so
   old readers ignore it and the versioned `subscribe` contract is untouched (notify entries are
   never delivered by `subscribe`). Older notifies have no field and are never marked.

Reports and directives between agents are not for the person, whoever reads them.

### The mark and its states

A one-cell column before the time, in every tier; it takes its cell from the message, which
already takes the rest of the row (`colsFor`, `activity.go`). `GlyphDotFull` and `GlyphDotEmpty` are aliases of `GlyphWorking` and
`GlyphIdle` (`theme.go`): in the Agents panel they mean working and idle. In Activity they sit in
their own column, which has no other use, so they cannot be read as a state. (Folding the mark
into the time's first digit in the narrow tier was rejected: `●5:04` hides whether it is 05 or 15.)

| State | Glyph | Role | When |
|---|---|---|---|
| unread | `GlyphDotFull` (●) | `RoleAttention` | the entry is for the person and not opened yet |
| superseded | `GlyphDotEmpty` (○) | `RoleMuted` | not opened, but a newer entry for the person from the **same** sender arrived after it, or the wait it belonged to was answered (`waitingAnswerAt`), or the ask was closed |
| read | none | — | opened |

- **Opened** means the person opened that line's detail (Enter or a click on the selected line).
  Scrolling past it is not reading. An answer (an ask answered, a reply in its thread) makes the
  entry **superseded** (○), as the table says, not read: only opening clears the mark (amended at
  implementation).
- Read state is saved on the event loop when a line is opened (`busmonCache.save`,
  as the recap saves `last_visit`), never from `Draw`, which does no I/O.
- Read state is stored in the same local cache, as `read: {<project>: [<entry id>…]}`, capped at
  the ids still in the retained window (older ids are dropped on save, as expired snoozes are).
  Nothing is written to Redis: reading is the person's, not the bus's, and two busmons of one
  person on two machines may disagree. That is acceptable for a mark.
- The header counts unread entries next to the existing attention counts, as `● N`
  (`RoleAttention`), only when N > 0; `R` (the recap) lists them first.
- A key, `u`, jumps to the next unread line (and wraps); the help and the footer come from
  `keys.go`, as every key does.

### Options considered

| | Option | Verdict |
|---|---|---|
| A | Local read state in busmon's cache | **Yes**: nothing about reading goes on the bus, survives restarts (the only bus addition is kind 4's `for=person` field) |
| B | Read receipts on the bus (`{p}:read`) | No: a second person, or busmon on a laptop, would mark the other's reading; and it is a new shared key for a personal fact |
| C | Only a filter "for me" without marks | Part of the v0.22 roadmap; the marks make the filter cheap to add later (the same predicate) |

---

## Tests

All unit tests, no broker unless stated. busmon's existing suites stay the gate:
`TestAlignmentSuite` (every size × palette × hostile fixture) and `TestNoStrayNonASCIILiterals`.

- **5:** `forYouHint(state, message, person)` table: idle + name as a word → true; `bernardo`,
  name inside a URL or a path → false; `waiting`/`working` → false; identity `pilot` (the default) → false whatever the message. The
  detail line appears only with the flag. Alignment fixtures gain an agent with the flag in every
  tier. A skills test (`tests/skills_test.sh`) asserts the master skill's ask rule.
- **6:** one completion function: `@` → `@all ` first then sorted names; `@ma` → `@master `;
  yourself and an unregistered agent never offered; a dormant one offered only with `a` on; a text with a space → none.
  `keys.go`: in ModeInput, Enter with the list open completes and does not call `inputDone` (a
  `ui` test with a simulated screen, as the existing input tests do). Compose: `@ma` + Tab in the
  text completes; send with `@master hi` in the text and an empty `to:` → recipient `master`,
  body `hi`.
- **7:** `agentbus report` with 301 characters and no newline → the stderr note and exit 0; with a
  newline or ≤300 → no note. The skills test asserts the shape's three parts in both skills.
- **8:** `forPerson(event, person, waitingAt)` table for the four kinds, and the negatives
  (agent→agent directive, report not tied to a wait, notify from an agent, the dispatcher's
  trim-gap notify, a notify without `for`). On the test broker: the two dispatcher writes carry
  `for=person` and the trim-gap one does not; `ParseEntry` reads the field. `markState`: unread →
  superseded on a newer entry from the same sender, on an answer, on a closed ask; → read when
  opened; a newer entry from **another** sender does not supersede. The cache round-trip keeps only
  retained ids and stays `0600`. Alignment fixtures gain unread and superseded rows in every tier
  and palette (the 16-colour and no-colour palettes must still tell the two apart: the glyph
  differs, not only the colour).

## Review

foureyes on `597f61f`: citations confirmed. Changes requested: kind 4 could not tell the dispatcher's
notices apart (now an explicit additive `for=person` field, stated as a protocol addition), and the
hint misfired on the default identity `pilot` (now off for it). Also addressed: where read state is
saved, the dot glyphs' aliases, dormant agents in completion with `a`, kind 2 limited to the
person's name, and `inputDone`'s file. Open questions 1–3 were answered as proposed.

## Open questions for review

1. Item 5's hint matches the person's identity name only, not words like `you`/`awaiting`.
   Proposal: name only (precise; the skill rule does the rest).
2. Item 8's mark also on entries older than the busmon session (backfilled)? Proposal: yes, unless
   older than the cache's `last_visit`, so a long absence shows what came in, and a first launch
   does not mark the whole backfill.
3. Key `u` for "next unread": free in the panel table today (`keys.go`). Proposal: `u`.
