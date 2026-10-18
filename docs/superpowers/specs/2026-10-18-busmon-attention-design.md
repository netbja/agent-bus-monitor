# busmon attention and recap design

## Goal

Help a person see what still needs their attention, remind them without interrupting active composition, and restore project context after an absence. The feature is a deterministic view over existing bus data plus small per-project local preferences. It does not change agent state or write derived attention facts to the bus.

## Behaviour

### Waiting for the person

Show the human waiting marker (`♙ waiting`) for an agent only while it has at least one unanswered question in an open ask, or it declared `waiting` and no qualifying human message arrived after that declaration. An open ask keeps the marker even if the person has sent some other message to the agent.

A qualifying message is an entry with `human=yes` addressed to that agent, including a separately fanned-out `@all` entry, or a human reply in a thread rooted at a message from that agent. Compare the event's millisecond stream ID timestamp strictly after the declared status timestamp; equal timestamps do not count because the streams do not provide a cross-stream ordering. A later `waiting` declaration starts a new interval. A newly opened ask also makes the agent waiting again.

If a declared `waiting` has a qualifying reply but no open ask, display `answered <age>` in place of the waiting label. Do not show the waiting glyph, bell, or fade, and exclude it from the header's waiting-for-you count. Keep the declared state unchanged in the detail view. Reports, silence, quota holds, and messages without `human=yes` do not answer a wait. This is display-only; busmon publishes no state update.

### Reminder popup

An attention item is one unanswered question in an open ask, or one unanswered waiting declaration. Its age starts at the ask/question creation time or the waiting status timestamp. When it reaches `waiting.popup_after`, busmon presents a reminder containing the agent, age, and question text or declared status message. The default delay is five minutes; `0` disables reminders.

If the person is composing in either the line input or compose window, has a line draft, or any other modal is open, queue due reminders. Present them as soon as the UI is clear. A single popup groups all currently due, unsnoozed items, oldest first. It has a selection cursor: Enter on an ask item opens that ask's question panel; `i` on a waiting item opens the compose window addressed to that agent. Esc means “later” and snoozes every item in that popup for `waiting.snooze` (default 15 minutes). Each item appears at most once during its snooze period; if it is still open afterward it may appear again. A new waiting declaration or new ask question is a new item and is not hidden by the previous item's snooze.

The existing bell and waiting fade apply only to items still waiting for the person and respect their settings. No OS, phone, or terminal notification is added.

### “Where was I” recap

Build a deterministic recap from bus snapshots and retained stream events; do not call an LLM. Include:

1. **Expected from you:** open asks, unanswered waiting declarations, directives addressed to the current human identity, and blocked agents.
2. **Agents:** each agent's declared state, last status message and age, and quota held or project paused indicators when known.
3. **Board:** tasks grouped by board state.
4. **Since your last visit:** verdicts/merges from the verdict ledger, swaps, pause/resume events, service failures, and report counts by agent.

Use the last visit timestamp for the selected project. For stream-backed sections, report the oldest available event timestamp when the requested interval begins before retained history. Never infer missing verdicts, swaps, reports, or service events. If a required read fails, label that section unavailable and keep the rest of the recap; do not render a failed read as an empty result. Snapshot-only values have no event history and are shown as current state, not as changes since the last visit.

Store the last visit per project in a local file under `~/.cache/agentbus/`, never in Redis. At startup, capture the prior timestamp, build the recap from it, and record this visit only after busmon has successfully connected and initialized. Show it automatically when the previous visit is older than `recap.auto_after` (default eight hours); `0` disables automatic display. With no prior timestamp, do not auto-open. A recap key opens it at any time; `R` is proposed because it is free in the current panel bindings. Esc closes it.

Each recap row is selectable and opens the most relevant existing view: agent detail, ask panel, or the matching activity/thread detail.

The recap ends with one action row, “Ask master for a summary” (Bernard approved it). It is never sent automatically: Enter on it asks a one-line confirmation (“sends a message to master, uses its tokens — y/n”), then sends one human directive to the project's escalation target (`master` by default) through the existing `ui.send` path: “Summarise where the project stands for me: what was done since <last visit>, what is in progress, what you expect from me. Answer on the bus with agentbus reply.” The row shows when the last such request was sent and whether a reply arrived in its thread; the reply is read in the normal thread/activity views. It is disabled, with the reason, when the target is not a registered agent, its provider is held, or the project is paused.

## Data read

Reuse busmon's existing poll/read model where possible: agent snapshots for declared state and status, open asks for unanswered questions, board entries for task state, budget and quota/pause records for current holds, and the retained cmd/report/notify/status and verdict/swap/service records for the since-last-visit section. Read human messages from cmd entries and require `human=yes`; correlate only addressed deliveries and replies to the agent's thread. `@all` counts through its per-agent fan-out entry, not through the original composer text.

