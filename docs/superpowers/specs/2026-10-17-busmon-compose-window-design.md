# busmon compose window (v0.16) — design

Status: draft rev 1, 2026-10-17. Asked by Bernard; options settled with him and Kimi.

## 1. Why

busmon's input is one line (`u.input`, a `tview.InputField`, `cmd/busmon/ui.go`). It is enough
for `@coder go`, not for the ten-line directive a person most often sends master, which scrolls
sideways and cannot be reread before sending. The free answer in the question panel is worse: a
hand-rolled `[]rune` with Backspace only (`askKey`, `cmd/busmon/askpanel.go`), no cursor movement,
no paste across lines.

## 2. Decisions taken with Bernard

| # | Option | Decision |
|---|---|---|
| 1 | Multi-line window, Enter = new line, **Ctrl+S = send** | yes |
| 2 | Recipient in the window's header | yes |
| 3 | Draft kept on Esc, shared with the one-line input | yes |
| 4 | Markdown preview | **no** (no use) |
| 5 | External `$EDITOR` | **no** (the built-in editor must be enough) |
| 6 | Reply in a thread from an Activity line | yes, **separate lot** (§8) |
| 7 | The same editor for a free answer in the question panel | yes |

Kimi's review adds: one send path shared by the line and the window (never duplicated); the
message type is inferred, not a second field; the counter counts runes, like the bus.

## 3. The window

A modal overlay (the modal layer of `ui.go`, like the question panel), centred, its frame in the
Activity hue (`panelHue`), width `min(screen − 4, 100)`, height `min(screen − 6, 16)` with a
minimum of 6 text lines; in the narrow tier it takes the whole screen less the header.

```
┌ Compose ───────────────────────────────── to: @master · directive ┐
│ Reprends task39 après la revue de foureyes.                       │
│ - pas de déploiement VDR sans mon GO                              │
│ - pousse main quand c'est fusionné                                │
│                                                                   │
├───────────────────────────────────────────────────────────────────┤
│ Ctrl+S send · Tab recipient · Esc close (draft kept)              │
└──────────────────────────────────────────────────── 142 / 65536 ──┘
```

- **Editor:** `tview.TextArea` (present in tview v0.42.0, `go.mod`): wrap, cursor keys, Home/End,
  word moves, selection, undo, paste. Its own bindings stay as they are (Ctrl+E is its end of
  line, Ctrl+A its start). Enter inserts a new line.
- **Header:** `to: <recipient> · <type>`. The type is read from the recipient: an agent or `@all`
  → `directive`; empty → `broadcast` (shown in the attention colour). No type field to edit.
- **Footer:** the window's bindings from the key table (§5), as everywhere else.
- **Counter:** runes of the trimmed text / the limit (`bus.CmdMaxRunes` for a message, the
  answer limit for an answer, §6); at the limit the editor accepts no more input
  (`SetMaxLength` counts what the bus counts: verify, else enforce on change) and the counter
  turns attention.
- **Glyphs:** only the existing set (`TestNoStrayNonASCIILiterals`); the frame passes
  `TestAlignmentSuite` at every size and palette (a new fixture: an open compose window).

## 4. Recipient

- `Tab` moves the focus from the text to the recipient field (one line, the current
  autocompletion of `@agent`/`@all` from `parseDirected` and `agentNames`); `Tab` again returns to
  the text. Enter in the recipient field returns to the text too (it never sends).
- Opening rules: from the line, the recipient is the `@name` the line starts with (and the body
  is the rest); from a panel with an agent selected (Agents panel, or its detail), that agent;
  otherwise empty.
- A recipient that is neither a known agent, `all`, nor empty is shown in the attention colour and
  Ctrl+S refuses with a flash (`unknown recipient`), the draft kept.

## 5. Keys

New mode `ModeCompose` with its own table in `keys.go` (which drives the handler, the footer and
the help).

| Where | Key | Action |
|---|---|---|
| Panels (any) | `I` | open the window (draft, else empty; recipient per §4) |
| One-line input | `Ctrl+O` | open the window with the line's text (recipient split off) |
| Window | `Ctrl+S` | send (§7) |
| Window | `Tab` | text ↔ recipient |
| Window | `Esc` | close, keep the draft |
| Window | `Ctrl+Q` | quit busmon (the global binding, unchanged) |

`i` keeps opening the one-line input. Ctrl+S is safe: tcell's raw mode disables XON/XOFF. Alt+Enter
is **not** bound (not reliably distinguished by every terminal; may come later once tested).
Mouse: a click outside the window closes it like Esc (the v0.9 overlay rule).

