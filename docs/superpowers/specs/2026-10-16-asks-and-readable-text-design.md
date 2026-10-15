# v0.14 — Questions for a person, and text people can read

Status: draft for review (Grok), 2026-10-16.

## 1. Problem

On hyperliquid, master needed three decisions from Bernard. It declared `waiting` with "see my
last report"; the report was one 1 500-character paragraph holding the context, three decisions
and its recommendations. Bernard answered one of them in a directive ("Ok pour l'amendement");
master then changed its recommendation for another in a `notify` nobody reads, and never updated
its status. Bernard saw three open questions where two remained, could not tell which, and had to
scroll an unformatted Activity window to find them.

Two causes: a question is free text inside a report, so nothing knows what was asked, what was
answered and what changed; and every long text is rendered as a flat block.

## 2. Goals and non-goals

Goals:
- An agent asks a person **structured questions** — one or several, each with options and a
  recommended one — the way Claude Code asks its user.
- busmon shows them like Claude Code: one tab per question, options to pick, free text always
  possible; **each answer is sent as soon as it is given**; an answered question never comes back.
- When the agent changes its mind, it **replaces** the open questions; busmon shows the new version.
- Long texts (reports, status messages, question context, message details) render as **Markdown
  readable in a terminal**: headings, lists, paragraphs, emphasis, code spans.

Non-goals: questions between agents (they have directives, requests and the 4-eyes flow);
choosing several options in one question (one choice or free text per question); rendering
tables or images.

## 3. The ask

An ask is one agent's open questions to people, stored in `{p}:asks` (hash, ask id → JSON). The id
is the Redis stream id of the `ask` cmd entry that announced it (§4), so it orders by time and
doubles as the thread id.

```json
{"id": "1792100000000-0", "from": "master", "title": "Opening of DCA act 4",
 "created_at": 1792100000000, "state": "open",
 "questions": [
   {"id": "t", "header": "Date T",
    "question": "When should the new DCA session open?",
    "context": "Act n4 is approved (4d5d51b). Opening must be ≥ 48 h after its merge.",
    "options": [
      {"label": "Mon 19/10 10:00 UTC", "description": "Before the 12–13 h DCA slice; read on 15/12.", "recommended": true},
      {"label": "Wed 21/10 10:00 UTC", "description": "Two more days of margin."}],
    "answer": null},
   {"id": "sha", "header": "Code", "question": "Which code runs during the window?", "options": [...], "answer": null}
 ]}
```

Rules (validated on write, rejected otherwise):
- 1–6 questions; each 1–6 options; `header` ≤ 16 characters, `label` ≤ 60, `question` ≤ 300,
  `description` ≤ 300, `context` ≤ 2 000, `title` ≤ 100; question ids unique, `ValidName`-like
  (`^[a-z][a-z0-9_-]{0,31}$`). Text passes `ValidateText`; `context` may carry Markdown (§7).
- At most one option per question is `recommended`. Free text is always allowed: it is not an
  option, busmon offers it on every question.
- `state`: `open` (some question unanswered), `answered` (every question answered), `replaced`
  (superseded by `replaced_by`), `withdrawn` (the agent cancelled it).
- An answer: `{"choice": "<label>"}` or `{"text": "<free text>"}`, plus `by` (the person) and `at`
  (Redis `TIME`, ms). Once set it never changes (a second answer is refused).

## 4. Bus operations (`bus/asks.go`)

All writes are one Lua script each, so a racing answer and a replacement cannot both win.

- **Ask** — `Bus.Ask(ctx, agent, ask)`: XADDs a cmd entry `type=ask`, `from=<agent>`,
  `target=<person or "people">`, `wake=no`, `ref=<its own id>`, command = the title; then stores the
  ask under that id with `state: open`, and declares the agent `waiting` with the message
  `N question(s): <title>` (the same `putSnapshot` path as `status`). Returns the id.
- **Replace** — `Bus.Ask(ctx, agent, ask, replaces=<old id>)`: the old ask must be the agent's and
  `open`; its already answered questions are **carried over** into the new ask by question id
  (same id = same question: the answer stays), the old one becomes `replaced` with `replaced_by`.
- **Answer** — `Bus.Answer(ctx, askID, questionID, person, answer)`: refused unless the ask is
  `open` and the question unanswered; sets the answer, sets `state: answered` when it was the last
  one, and XADDs **in the same script** a cmd `type=reply`, `ref=<ask id>`, `from=<person>`,
  `target=<agent>`, `human=yes`, command
  `Answer to "<header>": <choice or text>` (+ ` (N question(s) still open)`). The reply wakes the
  agent through the dispatcher like any directive. Nothing waits for the other questions.
- **Withdraw** — `Bus.Withdraw(ctx, agent, askID)`: the agent closes its own open ask.
- **Read** — `Bus.Asks(ctx)` (all, newest first); answered, replaced and withdrawn asks are kept 7
  days (`expire_at` in the JSON, trimmed by the next write), so busmon can still show what was
  answered.

`ask` joins `validCmdTypes`; `asks` joins `projectKinds`. `subscribe`/`--drain` deliver the reply
with `ref` = ask id, so an agent knows which ask it answers (no new protocol field: additive, `v`
unchanged). A `wake=no` ask entry never wakes anyone; the reply does.

## 5. CLI (`agentbus ask`)

```bash
agentbus ask <agent> --file ask.json            # or the JSON on stdin; prints the ask id
agentbus ask <agent> --replace <id> --file …    # replace an open ask (answered questions carried)
agentbus ask <agent> --withdraw <id>
agentbus asks [--open] [--json]                 # list; what each question got
agentbus answer <id> <question> (--choice <label> | --text <words…>)   # a person without busmon
```

