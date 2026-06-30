# Verdict Ledger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a durable, queryable 4-eyes verdict ledger so `agentbus verdicts --pr 25` mechanically answers "what is the review state of PR #25?", and make recording a verdict subject-first so it captures reviews requested via plain `cmd` too.

**Architecture:** A new append-only Redis stream `{project}:verdicts` holds one entry per verdict (subject/author/reviewer/decision/message/ref). The `bus` package owns the transport (`AppendVerdict`, `Verdicts`); the `agentbus` CLI owns the policy — a pure roll-up (`rollUp`) that computes APPROVED/REJECTED/PENDING under a frozen independence rule, plus rendering. The existing `verdict` command becomes subject-first and additive: it always writes the ledger, still publishes a live `CmdVerdict`, and resolves a matching gate only as a best-effort bonus.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, module `github.com/netbja/agent-bus-monitor`. Tests use the repo's `dialTest` helper (real dev broker, skips if Redis is down).

## Global Constraints

- Go 1.26; module `github.com/netbja/agent-bus-monitor`. Protocol changes go in `bus/stream.go` (transport) — not in the binaries.
- All UI/CLI text (usage, errors, output) is **English**.
- **Do not touch the `subscribe`/`:cmd` wire format.** This feature adds a new stream + new read command; the only change to an existing command is `verdict`, and it is purely additive/loosening — so armed subscribers cannot break.
- Verdict stream cap: default **10000**, override via `AGENT_BUS_VERDICT_MAX` (positive int).
- Exit codes for `verdicts <subject>`: `0`=APPROVED, `2`=PENDING, `3`=REJECTED. `1` stays reserved for usage/connection errors (the `die` path).
- `author` positional is **required** on `verdict` (needed to evaluate reviewer-≠-author independence).
- Self-approval (reviewer == author) is **recorded** but **never counted** toward APPROVED by the roll-up.
- Bus-level tests need Redis: run `docker compose up -d` first (port 6380). `dialTest` skips them if the broker is down.

## File Structure

- `bus/stream.go` — **Modify.** Add `VerdictsKey`, the `Verdict` type, `AppendVerdict`, `Verdicts`.
- `bus/bus.go` — **Modify.** Add `defaultVerdictMax` + `verdictMaxLen()` next to the existing `defaultReportMax`/`reportMaxLen()` (it already imports `os`; `stream.go` does not).
- `bus/stream_test.go` — **Modify.** Add ledger tests; extend `dialTest` cleanup to delete `VerdictsKey(project)`.
- `cmd/agentbus/verdicts.go` — **Create.** Pure policy + rendering: `resolveSubject`, `rollUp`, `verdictsReport`, `verdictsOverview`, `exitForState`. Mirrors `agents.go` (reuses its `humanAge`).
- `cmd/agentbus/verdicts_test.go` — **Create.** Unit tests for the pure functions (no Redis).
- `cmd/agentbus/main.go` — **Modify.** Rework the `verdict` case (subject-first, optional `--ref`, best-effort resolve, ledger append); add the `verdicts` case; update the usage banner and the doc-comment header.
- `README.md`, `CLAUDE.md` — **Modify.** Document the new stream key, the two commands, and the roll-up/exit-code contract.

---

### Task 1: Bus verdict ledger primitives

**Files:**
- Modify: `bus/bus.go` (add `defaultVerdictMax` + `verdictMaxLen()`)
- Modify: `bus/stream.go` (add `VerdictsKey`, `Verdict`, `AppendVerdict`, `Verdicts`)
- Test: `bus/stream_test.go` (add tests; extend `dialTest` cleanup)

**Interfaces:**
- Consumes: existing `bus` internals — `(*Bus).r` (`*redis.Client`), `(*Bus).project`, `ValidName`, `splitID`, `toStringMap`.
- Produces (later tasks rely on these exact signatures):
  - `func VerdictsKey(project string) string`
  - `type Verdict struct { ID, Subject, Author, Reviewer, Decision, Message, Ref string; TS int64 }`
  - `func (b *Bus) AppendVerdict(ctx context.Context, v Verdict) (string, error)`
  - `func (b *Bus) Verdicts(ctx context.Context, subject string) ([]Verdict, error)` — returns entries oldest→newest; `subject == ""` returns all.