The Activity panel's `--limit` backfill is not a completeness guarantee for attention or recap. Use an explicit bounded read for the required range and identify the oldest retained timestamp/cursor for each capped source. The supported history is only what the bus retains (typically around 1,000 stream entries; the verdict ledger has its own cap). If the last-visit cursor predates the oldest available item, say history before that point is unavailable. A read error preserves the previous screen snapshot and marks the recap section unavailable; it must never silently clear an outstanding ask or treat the absence of a retained reply as proof that nobody replied.

The local cache stores only the project key, last-visit time, and reminder snooze deadlines/item identities. It contains no message bodies, answers, or credentials. Write it atomically with restrictive user-only permissions; if it is absent or unreadable, continue with defaults and no prior-visit recap.

## Keys

- `R`: open the recap from the normal panel mode (free: `r` is Requests). On its last row, Enter then `y` sends the summary request to master.
- In a reminder popup, Up/Down (or `j`/`k`) selects an item; Enter opens a selected ask; `i` opens a selected unanswered-waiting item in the compose window; Esc snoozes the displayed batch.
- In the recap, Up/Down (or `j`/`k`) selects a row, Enter opens its linked detail, and Esc closes the recap.
- Existing keys and mode priority remain unchanged. A modal, compose window, or active line draft is never displaced by an automatic reminder or recap.

## Settings

Read `~/.config/agentbus/busmon.toml`, following the existing user configuration conventions. Precedence is command-line flag, environment variable, file, then default. Missing or invalid configuration must not prevent startup: apply defaults and show a muted header note. Invalid individual values use their documented defaults and are included in the note.

```toml
[waiting]
popup_after = "5m" # "0" disables reminders
snooze = "15m"
bell = true
fade = true

[recap]
auto_after = "8h" # "0" disables automatic recap

[display]
theme = "auto"
limit = 25

[identity]
as = "" # empty uses the existing default human identity
```

Settings are global in this version. `[project.<name>]` overrides are a future extension. Existing `--limit`, `--theme`, `--as`, `AGENT_BUS_BUSMON_LIMIT`, `AGENT_BUS_BUSMON_THEME`, and `AGENT_BUS_HUMAN` continue to work and take precedence over file values. There is no in-app settings screen.

## Edge cases

- An ask remains an attention item until its question is answered, withdrawn, or replaced; an unrelated human message does not close it.
- If one agent has both an open ask and an unanswered waiting declaration, show both as distinct items, but count the agent once in any agent-level summary. Its list state remains waiting.
- A human reply cannot be correlated across stream timestamps with sub-millisecond ordering; equal timestamp IDs do not resolve a wait. A new declared waiting timestamp resets its answered state.
- If a message or status predates the retained stream floor, busmon cannot prove the relationship. Keep the wait unresolved unless a retained qualifying message is found, and label the relevant history boundary where recap coverage is incomplete.
- If local cache writes fail, continue running without durable snoozes or last-visit tracking and show a muted note. A restart may therefore show the same due reminder again.
- `popup_after = "0"` disables popups, not the waiting marker, header count, bell, fade, or manual recap. `bell=false` and `fade=false` affect only those signals.
- Offline or partial bus reads never fabricate a clean recap; unavailable sections state that their data could not be read.

## Tests

- Pure waiting/answered cases: open ask, unanswered wait, a qualifying direct human reply, `@all` fan-out, a reply on the agent's thread, non-human message, unrelated human message, later re-declaration, and same-millisecond IDs.
- Recap model tests for each section, stable ordering, deduplication, missing history boundary, and independent read failures.
- UI tests that reminders wait while a compose window, line draft, or another modal is active, then appear immediately when it clears; grouped oldest-first ordering; ask and waiting actions; snooze suppression and expiry; a new declaration/question gets a new item.
- Settings tests for flag > environment > file > default, default values, disabled durations, and missing, malformed, or invalid-value files without startup failure.
- Preserve `TestAlignmentSuite` and glyph tests for all supported widths and palettes, including the reminder and recap views.
- Redis integration tests, if needed, must use only `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392`.

## Out of scope

- OS, desktop, terminal, or phone notifications.
- LLM-generated summaries inside busmon, or any summary request sent without the person's explicit confirmation.
- Per-project settings overrides and an in-app settings editor.
- Any bus write to record answered waiting, recap visits, or reminder snoozes.
- Changing agent-declared state, dispatch decisions, or how agents are woken.
