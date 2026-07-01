# Report Fidelity — Design Spec

- **Date**: 2026-07-02
- **Status**: Approved (brainstorm), ready for implementation plan
- **Scope**: One slice — `bus` + `agentbus` + a minimal `busmon` breadcrumb.
- **Backlog item**: feedback friction #7 (the human-facing `report` channel loses fidelity, so real signal migrates to `cmd`/files). See [[verdict-ledger-and-feedback-backlog]].

## Problem

`Bus.Report` runs `SanitizeReportMessage` **at write time** — it collapses
whitespace, strips newlines, and truncates to 500 runes, and stores only that.
The full text is therefore **never stored** — it is lost. A detailed multi-line
report becomes a one-line 500-char preview, so agents route the real content to
`cmd` (machine-fidelity) or files — exactly the channels the operator does not
read first.

The 500-cap + newline-strip exist for a reason: the line-based `agentbus listen`
consumer and the busmon ACTIVITY feed are **one-line-per-event**. So the fix is
not "raise the cap" (that floods the flat viewers and breaks `listen`). The fix is
structural: **retain the full text at write time, keep the bounded preview for the
flat viewers, and add a reader that restores the full fidelity on demand.**

## What already exists (do not rebuild)

- `Bus.Report` (`bus/stream.go`) writes `{p}:report` entries with fields
  `agent kind message`, where `message = SanitizeReportMessage(...)`.
- `SanitizeReportMessage` (`bus/bus.go`) + `reportMaxLen()` (`AGENT_BUS_REPORT_MAX`,
  default 500) — the one-line preview sanitizer. **Unchanged by this slice.**
- busmon and `listen` render the `message` (preview) field, one line each.

## Design decisions (locked during brainstorm)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Where fidelity surfaces | A **CLI reader** (`agentbus reports`), not a busmon detail pane. |
| 2 | Storage | Full text retained in the **same report stream entry** (a `full` field), stored **only when it differs from the preview**. |
| 3 | Full sanitization | `SanitizeReportFull`: strip control chars **except `\n`/`\t`**, cap at **8000** runes (`AGENT_BUS_REPORT_FULL_MAX`). |
| 4 | Command shape | One verb `reports [<id>]` — no arg lists recent, an id shows the full text. (Chosen over `report-show <id>` for CLI consistency.) |
| 5 | busmon | A compact ` (+N)` marker on a report line when `full` is present — no detail pane. |

## Write path — `Bus.Report`

```
preview := SanitizeReportMessage(message)   // unchanged: ≤500, one line, for listen/TUI
full    := SanitizeReportFull(message)      // new: keeps \n/\t, caps at 8000
```

- Always store `message: preview`.
- Store the `full` field **only if `full != preview`** — i.e. the message had
  newlines/tabs or exceeded the preview cap. For a short single-line report,
  `full == preview`, so `full` is omitted (no duplication; and the busmon marker
  then means "there is genuinely more than the preview shows").

### `SanitizeReportFull` (`bus/bus.go`, beside `SanitizeReportMessage`)

- Maps every control rune **except `\n` and `\t`** to a space (so the text is safe
  to print to a terminal, but structure — line breaks, indentation — is kept).
- Does **not** collapse runs of whitespace (structure preserved).
- Truncates to `reportFullMax()` runes, appending `…` when it exceeds — mirroring
  `SanitizeReportMessage`'s truncation shape.
- `reportFullMax()` resolves `AGENT_BUS_REPORT_FULL_MAX` (positive int) else
  `defaultReportFullMax = 8000` — mirrors `reportMaxLen()`.

## Data model

- `Event` (`bus/stream.go`) gains `Full string`. `ParseEntry` populates it for
  `kind == "report"` from `fields["full"]` (empty when the field was omitted).
- No other Event kind uses `Full`.

## Read path — `reports`

Two bus readers (`bus/stream.go`):