- [ ] **Step 1: Write the failing bus tests**

Add to `bus/stream_test.go`:

```go
func TestVerdictsKeyNaming(t *testing.T) {
	if got := VerdictsKey("busmon"); got != "busmon:verdicts" {
		t.Fatalf("VerdictsKey = %q, want busmon:verdicts", got)
	}
}

func TestVerdictLedger(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.AppendVerdict(ctx, Verdict{
		Subject: "pr:7", Author: "claude1", Reviewer: "claude2",
		Decision: "approve", Message: "LGTM", Ref: "r1",
	}); err != nil {
		t.Fatalf("AppendVerdict approve: %v", err)
	}
	if _, err := b.AppendVerdict(ctx, Verdict{
		Subject: "pr:8", Author: "claude1", Reviewer: "claude3", Decision: "reject",
	}); err != nil {
		t.Fatalf("AppendVerdict reject: %v", err)
	}
	all, err := b.Verdicts(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("Verdicts(all) = %d entries (%v), want 2", len(all), err)
	}
	if all[0].Subject != "pr:7" || all[0].Reviewer != "claude2" ||
		all[0].Decision != "approve" || all[0].Message != "LGTM" || all[0].Ref != "r1" {
		t.Fatalf("first verdict fields wrong: %+v", all[0])
	}
	if all[0].TS == 0 {
		t.Fatalf("verdict TS not derived from id: %+v", all[0])
	}
	only, err := b.Verdicts(ctx, "pr:8")
	if err != nil || len(only) != 1 || only[0].Decision != "reject" {
		t.Fatalf("Verdicts(pr:8) = %+v (%v), want 1 reject", only, err)
	}
}

func TestAppendVerdictValidation(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	bad := []Verdict{
		{Subject: "pr:1", Author: "Bad Author", Reviewer: "claude2", Decision: "approve"},
		{Subject: "pr:1", Author: "claude1", Reviewer: "claude2", Decision: "maybe"},
		{Subject: "", Author: "claude1", Reviewer: "claude2", Decision: "approve"},
	}
	for i, v := range bad {
		if _, err := b.AppendVerdict(ctx, v); err == nil {
			t.Errorf("case %d: AppendVerdict accepted invalid %+v, want error", i, v)
		}
	}
}
```

Also extend the `dialTest` cleanup so the new stream is removed. Change the `r.Del(...)` line in `dialTest` to include `VerdictsKey(project)`:

```go
		r.Del(ctx, StreamKey(project, "status"), StreamKey(project, "report"),
			StreamKey(project, "notify"), StreamKey(project, "cmd"), PilotKey(project),
			AgentsKey(project), UsageKey(project), VerdictsKey(project))
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `docker compose up -d && go test ./bus/ -run 'Verdict' -count=1 -v`
Expected: FAIL — `undefined: VerdictsKey`, `undefined: AppendVerdict`, `undefined: Verdicts`, `undefined: Verdict` (compile error).

- [ ] **Step 3: Add the cap helper to `bus/bus.go`**

Insert directly below the existing `reportMaxLen()` function (after line ~36):

```go
const defaultVerdictMax = 10000

