# busmon compose window (v0.16) — design

Status: rev 2, 2026-10-17. Asked by Bernard; options settled with him and Kimi. Rev 2 applies
Kimi's review of rev 1 (structured send, rune limit, keys, send results, modes, paste).

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
| 6 | Reply in a thread from an Activity line | yes, **separate lot** (§9) |
| 7 | The same editor for a free answer in the question panel | yes |

From Kimi: one send path shared by the line and the window (never duplicated); the message type
is inferred, not a second field; the counter counts runes, like the bus.

## 3. The window

A modal overlay, centred, its frame in the Activity hue (`panelHue`), width `min(screen − 4, 100)`,
height `min(screen − 6, 16)` with at least 6 text lines; in the narrow tier it takes the whole screen
less the header.

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

- **Editor:** `tview.TextArea` (tview v0.42.0): wrap, cursor keys, Home/End, word moves,
  selection, undo, its own copy/paste keys (Ctrl+Q copies a selection, Ctrl+E/Ctrl+A end/start of
  line — all left to it). Enter inserts a new line.
- **Header:** `to: <recipient> · <type>`: an agent or `@all` → `directive`; empty → `broadcast`
  (attention colour). No type field to edit.
- **Footer:** the window's bindings from the key table (§5); a send's flash (§7) replaces it until
  the next key.
- **Rune limit, ours, not tview's.** `TextArea.SetMaxLength` counts bytes (textarea.go:848) and
  `GetTextLength` too, so neither is used. The composer keeps its own limit in runes
  (`utf8.RuneCountInString`, the count `bus.ValidateText` applies): a `SetChangedFunc` hook rejects a
  change that would exceed it by restoring the last accepted text and cursor; a paste is cut at the
  last whole rune that fits (never mid-UTF-8) and the flash says how much was dropped. Limits:
  `bus.CmdMaxRunes` for a message, 2000 for an answer (§6). Counter `n / limit`, attention colour
  at the limit.
- **Paste.** busmon enables bracketed paste (`app.EnablePaste(true)`), and `screenView` and
  `modalView` gain a `PasteHandler` that relays to the focused child, as their `InputHandler` does
  (today both embed `*tview.Box`, whose paste handler drops it). The one-line input gets pastes too;
  a newline pasted into it moves the text to the window (§7, one draft).
- **Glyphs:** only the existing set (`TestNoStrayNonASCIILiterals`); the frame passes
  `TestAlignmentSuite` at every size and palette (new fixture: the window open).

## 4. Recipient

- `Tab` moves the focus between the text and the recipient field (one line, the current
  autocompletion of `@agent`/`@all` from `agentNames`). Tab is consumed by the composer: TextArea
  would insert a tab or call `finished` (textarea.go:2119). Enter in the recipient field returns to
  the text; it never sends.
- Opening: from the line, the `@name` the line starts with (the rest is the body, through
  `parseDirected` once, at opening only); from the Agents panel or an agent's detail (`ovAgent`),
  the selected agent; otherwise empty.
- A recipient that is neither a known agent, `all`, nor empty is shown in the attention colour and
  Ctrl+S refuses with a flash (`unknown recipient`), the draft kept.

## 5. Keys

New mode `ModeCompose` with its own table in `keys.go` (which drives the handler, the footer and
the help).

| Where | Key | Action |
|---|---|---|
| Panels (any) and the agent detail (`ovAgent`) | `I` | open the window (draft, else empty; recipient per §4) |
| One-line input | `Ctrl+O` | open the window with the line's text |
| Window | `Ctrl+S` | send (§7) |
| Window | `Tab` | text ↔ recipient (message); nothing (answer: consumed, no tab inserted) |
| Window | `Ctrl+T` | after the open-question hint only: answer in that agent's panel (§7) |
| Window | `Esc` | close, keep the draft |
| Everywhere | `Ctrl+C` | quit busmon (the global binding, `keys.go:139`, unchanged) |

- `i` keeps opening the one-line input. In the line, `I` is a letter like any other.
- **Ctrl+O in the line** is intercepted before `ModeInput` hands the event to the field
  (`ui.go:347` returns every key to it today); the line's footer reads `Ctrl+O: edit in the window`.
- **`I` in the agent detail** is bound in `overlayBindings` for `ovAgent` only (unbound keys in an
  overlay are swallowed today, `ui.go:327`).
- Ctrl+S is safe: tcell's raw mode (`term.MakeRaw`) clears IXON. Ctrl+S and Ctrl+T have no TextArea
  binding. Alt+Enter is not bound.

## 6. One editor, two uses

One component, `composer` (new file `cmd/busmon/compose.go`), created once at startup like every
primitive, parameterised when opened: title, rune limit, whether it has a recipient field, the
parent to return to, and `onSend(target, body string) sendResult`.

