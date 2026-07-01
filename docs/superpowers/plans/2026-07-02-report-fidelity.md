# Report Fidelity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retain the full text of a report at write time (alongside the existing bounded one-line preview) and surface it through an `agentbus reports [<id>]` reader, so the human-facing report channel stops losing fidelity — with a compact busmon breadcrumb so a truncated report is discoverable.

**Architecture:** `Bus.Report` stores a new `full` field (via `SanitizeReportFull`, which keeps newlines/tabs and caps at 8000) **only when it differs from the preview**. `Event` gains `Full`; two read-only bus methods (`Reports`, `ReportByID`) expose it; the `agentbus reports` command lists recent reports (marking those with more) and prints one report's full text. busmon appends a ` (+N)` marker. The 500-char preview and `SanitizeReportMessage` are untouched, so `listen`/busmon stay one-line-per-event.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, module `github.com/netbja/agent-bus-monitor`. Bus tests use the `dialTest` helper (real dev broker, skips if Redis down).

## Global Constraints

- Go 1.26; module `github.com/netbja/agent-bus-monitor`. The sanitizer + cap go in `bus/bus.go`; the `Event` field, `Report` change, and readers go in `bus/stream.go`.
- All CLI/output/doc text is **English**.
- `SanitizeReportFull`: keep `\n` and `\t`, map every **other** control rune to a space, **do not collapse** internal whitespace, truncate to `reportFullMax()` runes with a trailing `…`. `reportFullMax()` = `AGENT_BUS_REPORT_FULL_MAX` (positive int) else `defaultReportFullMax = 8000`. Mirror `reportMaxLen()`/`SanitizeReportMessage`.
- `Bus.Report` stores the `full` field **only when `full != preview`** (preview = `SanitizeReportMessage(message)`, unchanged).
- **Do not change** `SanitizeReportMessage` or the 500 preview cap.
- `reports` (no positional) lists the **25** most recent reports; `reports <id>` prints one report's full text (`Full` if present, else `Message`). The ` (+N)` marker (N = `Full` rune length) appears **only** when `Full` is non-empty — in both the CLI list and busmon.
- Non-goals: no busmon detail pane/expansion; no fidelity on `notify`/`cmd`; no durable store beyond the report stream's cap.

## File Structure

- `bus/bus.go` — **Modify.** `SanitizeReportFull`, `defaultReportFullMax`, `reportFullMax`.
- `bus/stream.go` — **Modify.** `Event.Full`; `ParseEntry` report case; `Bus.Report` full-when-differs; `Bus.Reports`; `Bus.ReportByID`.
- `bus/stream_test.go` — **Modify.** Report write + reader tests.
- `bus/bus_test.go` — **Modify.** `SanitizeReportFull` tests.
- `cmd/agentbus/reports.go` — **Create.** `reportsTable`, `reportDetail` (pure).
- `cmd/agentbus/reports_test.go` — **Create.** Pure render tests.
- `cmd/agentbus/main.go` — **Modify.** `reports` case; usage banner; doc header.
- `cmd/busmon/render.go` — **Modify.** `reportMarker` helper.
- `cmd/busmon/render_test.go` — **Modify.** `reportMarker` test.
- `cmd/busmon/main.go` — **Modify.** Append the marker in the report render.
- `README.md`, `CLAUDE.md`, `docs/AGENT-BUS-GUIDE.md` — **Modify.** Docs.

---

### Task 1: bus write side — `SanitizeReportFull` + retained `full`

**Files:**
- Modify: `bus/bus.go` (`SanitizeReportFull` + cap)
- Modify: `bus/stream.go` (`Event.Full`, `ParseEntry` report case, `Bus.Report`)
- Test: `bus/bus_test.go` (sanitizer), `bus/stream_test.go` (Report stores full-when-differs)

**Interfaces:**
- Consumes: existing `SanitizeReportMessage`, `reportMaxLen`, `b.add`, `Bus.Recent`, `ParseEntry`, `Event`.
- Produces (Tasks 2-4 rely on these): `func SanitizeReportFull(s string) string`; `Event.Full string` (populated by `ParseEntry` for `kind=="report"`); `Bus.Report` now writes a `full` field when it differs from the preview.

- [ ] **Step 1: Write the failing tests**