// verdictMaxLen resolves the verdict-ledger cap: AGENT_BUS_VERDICT_MAX if it
// parses to a positive int, else defaultVerdictMax (10000). The ledger is the
// money-path audit trail, so it is capped far higher than the 1000-entry
// activity streams — low volume, but we keep ~all of it.
func verdictMaxLen() int {
	if v := os.Getenv("AGENT_BUS_VERDICT_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultVerdictMax
}
```

(`bus.go` already imports `os` and `strconv` — no import change.)

- [ ] **Step 4: Add the ledger API to `bus/stream.go`**

Add the key constructor next to the other `*Key` funcs (after `AgentsKey`, ~line 51):

```go
// VerdictsKey is the per-project append-only ledger of 4-eyes verdicts
// ({project}:verdicts). Unlike the activity streams it is capped at
// verdictMaxLen() (default 10000) because it is the money-path audit trail.
func VerdictsKey(project string) string { return project + ":verdicts" }
```

Add the type near `AgentSnapshot` (after ~line 59):

```go
// Verdict is one entry in the {project}:verdicts ledger. Subject keys the review
// target (e.g. "pr:25"); Author is the agent under review and Reviewer the agent
// issuing the verdict. Ref optionally links to a challenge thread. TS is the ms
// timestamp derived from the stream id.
type Verdict struct {
	ID       string `json:"id"`
	Subject  string `json:"subject"`
	Author   string `json:"author"`
	Reviewer string `json:"reviewer"`
	Decision string `json:"decision"` // approve | reject
	Message  string `json:"message,omitempty"`
	Ref      string `json:"ref,omitempty"`
	TS       int64  `json:"ts"`
}
```

Add the two methods near the other `Bus` publish helpers (e.g. after `Cmd`, ~line 208). Note these `XAdd`/`XRange` directly rather than via `b.add`, because `b.add` hardcodes the 1000-entry `streamMaxLen`:

```go
// AppendVerdict records one verdict in the {project}:verdicts ledger and returns
// the new entry id. It writes unconditionally — including self-approvals
// (reviewer == author) — so the audit trail is complete; the independence rule
// is enforced only at read time by the roll-up. The cap is verdictMaxLen().
func (b *Bus) AppendVerdict(ctx context.Context, v Verdict) (string, error) {
	if !ValidName(v.Author) {
		return "", fmt.Errorf("invalid author %q", v.Author)
	}
	if !ValidName(v.Reviewer) {
		return "", fmt.Errorf("invalid reviewer %q", v.Reviewer)
	}
	if v.Subject == "" {
		return "", fmt.Errorf("verdict subject required")
	}
	if v.Decision != "approve" && v.Decision != "reject" {
		return "", fmt.Errorf("verdict decision must be approve or reject")
	}
	return b.r.XAdd(ctx, &redis.XAddArgs{
		Stream: VerdictsKey(b.project),
		MaxLen: int64(verdictMaxLen()),
		Approx: true,
		Values: map[string]interface{}{
			"subject": v.Subject, "author": v.Author, "reviewer": v.Reviewer,
			"decision": v.Decision, "message": v.Message, "ref": v.Ref,
		},
	}).Result()
}

// Verdicts returns ledger entries oldest→newest (XRANGE is ascending, which the
// CLI roll-up relies on). If subject != "", only entries for that subject are
// returned; subject == "" returns the whole ledger.
func (b *Bus) Verdicts(ctx context.Context, subject string) ([]Verdict, error) {
	msgs, err := b.r.XRange(ctx, VerdictsKey(b.project), "-", "+").Result()
	if err != nil {
		return nil, err
	}
	out := make([]Verdict, 0, len(msgs))
	for _, m := range msgs {
		f := toStringMap(m.Values)
		if subject != "" && f["subject"] != subject {
			continue
		}
		ms, _ := splitID(m.ID)
		out = append(out, Verdict{
			ID: m.ID, Subject: f["subject"], Author: f["author"],
			Reviewer: f["reviewer"], Decision: f["decision"],
			Message: f["message"], Ref: f["ref"], TS: ms,
		})
	}
	return out, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./bus/ -run 'Verdict' -count=1 -v`
Expected: PASS — `TestVerdictsKeyNaming`, `TestVerdictLedger`, `TestAppendVerdictValidation`.

- [ ] **Step 6: Build + vet the whole module**

Run: `go build ./... && go vet ./...`
Expected: no output (success).

- [ ] **Step 7: Commit**

```bash
git add bus/bus.go bus/stream.go bus/stream_test.go
git commit -m "feat(bus): {project}:verdicts ledger — Verdict, AppendVerdict, Verdicts

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: CLI roll-up & rendering (pure functions)

**Files:**
- Create: `cmd/agentbus/verdicts.go`
- Test: `cmd/agentbus/verdicts_test.go`

**Interfaces:**
- Consumes: `bus.Verdict` (from Task 1); `humanAge(time.Duration) string` (existing, `cmd/agentbus/agents.go`).
- Produces (Task 3 relies on these):
  - `func resolveSubject(pr, subject string) (string, error)`
  - `func rollUp(vs []bus.Verdict) (state string, deciderIdx int)` — `state ∈ {APPROVED,REJECTED,PENDING}`; `deciderIdx` is the index of the deciding entry or `-1`.
  - `func verdictsReport(subject string, vs []bus.Verdict, now time.Time) (out string, exitCode int)`
  - `func verdictsOverview(vs []bus.Verdict, now time.Time) string`
  - `func exitForState(state string) int`

- [ ] **Step 1: Write the failing tests**

Create `cmd/agentbus/verdicts_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestResolveSubject(t *testing.T) {
	if s, err := resolveSubject("25", ""); err != nil || s != "pr:25" {
		t.Fatalf("resolveSubject(pr=25) = (%q,%v), want pr:25", s, err)
	}
	if s, err := resolveSubject("", "commit:abc"); err != nil || s != "commit:abc" {
		t.Fatalf("resolveSubject(subject) = (%q,%v), want commit:abc", s, err)
	}
	if _, err := resolveSubject("25", "commit:abc"); err == nil {
		t.Error("resolveSubject with both --pr and --subject should error")
	}
	if _, err := resolveSubject("", ""); err == nil {
		t.Error("resolveSubject with neither should error")
	}
}

func TestRollUp(t *testing.T) {
	mk := func(reviewer, author, decision string) bus.Verdict {
		return bus.Verdict{Reviewer: reviewer, Author: author, Decision: decision}
	}
	cases := []struct {
		name string
		vs   []bus.Verdict
		want string
	}{
		{"independent approve", []bus.Verdict{mk("claude2", "claude1", "approve")}, "APPROVED"},
		{"self approve only", []bus.Verdict{mk("claude1", "claude1", "approve")}, "PENDING"},
		{"reject after approve", []bus.Verdict{mk("claude2", "claude1", "approve"), mk("claude3", "claude1", "reject")}, "REJECTED"},
		{"reject then approve", []bus.Verdict{mk("claude3", "claude1", "reject"), mk("claude2", "claude1", "approve")}, "APPROVED"},
		{"empty", nil, "PENDING"},
	}
	for _, c := range cases {
		if got, _ := rollUp(c.vs); got != c.want {
			t.Errorf("%s: rollUp = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestVerdictsReport(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	ts := func(secAgo int) int64 { return now.Add(-time.Duration(secAgo) * time.Second).UnixMilli() }
	vs := []bus.Verdict{
		{Subject: "pr:25", Reviewer: "claude3", Author: "claude1", Decision: "reject", Message: "typo", TS: ts(120)},
		{Subject: "pr:25", Reviewer: "claude2", Author: "claude1", Decision: "approve", Message: "LGTM", TS: ts(60)},
	}
	out, code := verdictsReport("pr:25", vs, now)
	if code != 0 {
		t.Fatalf("APPROVED should exit 0, got %d", code)
	}
	if !strings.Contains(out, "APPROVED") || !strings.Contains(out, "claude2") {
		t.Fatalf("report missing approved header: %q", out)
	}
	if !strings.Contains(out, "(superseded)") {
		t.Fatalf("older reject should be marked superseded: %q", out)
	}
	selfVs := []bus.Verdict{
		{Subject: "pr:26", Reviewer: "claude1", Author: "claude1", Decision: "approve", TS: ts(10)},
	}
	out2, code2 := verdictsReport("pr:26", selfVs, now)
	if code2 != 2 {
		t.Fatalf("PENDING should exit 2, got %d", code2)
	}
	if !strings.Contains(out2, "ignored") {
		t.Fatalf("self-approval should be annotated ignored: %q", out2)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/agentbus/ -run 'ResolveSubject|RollUp|VerdictsReport' -count=1 -v`
Expected: FAIL — `undefined: resolveSubject`, `undefined: rollUp`, `undefined: verdictsReport` (compile error).

- [ ] **Step 3: Write the implementation**

Create `cmd/agentbus/verdicts.go`:

```go
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// overviewLimit bounds the no-argument `verdicts` listing (matches busmon's
// default activity window).
const overviewLimit = 25

// resolveSubject maps the --pr / --subject flags to a single subject key.
// Exactly one must be set: --pr N becomes "pr:N"; --subject S is used verbatim.
func resolveSubject(pr, subject string) (string, error) {
	pr = strings.TrimSpace(pr)
	subject = strings.TrimSpace(subject)
	switch {
	case pr != "" && subject != "":
		return "", fmt.Errorf("pass only one of --pr or --subject")
	case pr != "":
		return "pr:" + pr, nil
	case subject != "":
		return subject, nil
	default:
		return "", fmt.Errorf("a subject is required: pass --pr N or --subject S")
	}
}

// rollUp computes the 4-eyes state of a subject from its verdicts, which MUST be
// ordered oldest→newest (as bus.Verdicts returns them). An approve counts only
// when reviewer != author. A reject supersedes earlier verdicts, but a later
// independent approve wins again. Returns the state and the index of the
// deciding entry (-1 for PENDING).
func rollUp(vs []bus.Verdict) (state string, deciderIdx int) {
	indepApproveIdx, lastRejectIdx := -1, -1
	for i := range vs {
		switch vs[i].Decision {
		case "approve":
			if vs[i].Reviewer != vs[i].Author {
				indepApproveIdx = i
			}
		case "reject":
			lastRejectIdx = i
		}
	}
	switch {
	case indepApproveIdx >= 0 && indepApproveIdx > lastRejectIdx:
		return "APPROVED", indepApproveIdx
	case lastRejectIdx >= 0:
		return "REJECTED", lastRejectIdx
	default:
		return "PENDING", -1
	}
}

// exitForState maps a roll-up state to a scriptable exit code (1 is reserved for
// usage/connection errors).
func exitForState(state string) int {
	switch state {
	case "APPROVED":
		return 0
	case "REJECTED":
		return 3
	default: // PENDING
		return 2
	}
}

// verdictsReport renders the roll-up header plus one detail line per verdict
// (oldest→newest), and returns the matching exit code. Entries older than the
// deciding entry are marked "(superseded)"; self-approvals are marked
// "(author=…, ignored)".
func verdictsReport(subject string, vs []bus.Verdict, now time.Time) (string, int) {
	state, deciderIdx := rollUp(vs)
	var sb strings.Builder
	switch {
	case deciderIdx >= 0:
		d := vs[deciderIdx]
		mark := "✓"
		if state == "REJECTED" {
			mark = "✗"
		}
		fmt.Fprintf(&sb, "%s: %s %s (%s, %s)\n", subject, state, mark,
			d.Reviewer, humanAge(now.Sub(time.UnixMilli(d.TS))))
	case hasSelfApprove(vs):
		fmt.Fprintf(&sb, "%s: PENDING (only self-approval)\n", subject)
	default:
		fmt.Fprintf(&sb, "%s: PENDING\n", subject)
	}
	for i := range vs {
		v := vs[i]
		fmt.Fprintf(&sb, "  %-9s %-7s %-12s", humanAge(now.Sub(time.UnixMilli(v.TS))), v.Decision, v.Reviewer)
		if v.Message != "" {
			fmt.Fprintf(&sb, "  %q", v.Message)
		}
		if v.Ref != "" {
			fmt.Fprintf(&sb, "  ref=%s", v.Ref)
		}
		switch {
		case v.Reviewer == v.Author:
			fmt.Fprintf(&sb, "  (author=%s, ignored)", v.Author)
		case deciderIdx >= 0 && i < deciderIdx:
			sb.WriteString("  (superseded)")
		}
		sb.WriteByte('\n')
	}
	return sb.String(), exitForState(state)
}

// hasSelfApprove reports whether any verdict is a self-approval (used only to
// annotate a PENDING header).
func hasSelfApprove(vs []bus.Verdict) bool {
	for _, v := range vs {
		if v.Decision == "approve" && v.Reviewer == v.Author {
			return true
		}
	}
	return false
}

// verdictsOverview lists the most recent overviewLimit verdicts across all
// subjects, one line each, oldest→newest. No per-subject roll-up.
func verdictsOverview(vs []bus.Verdict, now time.Time) string {
	if len(vs) > overviewLimit {
		vs = vs[len(vs)-overviewLimit:]
	}
	var sb strings.Builder
	for _, v := range vs {
		fmt.Fprintf(&sb, "%-9s %-12s %-7s %-12s  %q\n",
			humanAge(now.Sub(time.UnixMilli(v.TS))), v.Subject, v.Decision, v.Reviewer, v.Message)
	}
	return sb.String()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/agentbus/ -run 'ResolveSubject|RollUp|VerdictsReport' -count=1 -v`
Expected: PASS — all three tests.

- [ ] **Step 5: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output (success).

- [ ] **Step 6: Commit**

```bash
git add cmd/agentbus/verdicts.go cmd/agentbus/verdicts_test.go
git commit -m "feat(agentbus): verdict roll-up + rendering (subject, state, exit codes)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Wire the `verdict` and `verdicts` CLI commands

**Files:**
- Modify: `cmd/agentbus/main.go` (rework the `verdict` case ~lines 155-174; add a `verdicts` case; update the usage banner ~line 69 and the doc-comment header ~lines 13-23)

**Interfaces:**
- Consumes: `bus.Verdict`, `(*Bus).AppendVerdict`, `(*Bus).Verdicts` (Task 1); `resolveSubject`, `verdictsReport`, `verdictsOverview` (Task 2); existing `extractFlag`, `b.Cmd`, `b.ResolveChallenge`, `bus.CmdVerdict`.
- Produces: the user-facing `verdict` / `verdicts` commands. No symbols consumed by later tasks.

- [ ] **Step 1: Replace the `verdict` case**

In `cmd/agentbus/main.go`, replace the entire current `case "verdict":` block (from `case "verdict":` through its closing before `case "pilot":`) with:

```go
	case "verdict":
		rest, ref := extractFlag(rest, "--ref")
		rest, pr := extractFlag(rest, "--pr")
		rest, subjectFlag := extractFlag(rest, "--subject")
		subject, serr := resolveSubject(pr, subjectFlag)
		if serr != nil {
			die(serr.Error())
		}
		if len(rest) < 2 {
			die("usage: verdict (--pr N | --subject S) [--ref R] <author> <approve|reject> [message]")
		}
		author, decision := rest[0], rest[1]
		if decision != "approve" && decision != "reject" {
			die("verdict decision must be approve or reject")
		}
		rationale := strings.Join(rest[2:], " ")
		// 1. Durable ledger entry — always, including self-approvals (the
		//    independence rule is enforced at read time, not here).
		if _, err := b.AppendVerdict(ctx, bus.Verdict{
			Subject: subject, Author: author, Reviewer: self,
			Decision: decision, Message: rationale, Ref: ref,
		}); err != nil {
			die(err.Error())
		}
		// 2. Live notification on :cmd so busmon and the author see it (as before).
		cmdMsg := decision
		if rationale != "" {
			cmdMsg += ": " + rationale
		}
		if _, err := b.Cmd(ctx, self, author, bus.CmdVerdict, ref, cmdMsg); err != nil {
			die(err.Error())
		}
		// 3. Bonus gate resolution: best-effort, only when --ref names an open
		//    challenge. A missing/stale ref is a notice, not a fatal error — that
		//    is what lets a cmd-requested review (no challenge) still be recorded.
		if ref != "" {
			if err := b.ResolveChallenge(ctx, author, ref); err != nil {
				fmt.Fprintln(os.Stderr, "notice: "+err.Error())
			}
		}
		fmt.Printf("verdict recorded: %s %s on %s\n", decision, subject, author)
```

- [ ] **Step 2: Add the `verdicts` case**

Immediately after the `verdict` case, add:

```go
	case "verdicts":
		rest, pr := extractFlag(rest, "--pr")
		rest, subjectFlag := extractFlag(rest, "--subject")
		if strings.TrimSpace(pr) == "" && strings.TrimSpace(subjectFlag) == "" {
			vs, err := b.Verdicts(ctx, "") // overview: all subjects
			if err != nil {
				die(err.Error())
			}
			fmt.Print(verdictsOverview(vs, time.Now()))
			return
		}
		subject, serr := resolveSubject(pr, subjectFlag)
		if serr != nil {
			die(serr.Error())
		}
		vs, err := b.Verdicts(ctx, subject)
		if err != nil {
			die(err.Error())
		}
		out, code := verdictsReport(subject, vs, time.Now())
		fmt.Print(out)
		os.Exit(code)
```

- [ ] **Step 3: Update the usage banner and doc-comment header**

In the top-level usage `die` (the `len(args) < 1` branch, ~line 69), add `verdicts` to the command list:

```go
		die("usage: agentbus --project <p> <status|report|notify|cmd|challenge|reply|verdict|verdicts|pilot|gate|agents|pane|usage|subscribe|watch|listen> ...")
```

In the doc-comment header (~lines 13-16), replace the `verdict` usage line and add a `verdicts` line:

```go
//	agentbus --project P verdict   (--pr N | --subject S) [--ref R] <author> <approve|reject> [msg...]  # records to the ledger; resolves a matching gate as a bonus
//	agentbus --project P verdicts  [--pr N | --subject S]   # roll-up 4-eyes state of a subject (exit 0=approved/2=pending/3=rejected), or recent across all
```

- [ ] **Step 4: Build + vet, run all unit tests**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: build/vet clean; all tests PASS (bus tests skip if Redis is down — bring it up with `docker compose up -d` to actually exercise them).

- [ ] **Step 5: End-to-end smoke test against the dev broker**

Run:

```bash
docker compose up -d
go build -o agentbus ./cmd/agentbus
P=demo-$RANDOM
# independent approve → APPROVED, exit 0
AGENT_BUS_PROJECT=$P AGENT_BUS_AGENT=claude2 ./agentbus verdict --pr 25 claude1 approve "LGTM"
AGENT_BUS_PROJECT=$P ./agentbus verdicts --pr 25; echo "exit=$?"
# self-approval → PENDING, exit 2
AGENT_BUS_PROJECT=$P AGENT_BUS_AGENT=claude1 ./agentbus verdict --pr 26 claude1 approve "self"
AGENT_BUS_PROJECT=$P ./agentbus verdicts --pr 26; echo "exit=$?"
# overview
AGENT_BUS_PROJECT=$P ./agentbus verdicts
```

Expected:
- `pr:25` → `APPROVED ✓ (claude2, …)`, `exit=0`.
- `pr:26` → `PENDING (only self-approval)` with an `(author=claude1, ignored)` detail line, `exit=2`.
- overview lists both `pr:25` and `pr:26` lines.

- [ ] **Step 6: Commit**

```bash
git add cmd/agentbus/main.go
git commit -m "feat(agentbus): subject-first verdict + queryable verdicts command

verdict is now subject-first (--pr/--subject), always writes the ledger,
keeps the live CmdVerdict, and resolves a matching gate only as a
best-effort bonus (no longer dies without an open challenge). verdicts
returns a roll-up with scriptable exit codes.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Documentation

**Files:**
- Modify: `README.md` (stream table + command reference)
- Modify: `CLAUDE.md` (stream layout, the agentbus verb list, a "Things that bite" note)

**Interfaces:** none (docs only).

- [ ] **Step 1: Update `CLAUDE.md`**

Make three edits so the docs match the code:

1. In the **Stream layout** section, add a row to the stream table and document the new key. After the `{p}:cmd` row, the additional key list currently reads "Additional keys: `{p}:pilot` … `{p}:gate:{agent}` …". Append:
   > `{p}:verdicts` (Redis stream, append-only 4-eyes ledger; one entry per verdict — subject/author/reviewer/decision/message/ref — capped at `verdictMaxLen()` = 10000, override `AGENT_BUS_VERDICT_MAX`).

2. In the **`agentbus`** bullet (the fire-and-forget verb list), add `verdicts` to the list of verbs and note: "`verdict` is subject-first (`--pr N`/`--subject S`), always appends to the `{p}:verdicts` ledger, publishes a live `CmdVerdict`, and resolves a matching `--ref` gate only as a best-effort bonus (it no longer errors when no challenge is open). `verdicts [--pr N|--subject S]` prints the roll-up 4-eyes state (`APPROVED`/`REJECTED`/`PENDING`) with exit codes 0/3/2; no-arg lists recent verdicts across all subjects."

3. In **Things that bite**, add a bullet:
   > - **The verdict ledger is the audit source of truth, the gate is just a lock.** `verdict` writes `{p}:verdicts` unconditionally (self-approvals included — the roll-up ignores them, but the record stays). The roll-up rule: APPROVED iff the latest independent approve (reviewer ≠ author) is newer than any reject. Don't gate "was this approved?" on the `{p}:gate:{agent}` hash — that only holds *open* challenges; query `agentbus verdicts` instead.

- [ ] **Step 2: Update `README.md`**

Add `{p}:verdicts` to the stream/key reference table (mirroring the existing rows), and add `verdict`/`verdicts` to the command reference with a one-line description of the roll-up and the 0/2/3 exit codes. Match the surrounding table/section formatting already in the file.

- [ ] **Step 3: Verify the docs build nothing / no code drift**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: still green (docs-only change; this catches an accidental code edit).

- [ ] **Step 4: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs: document the {project}:verdicts ledger + verdict/verdicts commands

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review (completed by plan author)

**Spec coverage:**
- Stream `{p}:verdicts`, cap 10000 + `AGENT_BUS_VERDICT_MAX` → Task 1 (Steps 3-4).
- Entry schema (subject/author/reviewer/decision/message/ref + TS) → Task 1 (`Verdict`, `Verdicts`).
- `AppendVerdict` validation (author/reviewer/subject/decision) → Task 1 (Step 4 + test Step 1).
- Subject-first `verdict`, `--pr`/`--subject`, optional `--ref`, best-effort resolve, no-longer-fatal-without-challenge, live CmdVerdict, self-approval recorded → Task 3 (Step 1).
- `verdicts` roll-up (frozen rule), header/detail/superseded/ignored markers, exit codes 0/2/3, no-arg overview → Task 2 + Task 3 (Step 2).
- Independence enforced at read time only → Task 2 (`rollUp` reviewer != author) + Task 1 writes unconditionally.
- Tests (round-trip, subject filter, 4 roll-up cases, validation, exit codes) → Task 1 + Task 2 test steps.
- Docs (stream key, commands, roll-up/exit contract) → Task 4.
- Non-goals (busmon, reply/directive ledgering, ack/TTL/identity/versioning) → not implemented, as intended.

**Placeholder scan:** none — every code/doc step shows the actual content.

**Type consistency:** `Verdict` fields and the `AppendVerdict`/`Verdicts`/`rollUp`/`verdictsReport`/`resolveSubject` signatures are identical across Tasks 1-3. `rollUp` returns `(state, deciderIdx)` and is consumed only inside `verdictsReport` in the same file. Exit codes 0/2/3 are consistent between `exitForState`, the spec, and the smoke test.