- **Message** (§3-§4): limit `bus.CmdMaxRunes`, recipient field, parent none, sends through §7.
- **Free answer** in the question panel: opened by Enter on the "write an answer" option, title
  `Answer: <question header>`, no recipient field, limit 2000 runes (today's cap; if the bus
  validates a lower cap for answers, that one wins), parent the question panel. Its text is kept per
  ask id and question id (`ask.drafts`), and cleared **only once the answer is accepted**:
  `sendAnswer` today clears the text before it checks the error (`askpanel.go:341`); it now returns a
  `sendResult`, and a failure keeps the text, the window and its flash. The `typing`/`[]rune` path
  of `askKey` is removed.

**Modes and focus.** `mode()` returns `ModeCompose` whenever the composer is open, ahead of
`ModeAsk` and `ModeOverlay` (`ui.go:224`), and `focusPrimitive()` returns the composer's text or
recipient field (`ui.go:255`). There is still one overlay layer, no stack: opening the composer over
the question panel records the panel as its parent; closing (Esc, or a click outside — the same
function, draft included) reopens the parent and gives it the focus, or returns to the panel that had
it. A resize relayouts the open composer without touching its text, cursor or field focus.

## 7. One send path

`inputDone`'s Enter branch becomes `u.send(target, body string, directed bool) sendResult`:

- the **line** adapts its text with `parseDirected` and calls it;
- the **window** passes its fields as they are: never rebuilt into an `@name body` string, so an
  empty recipient with a body starting `@coder` stays a broadcast, and `@master` with an empty body
  is not turned into one.

An empty body (after normalisation) is refused before anything else (`nothing to send`).
Everything `inputDone` does today stays in `send`, for both callers: the open-question hint
(`askHolders`/`askDraft`), the broadcast warning (`broadcastDraft`), the `@all` fan-out with partial
failures, the failed send.

```go
type sendResult int
const (
	sendSent    sendResult = iota // everything sent: close, clear the draft
	sendPartial                   // @all, some served: close, clear the draft (a retry would send twice), flash the failures
	sendHeld                      // a warning that waits for a second Ctrl+S/Enter: stay, keep the draft
	sendFailed                    // nothing sent: stay, keep the draft, flash the error
	sendRefused                   // empty body, unknown recipient: stay, keep the draft
)
```

In the window the open-question hint reads "Ctrl+S: send as a message · Ctrl+T: answer in its
panel · Esc: back to editing"; in the line it is unchanged (Enter, Tab, Esc).

**Text normalisation** before sending: `\r\n` and `\r` become `\n`; control characters other than
`\n` and `\t` are removed; trailing spaces on each line and blank lines at both ends are trimmed. Cmd
bodies are bounded by `CmdMaxRunes` only (`ValidateText`: valid UTF-8 and a rune count), so newlines
reach `HumanCmd`/`HumanNotify` as written.

**One draft.** `u.msgDraft` holds the text, newlines included, for both. A draft with several lines
is never flattened into the line: `i` then opens the window on it, and a newline pasted into the line
moves the text to the window.

## 8. Agents use a code index when one is present

Bernard asks that agents use a code-graph MCP when the machine has one. This repository's `AGENTS.md`
already says so; the projects the bus serves do not. The `agent-bus` peer skill (read by every role in
every project) gains a short section: if a code-index MCP server is available (codebase-memory-mcp,
codegraph), use it first for structural questions (where is X defined, who calls Y, what breaks if Z
changes) and grep for literals, configuration and documents; index the repository once if it is not;
an index is the code at its indexed revision, never evidence of what is deployed. Doc-only; agent-bus
configures no client.

## 9. Later lot: reply in a thread (not in v0.16.0)

`R` on an Activity line opens the window with the recipient (the line's author) and the thread root
(the line's `ref`, else its id), header `to: @x · reply in <short id>`. It needs a bus primitive:
`HumanCmd` sends a directive without `ref` and `HumanClose` replies with `wake=no`, so a new
`Bus.HumanReply(ctx, from, to, ref, body)` (`type=reply`, `human=yes`, waking) is required, with its
own tests. Specified when that lot starts.

## 10. Tests

- `composer`: rune limit with multi-byte text at the exact limit and limit + 1 (typed and pasted, cut
  on a rune boundary); counter; Esc keeps text; Ctrl+S calls `onSend` once; Tab consumed (toggle for
  a message, nothing for an answer, never a tab character); unknown recipient refused.
- `send`: the existing `inputDone` tests run through the line adapter; from the window: empty
  recipient + body `@coder …` is a broadcast (warning), `@master` + empty body refused, hint then
  Ctrl+T, broadcast second Ctrl+S, `@all` partial failure closes and clears, total failure stays; a
  multi-line body reaches `HumanCmd` intact; normalisation of pasted `\r\n` and control characters.
- Answers: Enter on "write an answer" opens the composer over the panel; Ctrl+S sends one answer; a
  failed `sendAnswer` keeps the text; Esc and a click outside return to the panel with the text kept
  for that question.
- Modes: `mode()` priority, `focusPrimitive()`, resize with the window open keeps text and focus,
  Ctrl+O from the line, `I` from a panel and from `ovAgent`, paste relayed by `screenView`/`modalView`.
- Keys: `ModeCompose` table in the help and the footer; `TestAlignmentSuite` with the window open.

## 11. Out of scope

Preview, external editor, reply in a thread (§9), templates, spell check, drafts across busmon
restarts.