Add to `bus/bus_test.go`:

```go
func TestSanitizeReportFull(t *testing.T) {
	// keeps newlines and tabs, maps other control chars to space, no collapse
	got := SanitizeReportFull("a\nb\tc\x00d   e")
	if got != "a\nb\tc d   e" {
		t.Fatalf("SanitizeReportFull = %q, want %q", got, "a\nb\tc d   e")
	}
	// truncates at the cap with an ellipsis
	t.Setenv("AGENT_BUS_REPORT_FULL_MAX", "5")
	if got := SanitizeReportFull("abcdefghij"); got != "abcde…" {
		t.Fatalf("truncated = %q, want %q", got, "abcde…")
	}
}
```

Add to `bus/stream_test.go`:

```go
func TestReportStoresFullWhenDiffers(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	// short single-line report → preview == full → no full field stored
	if _, err := b.Report(ctx, "claude2", ReportNote, "all good"); err != nil {
		t.Fatalf("Report short: %v", err)
	}
	// multi-line report → preview flattens, full preserved → full field stored
	if _, err := b.Report(ctx, "claude3", ReportNote, "line1\nline2\n\nmore"); err != nil {
		t.Fatalf("Report multiline: %v", err)
	}
	evs, _, err := b.Recent(ctx, []string{"report"}, 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	var short, multi Event
	for _, e := range evs {
		switch e.Agent {
		case "claude2":
			short = e
		case "claude3":
			multi = e
		}
	}
	if short.Full != "" {
		t.Fatalf("short report should carry no full text, got %q", short.Full)
	}
	if multi.Full == "" || !strings.Contains(multi.Full, "\n") {
		t.Fatalf("multiline report should retain full text with newlines, got %q", multi.Full)
	}
	if strings.Contains(multi.Message, "\n") {
		t.Fatalf("preview should be one line, got %q", multi.Message)
	}
}
```