`agentbus ask` validates before writing and prints the errors (field, limit). The agent's state
becomes `waiting`; once every question is answered, the agent declares `working` again itself
(the declared state is never rewritten by the bus).

## 6. busmon

- **Header**: `♙ N waiting for you` counts the questions still open across asks (not agents).
- **Agents panel**: an agent with an open ask keeps the fading `waiting` (v0.12.4); its detail
  starts with the open ask (title, then each question's header and state) above the status message.
- **The ask panel**: Enter on an agent with an open ask (or `?` from anywhere when one is open, the
  oldest first) opens it as an overlay:

  ```
  ╭─ ♙ master asks · Opening of DCA act 4 ───────────────────────────────╮
  │  ✓ Amendment   ● Date T   ○ Code                       ←/→ question  │
  │                                                                      │
  │  When should the new DCA session open?                               │
  │  Act n4 is approved (4d5d51b). Opening must be ≥ 48 h after its      │
  │  merge.                                                              │
  │                                                                      │
  │ ▶ 1. Mon 19/10 10:00 UTC  (recommended)                              │
  │      Before the 12–13 h DCA slice; read on 15/12.                    │
  │   2. Wed 21/10 10:00 UTC                                             │
  │      Two more days of margin.                                        │
  │   3. Other… (type your answer)                                       │
  │                                                                      │
  │  ↑↓ choose · Enter answer · 1-9 quick pick · Esc close               │
  ╰──────────────────────────────────────────────────────────────────────╯
  ```

  - Tabs: one per question, `✓` answered (its answer shown in place of the options, read-only),
    `●` the current one, `○` open. ←/→ (or Tab) moves between them; the panel opens on the first
    open question.
  - ↑/↓ and digits pick an option; Enter sends **that answer now** (`Bus.Answer`) and moves to the
    next open question; the last one closes the panel. "Other…" turns the bottom line into an input
    (Enter sends, Esc back to the options).
  - A refused answer (already answered by someone else, ask replaced) reloads the ask and says why.
    A replaced ask opens on its replacement.
  - The panel's text uses the Markdown renderer (§7); options and descriptions wrap with a hanging
    indent. All glyphs from the closed set; every frame passes the alignment suite.
- **Activity**: an `ask` entry reads `master asks · Opening of DCA act 4 (3 questions)`; a reply on an
  ask reads `Bernard → master · Date T: Mon 19/10 10:00 UTC`.
- **Requests**: open asks are what is "for you" (v0.12.7 count includes their open questions).

## 7. Markdown readable in a terminal (`cmd/busmon/markdown.go`)

A small renderer for overlay bodies (never the one-line rows): input text → wrapped lines of
role-tagged segments at a given width.

- `#`, `##`, `###` headings: accent, bold; a blank line before.
- `-`, `*`, `+` and `1.` lists: a bullet / number, wrapped with a hanging indent; nested by 2 spaces.
- Paragraphs: consecutive lines joined and re-wrapped; blank lines kept (at most one in a row).
- `**bold**`, `*italic*` (shown as plain text: terminals vary), `` `code` `` (muted).
- Fenced code blocks: kept verbatim, muted, not wrapped (cut with `…`).
- `> quote`: muted with a `│` gutter.
- Anything else is text. No HTML, no links rendering (a URL stays as text), no tables.
- The renderer only reorders whitespace and styles; it never drops characters (except the
  Markdown markers themselves) and passes text through the same sanitiser as today (`DetailText`).

Used for: report full text (agent detail, message detail), status message, ask `context`,
directive and reply bodies in the message detail. One-line previews (Activity rows) stay as today.

## 8. Skills

- master (`agent-bus-master`): every decision for a person is an `agentbus ask` — never a question
  inside a report or a `notify`. One question per decision, options with a recommended one and the
  reason in its description, `context` short (the full analysis stays in a report, which the context
  may cite). When an answer arrives, act on it; when the recommendation changes, `--replace`. When
  nothing is open any more, declare `working`.
- every agent (`agent-bus`): write reports for people — a `## heading` per part, lists for items,
  short paragraphs; never one block.

## 9. Security and limits

- An ask is agent text shown to a person: same sanitising as every detail (`DetailText`); no
  terminal escape reaches the screen; no command is run from an answer.
- Answers are the person's words sent to an agent; they carry `human=yes` like every busmon write.
- One person per answer: the first answer wins, a later one is refused and shown.
- No notification beyond v0.12.3's (fade, header count, one bell per new ask).

## 10. Tests

- Validation: limits, unique ids, one recommended, empty options, bad names.
- Bus (test Redis): ask stores, declares waiting and writes a `wake=no` entry; answer sets once,
  refuses a second answer and an answer to a replaced/withdrawn ask, writes the reply with `ref` in
  the same script; last answer → `answered`; replace carries answered questions by id and refuses a
  foreign or closed ask; withdraw; expiry trims old closed asks.
- Dispatcher: an `ask` entry wakes nobody; the reply wakes the agent.
- busmon: the panel's tabs, option pick, digits, Other input, send-on-Enter, refused answer reload,
  replaced ask, header count of open questions; Activity rows; alignment suite with a hostile ask.
- Markdown renderer: headings, nested lists with hanging indent, paragraph re-wrap, code block
  verbatim, emphasis markers removed, width edge cases, no character lost, sanitising kept.
- CLI: `ask` from file/stdin with error messages, `--replace`, `--withdraw`, `asks`, `answer`.