## 6. One editor, two uses

The window is one component, `composer` (new file `cmd/busmon/compose.go`), parameterised by
title, limit, a header line, whether it has a recipient field, and an `onSend(text) error`:

- **Message** (§3-§4): limit `bus.CmdMaxRunes`, recipient field, sends through §7.
- **Free answer** in the question panel: opened by Enter on the "write an answer" option, title
  `Answer: <question header>`, no recipient field, limit 2000 runes (today's cap in `askKey`; if
  the bus validates a different cap, that one wins), sends through `sendAnswer(a, q, "", text)`.
  Esc returns to the question panel with the text kept for that question (`ask.text` keyed by ask
  id and question id, cleared once answered). The `typing`/`[]rune` path of `askKey` is removed.

## 7. One send path

`inputDone`'s Enter branch becomes `u.send(text) (sent bool)`, called by the line (Enter) and by the
window (Ctrl+S, with `@<recipient> ` prefixed to the body when there is one). Everything it does
today stays in that one function and applies to both:

- the open-question hint (`askHolders`/`askDraft`: first Ctrl+S shows the hint, second sends, Tab
  opens the panel — in the window, the hint's "Tab" becomes "Ctrl+T: answer in its panel", since Tab
  moves to the recipient);
- the broadcast warning (`broadcastDraft`: a second Ctrl+S sends it);
- `@all` fan-out with partial failures (the draft is cleared when some were served);
- a failed send keeps the draft.

On success the window closes and the shared draft (`u.msgDraft`) is cleared; on a warning or a
failure it stays open with the flash shown in its footer. Multi-line text is sent as written:
`HumanCmd`/`HumanNotify` already keep newlines (`SanitizeReportFull` is not involved: cmd bodies
are bounded by `CmdMaxRunes` only (`ValidateText`: valid UTF-8 and a rune count). The composer
normalises before sending: `\r\n` and `\r` become `\n`, other control characters except `\n` and
`\t` are removed, trailing spaces on each line and blank lines at both ends are trimmed.

**One draft.** `u.msgDraft` holds the text, newlines included, for both. The line shows a one-line
draft as today; a draft with several lines is never flattened into it: `i` then opens the window
on it (the line cannot show it without loss), and the footer of the line's mode says
`I: edit the draft`.

## 8. Later lot: reply in a thread (not in v0.16.0)

`R` on an Activity line opens the window with the recipient (the line's author) and the thread
root (the line's `ref`, else its id), header `to: @x · reply in <short id>`. It needs a bus
primitive: `HumanCmd` sends a directive without `ref` and `HumanClose` replies with `wake=no`, so a
new `Bus.HumanReply(ctx, from, to, ref, body)` (`type=reply`, `human=yes`, waking) is required, with
its own tests. Specified when that lot starts.

## 9. Agents use a code index when one is present

Bernard asks that agents use a code-graph MCP when the machine has one. This repository's
`AGENTS.md` already says so; the projects the bus serves do not. The `agent-bus` peer skill (read by
every role in every project) gains a short section: if a code-index MCP server is available
(codebase-memory-mcp, codegraph), use it first for structural questions (where is X defined, who
calls Y, what breaks if Z changes) and grep for literals, configuration and documents; index the
repository once if it is not; an index is the code at its indexed revision, never evidence of what
is deployed. Doc-only; no client is configured by agent-bus.

## 10. Tests

- `composer`: rune counter and limit, Esc keeps text, Ctrl+S calls `onSend` once, recipient Tab
  cycle, unknown recipient refused.
- `send`: the existing `inputDone` tests run through `send`; the same cases driven from the window
  (hint, broadcast warning, `@all` partial failure, failed send keeps the draft); a multi-line body
  reaches `HumanCmd` intact.
- Shared draft: line → Ctrl+O → window keeps text and recipient; window → Esc → `i` reopens the
  window when the draft has several lines, the line when it has one; normalisation (`\r\n`, control
  characters, trims) tested on pasted input.
- Free answer: Enter on "write an answer" opens the composer; Ctrl+S sends one answer; Esc keeps
  the text per question.
- Keys: `ModeCompose` table in the help and the footer; `TestAlignmentSuite` with the window open.

## 11. Out of scope

Preview, external editor, reply in a thread (§8), templates, spell check, saving drafts across
busmon restarts.