(`bus/stream_test.go` already imports `strings`, `context`; `bus/bus_test.go` — check it imports `testing` only, add nothing else since `t.Setenv` is stdlib.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `docker compose up -d && go test ./bus/ -run 'SanitizeReportFull|ReportStoresFull' -count=1 -v`
Expected: FAIL — `undefined: SanitizeReportFull`, and `Event` has no field `Full` (compile error).

- [ ] **Step 3: Add `SanitizeReportFull` to `bus/bus.go`**

Insert after `SanitizeReportMessage` (and add the cap helper near `reportMaxLen`):

```go
const defaultReportFullMax = 8000

// reportFullMax resolves the retained-full-text cap: AGENT_BUS_REPORT_FULL_MAX if
// it parses to a positive int, else defaultReportFullMax (8000). Read per call,
// mirroring reportMaxLen.
func reportFullMax() int {
	if v := os.Getenv("AGENT_BUS_REPORT_FULL_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultReportFullMax
}

// SanitizeReportFull is the fidelity-preserving companion to
// SanitizeReportMessage: it keeps newlines and tabs (multi-line structure
// survives) but maps every other control rune to a space (safe to print), does
// NOT collapse internal whitespace, and truncates to reportFullMax() runes with a
// trailing … . Stored in the report entry's `full` field, read by
// `agentbus reports <id>`.
func SanitizeReportFull(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if max := reportFullMax(); len([]rune(mapped)) > max {
		mapped = strings.TrimSpace(string([]rune(mapped)[:max])) + "…"
	}
	return mapped
}
```

(`bus/bus.go` already imports `os`, `strconv`, `strings`, `unicode`.)

- [ ] **Step 4: Add `Event.Full`, parse it, and store it in `bus/stream.go`**

Add the field to `Event` (after `Message`):

```go
	Message string // status/report/notify text, or the cmd command
	Full    string // report: full retained text (empty unless it differs from the preview)
```

Populate it in `ParseEntry`'s report case:

```go
	case "report":
		e.Agent, e.RKind, e.Message, e.Full = fields["agent"], fields["kind"], fields["message"], fields["full"]
```

Rewrite `Bus.Report` to store `full` when it differs from the preview:

```go
// Report publishes a curated report to the {project}:report stream. The one-line
// preview (SanitizeReportMessage, ≤500) is what listen/busmon render; the full
// text (SanitizeReportFull, keeps newlines, ≤8000) is retained in a `full` field
// only when it carries more than the preview, so `agentbus reports <id>` can
// restore fidelity without flooding the flat viewers.
func (b *Bus) Report(ctx context.Context, agent, kind, message string) (string, error) {
	if !ValidName(agent) {
		return "", fmt.Errorf("invalid agent %q", agent)
	}
	preview := SanitizeReportMessage(message)
	values := map[string]interface{}{"agent": agent, "kind": kind, "message": preview}
	if full := SanitizeReportFull(message); full != preview {
		values["full"] = full
	}
	return b.add(ctx, "report", values)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./bus/ -run 'SanitizeReportFull|ReportStoresFull' -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Build + vet + full bus tests (guard the Report change)**

Run: `go build ./... && go vet ./... && go test ./bus/ -count=1`
Expected: build/vet clean; all bus tests PASS (the added `full` field is additive; existing report/tail tests still pass).

- [ ] **Step 7: Commit**

```bash
git add bus/bus.go bus/stream.go bus/bus_test.go bus/stream_test.go
git commit -m "feat(bus): retain full report text (SanitizeReportFull, stored when it differs)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: bus read side — `Reports` + `ReportByID`

**Files:**
- Modify: `bus/stream.go` (`Bus.Reports`, `Bus.ReportByID`)
- Test: `bus/stream_test.go`

**Interfaces:**
- Consumes: `Event` (incl. `Full` from Task 1), `StreamKey`, `ParseEntry`, `toStringMap`, `Bus.Report` (writes `full`).
- Produces (Task 3 relies on these): `func (b *Bus) Reports(ctx context.Context, n int) ([]Event, error)` (oldest→newest); `func (b *Bus) ReportByID(ctx context.Context, id string) (Event, error)` (errors if absent).

- [ ] **Step 1: Write the failing tests**

Add to `bus/stream_test.go`:

```go
func TestReportsAndReportByID(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Report(ctx, "claude2", ReportNote, "first"); err != nil {
		t.Fatalf("Report: %v", err)
	}
	multiID, err := b.Report(ctx, "claude3", ReportNote, "big\nreport\nbody")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	// Reports: recent, chronological, Full populated for the multiline one
	evs, err := b.Reports(ctx, 10)
	if err != nil || len(evs) != 2 {
		t.Fatalf("Reports = %d entries (%v), want 2", len(evs), err)
	}
	if evs[0].Agent != "claude2" || evs[1].Agent != "claude3" {
		t.Fatalf("Reports not chronological: %+v", evs)
	}
	if evs[1].Full == "" {
		t.Fatalf("multiline report should carry Full: %+v", evs[1])
	}
	// ReportByID: fetch the one entry; unknown id errors
	got, err := b.ReportByID(ctx, multiID)
	if err != nil || got.Agent != "claude3" || !strings.Contains(got.Full, "\n") {
		t.Fatalf("ReportByID(%q) = %+v (%v)", multiID, got, err)
	}
	if _, err := b.ReportByID(ctx, "1-0"); err == nil {
		t.Fatal("ReportByID(unknown) should error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./bus/ -run 'ReportsAndReportByID' -count=1 -v`
Expected: FAIL — `undefined: (*Bus).Reports` / `(*Bus).ReportByID`.

- [ ] **Step 3: Implement the readers in `bus/stream.go`**

Add near the other read helpers (e.g. after `Recent`):

```go
// Reports returns the n most recent {project}:report entries in chronological
// order (oldest→newest), each with Full populated when the report retained a full
// text. Read-only (XREVRANGE then reversed) — no consumer-group cursors touched,
// like Recent/Verdicts.
func (b *Bus) Reports(ctx context.Context, n int) ([]Event, error) {
	key := StreamKey(b.project, "report")
	msgs, err := b.r.XRevRangeN(ctx, key, "+", "-", int64(n)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- { // XREVRANGE is newest-first; yield oldest→newest
		out = append(out, ParseEntry(key, msgs[i].ID, toStringMap(msgs[i].Values)))
	}
	return out, nil
}

// ReportByID returns the single {project}:report entry with the given id, or an
// error if no such entry exists.
func (b *Bus) ReportByID(ctx context.Context, id string) (Event, error) {
	key := StreamKey(b.project, "report")
	msgs, err := b.r.XRange(ctx, key, id, id).Result()
	if err != nil {
		return Event{}, err
	}
	if len(msgs) == 0 {
		return Event{}, fmt.Errorf("no report %q", id)
	}
	return ParseEntry(key, msgs[0].ID, toStringMap(msgs[0].Values)), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./bus/ -run 'ReportsAndReportByID' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): Reports + ReportByID readers for retained report text

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `agentbus reports [<id>]` command

**Files:**
- Create: `cmd/agentbus/reports.go`, `cmd/agentbus/reports_test.go`
- Modify: `cmd/agentbus/main.go` (`reports` case; usage banner line ~70; doc header ~line 10)

**Interfaces:**
- Consumes: `bus.Event` (fields `ID`, `Agent`, `RKind`, `Message`, `Full`); `(*bus.Bus).Reports`, `(*bus.Bus).ReportByID` (Task 2); existing `extractBool`, `die`, `ctx`, `b`, `json`.
- Produces: `func reportsTable(evs []bus.Event) string`; `func reportDetail(e bus.Event) string`; the `reports` command.

- [ ] **Step 1: Write the failing tests**

Create `cmd/agentbus/reports_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestReportsTable(t *testing.T) {
	evs := []bus.Event{
		{ID: "1-0", Agent: "claude2", RKind: "note", Message: "short one"},
		{ID: "2-0", Agent: "claude3", RKind: "auto", Message: "preview…", Full: "the\nfull\ntext"},
	}
	out := reportsTable(evs)
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "1-0") || !strings.Contains(first, "claude2") || !strings.Contains(first, `"short one"`) {
		t.Fatalf("first row wrong: %q", first)
	}
	if strings.Contains(first, "(+") {
		t.Fatalf("short report must have no (+N) marker: %q", first)
	}
	if !strings.Contains(out, "(+13)") { // "the\nfull\ntext" = 13 runes
		t.Fatalf("full report must carry (+13): %q", out)
	}
}

