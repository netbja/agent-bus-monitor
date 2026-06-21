# busmon UI: width-fit + master status bar — Design (Slice 2)

**Date:** 2026-06-21
**Status:** Approved for implementation
**Scope:** `cmd/busmon/` only. **No `bus` or `agentbus` changes.** Branches off `main`
(independent of Slice 1 / PR #7 — the two never touch the same files).

Second of three slices from the multi-agent friction list:

| Slice | Theme | Status |
|-------|-------|--------|
| 1 | Bus protocol robustness | PR #7 (open) |
| **2 (this doc)** | busmon UI: width-fit + master status bar | designing |
| 3 | Master orchestration + herdr integration | later |

## Goals

1. **Width-fit (friction #6).** The AGENTS pane joins every agent chip into one line inside a
   fixed height-3 box, so at narrow widths or with several agents the chips past the first line are
   hidden. Re-render so nothing is hidden: wrapped chips that flow onto extra rows, with the pane
   growing to fit (capped).
2. **Master indicator (friction #7).** A dedicated top status bar that flags which agent is
   **master**. *Master is defined as the pilot-lease driver* (`Bus.PilotDriver`, already read by
   busmon) — no new bus concept. "autonomous" (no lease) = no master.

## Non-goals / out of scope

- Any `bus`/`agentbus` change. Master reuses the existing pilot lease.
- ACTIVITY feed changes — it already wraps (tview default), so it has no horizontal overflow. It
  keeps its `[live]`/`[↑ pause]` title indicator unchanged; the status bar does **not** duplicate a
  live token (the two mean different things — global state vs. feed-scroll state).

---

## Component A — Status bar (new top row)

A new single-line `TextView` becomes the **first** FlexRow item, above AGENTS: height 1, no border,
not focusable, `SetDynamicColors(true)`.

Content from a pure, testable function:

```go
// statusBar renders the top bar: the project, then the master indicator derived
// from the pilot-lease driver (master == whoever holds the lease).
func statusBar(project, driver string) string {
	if driver == "" {
		return fmt.Sprintf(" [white]%s[-]  ·  [yellow]autonome (pas de master)[-]", tview.Escape(project))
	}
	return fmt.Sprintf(" [white]%s[-]  ·  [green]⬢ MASTER %s[-]", tview.Escape(project), tview.Escape(driver))
}
```

A `renderStatus(view, project, &mu, &pilot)` wiring helper (mutex-guarded read of the shared
`pilot` string) sets the text. It is called wherever the agents pane is re-rendered (the `handle`
closure and the 1s ticker), so the master indicator stays current as the pilot lease changes/expires.

**The pilot/master label moves out of the AGENTS title** into this bar. The AGENTS title becomes a
static ` AGENTS ` (set once at construction; `renderAgents` no longer touches the title). The old
`pilotLabel` helper and its `TestPilotLabel` are **removed** — `statusBar` supersedes them.

---

## Component B — AGENTS pane: wrapped chips, dynamic height

Keep the horizontal chip style, but **pre-wrap deterministically** rather than relying on tview's
word-wrap (which would break a chip like `claude1: working (rebase onto main)` at its internal
spaces). `agentsView` gets `SetWrap(false)` since we supply explicit row breaks.

### Packing (pure, testable)

```go
type chip struct {
	text string // tagged, render-ready (color tags included)
	w    int    // visible width (tview.TaggedStringWidth), tags excluded
}

const chipSep = "  " // 2 spaces between chips on a row

// packChips greedily packs chips into rows no wider than width, keeping each chip
// intact. At most maxRows rows; if chips remain after maxRows, the last row gets a
// "[gray]+N[-]" marker counting the unplaced chips. Returns the rendered rows and
// their count (always >= 1). width<1 and maxRows<1 are clamped to 1.
func packChips(chips []chip, width, maxRows int) ([]string, int)
```

Algorithm: fill a row with whole chips while `curW + sep + chip.w <= width`; start a new row when
the next chip won't fit; stop at `maxRows` rows. A single chip wider than `width` takes its own row
(the box clips it). After the loop, if chips remain unplaced, append `chipSep + "[gray]+N[-]"` to
the last row.

### Render wiring

```go
const maxAgentRows = 4 // content rows before the "+N" overflow marker

func renderAgents(layout *tview.Flex, view *tview.TextView, agents map[string]*agentState, mu *sync.Mutex, pilot *string) {
	mu.Lock()
	defer mu.Unlock()
	// names sorted; build a chip per agent (master marker when n == *pilot)
	_, _, w, _ := view.GetInnerRect()
	if w < 1 {
		w = 80 // before the first layout pass; converges on the next render
	}
	chips := /* for each sorted name: lbl := agentLabel(n, agents[n], now, n == *pilot); chip{lbl, tview.TaggedStringWidth(lbl)} */
	rows, used := packChips(chips, w, maxAgentRows)
	view.SetText(strings.Join(rows, "\n"))
	layout.ResizeItem(view, used+2, 0) // +2 borders; grows the pane to fit, never starves ACTIVITY
}
```

`renderAgents` gains the `layout *tview.Flex` parameter so it can `ResizeItem` the AGENTS item each
render. Width is re-read from `GetInnerRect` every render (1s ticker + each event), so the layout
adapts to terminal resizes within a frame. Before the first layout pass `GetInnerRect` returns 0 →
the 80-column fallback is used and corrected on the next render.

### Master chip marker

`agentLabel` gains a `master bool` parameter; when true it **prepends a fuchsia `⬢ ` marker** to the
chip (`"[fuchsia]⬢[-] " + label`); the existing state coloring is unchanged. The caller passes
`n == *pilot`. (If the master is e.g. `hermes` with no chip in the map, the status bar still carries
the signal — the per-chip marker is secondary.)

```go
func agentLabel(n string, a *agentState, now time.Time, master bool) string
```

---

## Component C — File organization

`cmd/busmon/main.go` is ~590 lines and already has a `render_test.go` exercising pure helpers that
live in `main.go`. Extract the pure rendering helpers into a new **`cmd/busmon/render.go`** (same
`package main`), leaving `main.go` as the tview wiring + event loop:

- **Move to `render.go`** (verbatim, no behavior change): `stateColor`, `tag`, `clip`,
  `activityTitle`, `selectionTitle`, `entryTime`, `agentLabel`, `selPos`, the `feedLine` type.
- **Add to `render.go`** (new): the `chip` type, `chipSep`, `statusBar`, `packChips`.
- **Remove:** `pilotLabel` (superseded by `statusBar`).
- **Stays in `main.go`:** `main`, the `agentState` type, `resolveLimit`, `confirmReset`, the
  `renderAgents`/`renderStatus` wiring helpers, constants, and the event loop.

`render_test.go` keeps testing the moved helpers unchanged (same package). This is a targeted
improvement directly serving this slice (which adds non-trivial render logic), not unrelated
refactoring.

---

## Testing

Pure helpers are unit-tested; tview glue (`ResizeItem`, `GetInnerRect`, the new FlexRow item) stays
integration-level, matching today's split.

- **`TestStatusBar`** — master case contains `⬢ MASTER <driver>` and the driver; autonomous case
  (empty driver) contains the "autonome" text and no `MASTER`.
- **`TestPackChips`** — (a) chips that all fit → 1 row containing each; (b) chips summing wider than
  `width` → 2 rows, each within `width` by visible measure; (c) more chips than `maxRows` capacity →
  exactly `maxRows` rows with `+N` on the last; (d) **tag-width correctness:** chips whose `w`
  differs from `len(text)` (color tags) pack by `w`, not string length. Built with hand-made
  `chip{text, w}` so the test does not depend on `tview.TaggedStringWidth`.
- **`TestAgentLabelMaster`** — `master == true` output contains `⬢`; existing `TestAgentLabel` is
  updated for the new `master bool` parameter (passing `false`).
- **Remove `TestPilotLabel`** (function removed).

Gate (run on each task + a full pass at the end): `go build ./... && go vet ./... && go test ./... -count=1`.

---

## Summary of changed surfaces

| File | Change |
|------|--------|
| `cmd/busmon/render.go` | **new** — moved pure helpers + `chip`/`chipSep`/`statusBar`/`packChips` |
| `cmd/busmon/main.go` | status `TextView` + FlexRow item; `renderStatus`; `renderAgents(+layout, dynamic height, wrapped chips)`; `agentLabel(+master)`; AGENTS title static; `SetWrap(false)`; remove `pilotLabel` + moved helpers |
| `cmd/busmon/render_test.go` | remove `TestPilotLabel`; update `TestAgentLabel`; add `TestStatusBar`, `TestPackChips`, `TestAgentLabelMaster` |
| `bus/*`, `cmd/agentbus/*` | **none** |

## Things to get right

- **Dynamic resize must not starve ACTIVITY:** `maxAgentRows = 4` caps the AGENTS pane at 6 rows
  (incl. borders); ACTIVITY stays `proportion 1` and absorbs the rest.
- **Visible width, not byte length:** packing measures with `tview.TaggedStringWidth` so color tags
  and wide runes (badges 👂/⌛/🔒/⬢) don't throw off the fit.
- **Master can lack a chip:** the status bar is the authoritative master signal; the chip marker is
  best-effort when the driver name is also a tracked agent.