- `func (b *Bus) Reports(ctx, n int) ([]Event, error)` — the `n` most recent report
  entries, chronological (oldest→newest), via `XRevRangeN` then reverse. Mirrors
  the `Recent`/`Verdicts` read-only pattern (XRANGE family, no consumer groups).
- `func (b *Bus) ReportByID(ctx, id string) (Event, error)` — the single report
  entry with that id (`XRANGE id id`); errors `no report <id>` if absent.

CLI (`cmd/agentbus/`):

```
agentbus reports [--json]     # list the recent reports
agentbus reports <id>         # print the full retained text of one report
```

- `reports` (no positional) → `Bus.Reports(ctx, 25)`; render one line each:
  `<id>  <agent>  <kind>  "<preview>"  (+<N>)` where `(+<N>)` (N = the full text's
  rune length) appears **only** when `Full` is non-empty. `--json` emits the raw
  list.
- `reports <id>` → `Bus.ReportByID`; print `Full` if non-empty, else `Message`
  (the preview) — the full multi-line text, raw. Ignores `--json`.
- Rendering lives in a new `cmd/agentbus/reports.go` (`reportsTable`,
  `reportDetail`), pure and unit-tested, mirroring `verdicts.go`/`agents.go`.

## busmon breadcrumb

busmon already parses report entries through `ParseEntry`, so it gets `Event.Full`
for free. The report line in the ACTIVITY feed appends a compact ` (+N)` marker
(N = `Full` rune length) **only when `Full` is non-empty**, signalling "this report
is truncated — `agentbus reports <id>` for the full text". No detail pane, no
expansion (CLI-centric, decided). This is the only busmon change.

## Non-goals (explicit)

- **busmon inline expansion / detail pane** — the rejected fork B.
- **Fidelity on `notify`/`cmd`** — only `report` is the curated human channel.
- **Durable retention beyond the report stream's ~1000-entry cap** — the `full`
  field lives in the entry and is trimmed with it. `report` is an activity channel,
  not an audit trail (that is the verdict ledger's job). Acceptable and intended.
- **Changing `SanitizeReportMessage` / the 500 preview cap** — untouched.

## Testing

**`bus`**
- `SanitizeReportFull`: preserves `\n`/`\t`, maps other control chars to space,
  does not collapse internal whitespace, truncates at the cap with `…`,
  `AGENT_BUS_REPORT_FULL_MAX` override.
- `Report` stores `full` only when it differs from the preview: a short single-line
  message → no `full` field; a multi-line or >500-rune message → `full` present and
  distinct.
- `Reports` returns recent entries chronological with `Full` populated;
  `ReportByID` round-trips one entry and errors on an unknown id.
- `ParseEntry` maps `fields["full"]` into `Event.Full` for report kind.

**`agentbus`** (pure render, no Redis)
- `reportsTable`: the `(+N)` marker appears iff `Full` is non-empty; columns/sort.
- `reportDetail`: prints `Full` when present, else the preview.

**`busmon`** (pure render)
- the report line carries ` (+N)` iff `Full` is non-empty.

## Files touched

- `bus/bus.go` — `SanitizeReportFull`, `defaultReportFullMax`, `reportFullMax`.
- `bus/stream.go` — `Event.Full`; `ParseEntry` report case; `Bus.Report`
  full-when-differs; `Bus.Reports`; `Bus.ReportByID`.
- `bus/stream_test.go` / `bus/bus_test.go` — the bus tests above.
- `cmd/agentbus/reports.go` (+ `reports_test.go`) — `reportsTable`, `reportDetail`.
- `cmd/agentbus/main.go` — `reports` case; usage banner; doc-comment header.
- `cmd/busmon/main.go` (or `render.go`) — the ` (+N)` report marker.
- `README.md`, `CLAUDE.md`, `docs/AGENT-BUS-GUIDE.md` — document the retained full
  text + `reports [<id>]`.