func TestReportDetail(t *testing.T) {
	if got := reportDetail(bus.Event{Message: "preview…", Full: "the\nfull\ntext"}); got != "the\nfull\ntext" {
		t.Fatalf("detail should return Full: %q", got)
	}
	if got := reportDetail(bus.Event{Message: "just a preview"}); got != "just a preview" {
		t.Fatalf("detail should fall back to Message: %q", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/agentbus/ -run 'ReportsTable|ReportDetail' -count=1 -v`
Expected: FAIL — `undefined: reportsTable` / `reportDetail`.

- [ ] **Step 3: Create `cmd/agentbus/reports.go`**

```go
package main

import (
	"fmt"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// reportsTable renders recent reports one line each (oldest→newest). A report that
// retained a full text (Full non-empty) gets a compact "(+N)" marker (N = full
// rune length) signalling `agentbus reports <id>` shows more.
func reportsTable(evs []bus.Event) string {
	var sb strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&sb, "%s  %-12s %-5s %q", e.ID, e.Agent, e.RKind, e.Message)
		if e.Full != "" {
			fmt.Fprintf(&sb, "  (+%d)", len([]rune(e.Full)))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// reportDetail returns the full retained text of a report (Full when present, else
// the preview Message) — the payload of `agentbus reports <id>`.
func reportDetail(e bus.Event) string {
	if e.Full != "" {
		return e.Full
	}
	return e.Message
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/agentbus/ -run 'ReportsTable|ReportDetail' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Add the `reports` case to `cmd/agentbus/main.go`**

Insert a new case after the `report` case:

```go
	case "reports":
		rest, asJSON := extractBool(rest, "--json")
		if len(rest) >= 1 { // detail: one report's full text by id
			e, err := b.ReportByID(ctx, rest[0])
			if err != nil {
				die(err.Error())
			}
			fmt.Println(reportDetail(e))
			return
		}
		evs, err := b.Reports(ctx, 25)
		if err != nil {
			die(err.Error())
		}
		if asJSON {
			out, _ := json.MarshalIndent(evs, "", "  ")
			fmt.Println(string(out))
			return
		}
		fmt.Print(reportsTable(evs))
```

- [ ] **Step 6: Update the usage banner and doc-comment header**

In the `len(args) < 1` usage `die` (line ~70), add `reports` after `report`:

```go
		die("usage: agentbus --project <p> <status|report|reports|notify|cmd|thread|challenge|reply|verdict|verdicts|version|pilot|gate|agents|pane|usage|subscribe|watch|listen> ...")
```

In the doc-comment header, add a `reports` line after the `report` line (~line 10):

```go
//	agentbus --project P reports   [<id>]         # list recent reports; <id> prints one report's full retained text
```

- [ ] **Step 7: Build + vet + full unit tests + e2e smoke**

Run:

```bash
go build ./... && go vet ./... && go test ./... -count=1
docker compose up -d
go build -o agentbus ./cmd/agentbus
P=demo-reports-$RANDOM
AGENT_BUS_PROJECT=$P AGENT_BUS_AGENT=claude2 ./agentbus report claude2 "$(printf 'migration plan:\n step 1 dump\n step 2 restore')"
AGENT_BUS_PROJECT=$P ./agentbus reports
ID=$(AGENT_BUS_PROJECT=$P ./agentbus reports | awk 'NR==1{print $1}')
AGENT_BUS_PROJECT=$P ./agentbus reports "$ID"
```

Expected: build/vet/tests green; `reports` shows a line ending in ` (+N)` for the multi-line report; `reports <ID>` prints the **multi-line** full text (with the real newlines, not the flattened preview).

- [ ] **Step 8: Commit**

```bash
git add cmd/agentbus/reports.go cmd/agentbus/reports_test.go cmd/agentbus/main.go
git commit -m "feat(agentbus): reports [<id>] — list recent reports + print full text

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: busmon ` (+N)` report marker

**Files:**
- Modify: `cmd/busmon/render.go` (`reportMarker` helper)
- Test: `cmd/busmon/render_test.go`
- Modify: `cmd/busmon/main.go` (append the marker in the report render, ~lines 415-416)

**Interfaces:**
- Consumes: `bus.Event.Full` (Task 1) — busmon already parses report entries via `ParseEntry`, so `e.Full` is populated.
- Produces: `func reportMarker(full string) string`.

- [ ] **Step 1: Write the failing test**

Add to `cmd/busmon/render_test.go`:

```go
func TestReportMarker(t *testing.T) {
	if got := reportMarker(""); got != "" {
		t.Fatalf("empty full → no marker, got %q", got)
	}
	if got := reportMarker("the\nfull\ntext"); got != " (+13)" { // 13 runes
		t.Fatalf("marker = %q, want ' (+13)'", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/busmon/ -run 'ReportMarker' -count=1 -v`
Expected: FAIL — `undefined: reportMarker`.

- [ ] **Step 3: Add `reportMarker` to `cmd/busmon/render.go`**

```go
// reportMarker returns a compact " (+N)" breadcrumb (N = full rune length) when a
// report retained a full text, so the operator knows `agentbus reports <id>` shows
// more; empty when there is nothing extra.
func reportMarker(full string) string {
	if full == "" {
		return ""
	}
	return fmt.Sprintf(" (+%d)", len([]rune(full)))
}
```

If `cmd/busmon/render.go` does not already import `fmt`, add it to the import block.

- [ ] **Step 4: Append the marker in the report render in `cmd/busmon/main.go`**

Replace the two report render lines (currently ~415-416):

```go
			line = tag("gray", ts) + " " + tag("teal", "[report:"+e.RKind+"->"+e.Agent+"]") + " " + tview.Escape(e.Message)
			plain = ts + " [report:" + e.RKind + "->" + e.Agent + "] " + e.Message
```

with (append `reportMarker(e.Full)` to both):

```go
			marker := reportMarker(e.Full)
			line = tag("gray", ts) + " " + tag("teal", "[report:"+e.RKind+"->"+e.Agent+"]") + " " + tview.Escape(e.Message) + marker
			plain = ts + " [report:" + e.RKind + "->" + e.Agent + "] " + e.Message + marker
```

- [ ] **Step 5: Run the test + build + vet**

Run: `go test ./cmd/busmon/ -run 'ReportMarker' -count=1 -v && go build ./... && go vet ./...`
Expected: test PASS; build/vet clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/busmon/render.go cmd/busmon/render_test.go cmd/busmon/main.go
git commit -m "feat(busmon): (+N) marker on reports that retained full text

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Documentation

**Files:**
- Modify: `CLAUDE.md` (report stream note + `reports` verb + a "Things that bite" bullet)
- Modify: `README.md` (command reference + stream table note)
- Modify: `docs/AGENT-BUS-GUIDE.md` (`reports` cheat-sheet lines)

**Interfaces:** none (docs only).

- [ ] **Step 1: Update `CLAUDE.md`**

Three edits:

1. In the **Stream layout** table, annotate the `{p}:report` row: its `message` field is the ≤500 one-line preview, and an optional `full` field retains the fidelity-preserving text (kept only when it differs from the preview).

2. In the **`agentbus`** verb list, add `reports` after `report`, and add a sentence:
   > `report` now retains the full text (newlines/tabs kept, ≤8000, `AGENT_BUS_REPORT_FULL_MAX`) in a `full` field **only when it differs from the one-line ≤500 preview**; `reports` lists recent reports (marking those with more via ` (+N)`) and `reports <id>` prints one report's full retained text. `listen`/busmon still render the preview.

3. Add a **Things that bite** bullet:
   > - **`report` keeps a bounded preview AND the full text.** The write path stores `message` (the ≤500 one-line `SanitizeReportMessage` preview, for `listen`/busmon) and — only when it carries more — a `full` field (`SanitizeReportFull`: newlines/tabs kept, ≤8000). Don't raise the preview cap to "see more" — that floods the flat viewers; use `agentbus reports <id>` (or the busmon ` (+N)` marker) instead. The `full` field lives in the entry and is trimmed with the ~1000-cap report stream — `report` is an activity channel, not the durable audit (that's the verdict ledger).

- [ ] **Step 2: Update `README.md`**

Add to the command reference (matching the one-liner style):

```
agentbus reports                                  # list recent reports (marks truncated ones with (+N))
agentbus reports <id>                             # print one report's full retained text
```

If the README stream/key table has a `{p}:report` row, note the optional `full` field there. Match the file's existing formatting.

- [ ] **Step 3: Update `docs/AGENT-BUS-GUIDE.md`**

Near the REPORT cheat-sheet section, add reader lines (matching the aligned `#`-comment style):

```bash
agentbus reports                                       # list recent reports; (+N) = full text retained
agentbus reports <id>                                  # print that report's full text (multi-line, untruncated)
```

- [ ] **Step 4: Verify no code drift**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: still green (docs-only).

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md docs/AGENT-BUS-GUIDE.md
git commit -m "docs: document retained full report text + reports [<id>]

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review (completed by plan author)

**Spec coverage:**
- `SanitizeReportFull` (keep \n/\t, map other control → space, no collapse, cap 8000 + env) → Task 1 (Step 3) + test (Step 1).
- `Bus.Report` stores `full` only when it differs from preview; preview unchanged → Task 1 (Step 4) + test.
- `Event.Full` + `ParseEntry` report case → Task 1 (Step 4).
- `Bus.Reports` (recent, chronological, Full populated) + `Bus.ReportByID` (errors on unknown) → Task 2.
- `agentbus reports` list (25, `--json`) + `reports <id>` detail (full||preview) → Task 3.
- ` (+N)` marker (N = Full rune length) in the CLI list AND busmon, only when Full non-empty → Task 3 (`reportsTable`) + Task 4 (`reportMarker`).
- Non-goals (no busmon pane, no notify/cmd fidelity, no durable store, preview cap untouched) → nothing in any task touches those.
- Docs → Task 5.

**Placeholder scan:** none — every code/doc step shows the actual content.

**Type consistency:** `SanitizeReportFull(string) string`, `Event.Full string`, `Bus.Reports(ctx,int)([]Event,error)`, `Bus.ReportByID(ctx,string)(Event,error)`, `reportsTable([]bus.Event) string`, `reportDetail(bus.Event) string`, `reportMarker(string) string` are used identically across the tasks that define and consume them. The `(+N)` marker with `N = len([]rune(Full))` is computed identically in `reportsTable` (Task 3) and `reportMarker` (Task 4); the test constant `(+13)` matches `"the\nfull\ntext"` in both.
