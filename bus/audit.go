package bus

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
)

// The audit stream (spec docs/superpowers/specs/2026-11-24-audit-stream-design.md;
// the person's answers: Q1 90 days, Q2 every act of §2, Q3 B a trail with a
// hash chain). One stream per project, {p}:audit, one writer, Audit, which
// only appends: who approved, merged, pushed or forced what, and when.
//
// A trail, not a proof (§3): XADD itself trims (MAXLEN 0, a recent MINID),
// so any user allowed to append can erase. The hash chain detects an entry
// edited, deleted or added in the middle; it does not detect a cut of the
// tail, nor tell the head trimmed by the retention from one cut by hand.

// AuditKey is project's audit stream.
func AuditKey(project string) string { return project + ":audit" }

// AuditActions are §2's acts, the only actions taken; reject joined approve
// at step 2 (master's answer: a verdict either way).
var AuditActions = []string{"approve", "reject", "answer", "merge", "push", "deliver", "force", "ci-handoff", "update", "reinstall",
	"quota-pause", "pause", "resume", "suspend", "retire", "retro-apply",
	"step-merged", "step-abandoned", "parallel"} // sibling-enforce §3

// The outcomes of an act.
const (
	AuditOK      = "ok"
	AuditRefused = "refused"
	AuditFailed  = "failed"
)

// The fields' bounds: detail §2's 300 runes; target and ref name a PR, a
// branch, a task, an agent, a version or a bus id.
const (
	AuditDetailMax = 300
	auditFieldMax  = 200
)

// DefaultAuditRetention is audit.retention's default (Q1: 90 days).
const DefaultAuditRetention = 90 * 24 * time.Hour

// AuditEntry is one line of the trail. ID, At, Prev and Hash are the
// stream's (read back); a writer fills the rest from fixed parts, never from
// free text it received (§2).
type AuditEntry struct {
	ID      string `json:"id,omitempty"`
	At      int64  `json:"at,omitempty"` // Unix ms, Redis TIME at the append
	Action  string `json:"action"`
	Actor   string `json:"actor"`
	Human   string `json:"human,omitempty"` // "yes" when a person's entry caused the act
	Typed   string `json:"typed,omitempty"` // "yes" when the person typed it
	Target  string `json:"target,omitempty"`
	Ref     string `json:"ref,omitempty"` // the bus id that authorises the act
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
	Prev    string `json:"prev,omitempty"` // the previous entry's hash
	Hash    string `json:"hash,omitempty"`
}

// auditFields are an entry's chained fields, in the hash's order.
func auditFields(e AuditEntry) []string {
	return []string{strconv.FormatInt(e.At, 10), e.Action, e.Actor, e.Human, e.Typed, e.Target, e.Ref, e.Outcome, e.Detail}
}

// AuditHash is e's hash: SHA-1 (the one hash Redis scripts have) of the
// previous hash and the chained fields, one per line (no field holds a
// control character).
func AuditHash(e AuditEntry) string {
	sum := sha1.Sum([]byte(e.Prev + "\n" + strings.Join(auditFields(e), "\n")))
	return hex.EncodeToString(sum[:])
}

// auditText: no control character, at most max runes.
func auditText(s string, max int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}

// valid refuses an entry §2 does not allow: nothing is written then.
func (e AuditEntry) valid() error {
	switch {
	case !slices.Contains(AuditActions, e.Action):
		return fmt.Errorf("audit: unknown action %q", e.Action)
	case e.Outcome != AuditOK && e.Outcome != AuditRefused && e.Outcome != AuditFailed:
		return fmt.Errorf("audit: outcome %q, want ok, refused or failed", e.Outcome)
	case !ValidName(e.Actor):
		return fmt.Errorf("audit: actor %q is not a name", e.Actor)
	case e.Human != "" && e.Human != "yes", e.Typed != "" && e.Typed != "yes":
		return errors.New("audit: human and typed are yes or empty")
	case !auditText(e.Target, auditFieldMax), !auditText(e.Ref, auditFieldMax):
		return fmt.Errorf("audit: target and ref hold at most %d runes and no control character", auditFieldMax)
	case !auditText(e.Detail, AuditDetailMax):
		return fmt.Errorf("audit: detail holds at most %d runes and no control character", AuditDetailMax)
	}
	return nil
}

// AuditRetention is audit.retention, the person's alone (an agent must not
// shorten the trail it is in): config.toml, else 90 days.
func AuditRetention(getenv func(string) string) time.Duration {
	if d := config.Machine(getenv).Duration("audit.retention"); d > 0 {
		return d
	}
	return DefaultAuditRetention
}

// Audit appends e with the retention in force; its id.
func (b *Bus) Audit(ctx context.Context, e AuditEntry) (string, error) {
	return b.AuditWith(ctx, e, AuditRetention(os.Getenv))
}

// AuditWarn is told an audit line an act could not write: the act stands
// (a trail never fails the act it records). The CLI's stderr by default.
var AuditWarn = func(err error) { fmt.Fprintln(os.Stderr, "warning: audit:", err) }

// AuditAfter writes e after its act, best effort: a failure goes to
// AuditWarn (the CLI's acts call it, audit step 2).
func (b *Bus) AuditAfter(ctx context.Context, e AuditEntry) {
	if _, err := b.Audit(ctx, e); err != nil {
		AuditWarn(fmt.Errorf("%s by %s: %w", e.Action, e.Actor, err))
	}
}

// auditLua defines audit_append(key, keep, f), and audit_try, the same
// for a fenced script (an error never interrupts its act): it reads the last entry's
// hash, chains the entry of fields f (auditFields' order after At: action,
// actor, human, typed, target, ref, outcome, detail) onto it and appends it,
// trimming by age (MINID at Redis TIME minus keep ms). A script that
// includes it appends in the same step as its own act (audit step 2: the
// dispatcher's acts, fenced with them); two writers never chain onto the
// same entry.
const auditLua = `
local function audit_append(key, keep, f)
  local t = redis.call('TIME')
  local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  local prev = ''
  local last = redis.call('XREVRANGE', key, '+', '-', 'COUNT', 1)
  if #last > 0 then
    local lf = last[1][2]
    for i = 1, #lf, 2 do
      if lf[i] == 'hash' then prev = lf[i + 1] end
    end
  end
  local canon = prev .. '\n' .. tostring(now)
  for i = 1, 8 do canon = canon .. '\n' .. f[i] end
  local hash = redis.sha1hex(canon)
  local minid = tostring(now - tonumber(keep)) .. '-0'
  return redis.call('XADD', key, 'MINID', minid, '*', 'at', tostring(now),
    'action', f[1], 'actor', f[2], 'human', f[3], 'typed', f[4], 'target', f[5],
    'ref', f[6], 'outcome', f[7], 'detail', f[8], 'prev', prev, 'hash', hash)
end
-- audit_try is audit_append for a fenced script, after its act's writes: a
-- Lua error does not roll back what was written before it, so an audit
-- error must never interrupt the act (reviewer-codex): the line is lost,
-- the act stands and records its progress.
local function audit_try(key, keep, f)
  pcall(audit_append, key, keep, f)
end
-- audit_fenced writes the line of the ARGV block from i that fencedAudit
-- built and checked in Go: ARGV[i] '1' a line ('0' none), then the
-- retention and the fields; the detail is ARGV[i+9], or ARGV[i+9] .. n ..
-- ARGV[i+10] when ARGV[i+11] is '1' (n: the count the script computed).
local function audit_fenced(key, i, n)
  if ARGV[i] ~= '1' then return end
  local d = ARGV[i + 9]
  if ARGV[i + 11] == '1' then d = d .. n .. ARGV[i + 10] end
  audit_try(key, ARGV[i + 1], {ARGV[i + 2], ARGV[i + 3], ARGV[i + 4], ARGV[i + 5], ARGV[i + 6], ARGV[i + 7], ARGV[i + 8], d})
end
`

// auditAppend is audit_append alone: KEYS[1] the stream, ARGV[1] the
// retention in ms, ARGV[2..9] the fields.
var auditAppend = redis.NewScript(auditLua + `
return audit_append(KEYS[1], ARGV[1], {ARGV[2], ARGV[3], ARGV[4], ARGV[5], ARGV[6], ARGV[7], ARGV[8], ARGV[9]})`)

// auditArgs are e's retention and fields as audit_append takes them: e
// checked (an entry §2 does not allow is refused) and its free fields each
// replaced whole by [redacted] on a credential shape (§3).
func auditArgs(e AuditEntry, retention time.Duration) ([]any, error) {
	if err := e.valid(); err != nil {
		return nil, err
	}
	if retention <= 0 {
		retention = DefaultAuditRetention
	}
	e.Target, e.Ref, e.Detail = health.Redact(e.Target), health.Redact(e.Ref), health.Redact(e.Detail)
	args := []any{retention.Milliseconds()}
	for _, f := range auditFields(e)[1:] {
		args = append(args, f)
	}
	return args, nil
}

// AuditCountMark marks, in a fenced line's detail, where the script puts the
// count it computes (a hand-off's number): the only part not built in Go.
const AuditCountMark = "{n}"

// fencedAuditArgs is the length of a fencedAudit block.
const fencedAuditArgs = 12

// fencedAudit is e as a fenced script's ARGV block (audit_fenced): e checked
// and redacted in Go by auditArgs, step 1's one path (foureyes), with 999
// standing for the count; the detail split at AuditCountMark. A refused e
// gives a block that writes no line, said on AuditWarn: the act is never
// refused for its line. A detail redacted whole takes no count.
func fencedAudit(e AuditEntry) []any {
	pre, post, mark := strings.Cut(e.Detail, AuditCountMark)
	probe := e
	if mark {
		probe.Detail = pre + "999" + post
	}
	args, err := auditArgs(probe, AuditRetention(os.Getenv))
	if err != nil {
		AuditWarn(fmt.Errorf("%s by %s: %w", e.Action, e.Actor, err))
		out := make([]any, fencedAuditArgs)
		out[0] = "0"
		for i := 1; i < len(out); i++ {
			out[i] = ""
		}
		return out
	}
	out := append([]any{"1"}, args[:8]...) // the flag, the retention, action ... outcome
	if detail, _ := args[8].(string); detail == health.Redacted || !mark {
		return append(out, detail, "", "0")
	}
	return append(out, pre, post, "1")
}

// AuditWith is Audit with retention: e checked (an entry §2 does not allow
// is refused, nothing written), its free fields each replaced whole by
// [redacted] on a credential shape (§3: never a span; the line is still
// written), then appended and chained.
func (b *Bus) AuditWith(ctx context.Context, e AuditEntry, retention time.Duration) (string, error) {
	args, err := auditArgs(e, retention)
	if err != nil {
		return "", err
	}
	return auditAppend.Run(ctx, b.r, []string{AuditKey(b.project)}, args...).Text()
}

func auditOf(m redis.XMessage) AuditEntry {
	s := func(k string) string { v, _ := m.Values[k].(string); return v }
	at, _ := strconv.ParseInt(s("at"), 10, 64)
	return AuditEntry{ID: m.ID, At: at, Action: s("action"), Actor: s("actor"), Human: s("human"), Typed: s("typed"),
		Target: s("target"), Ref: s("ref"), Outcome: s("outcome"), Detail: s("detail"), Prev: s("prev"), Hash: s("hash")}
}

// AuditEntries are the entries from since (Unix ms; 0 all), newest first.
func (b *Bus) AuditEntries(ctx context.Context, since int64) ([]AuditEntry, error) {
	start := "-"
	if since > 0 {
		start = strconv.FormatInt(since, 10)
	}
	ms, err := b.r.XRevRange(ctx, AuditKey(b.project), "+", start).Result()
	if err != nil {
		return nil, err
	}
	out := make([]AuditEntry, 0, len(ms))
	for _, m := range ms {
		out = append(out, auditOf(m))
	}
	return out, nil
}

// AuditLast are the newest count entries, newest first (busmon's Audit
// list, spec §4).
func (b *Bus) AuditLast(ctx context.Context, count int64) ([]AuditEntry, error) {
	ms, err := b.r.XRevRangeN(ctx, AuditKey(b.project), "+", "-", count).Result()
	if err != nil {
		return nil, err
	}
	out := make([]AuditEntry, 0, len(ms))
	for _, m := range ms {
		out = append(out, auditOf(m))
	}
	return out, nil
}

// AuditCheck is AuditVerify's answer. Inconclusive: the head kept moving
// past the check (appends trimming by age): neither whole nor broken.
type AuditCheck struct {
	OK           bool   `json:"ok"`
	Inconclusive bool   `json:"inconclusive,omitempty"`
	Entries      int    `json:"entries"`
	First        string `json:"first,omitempty"`
	Last         string `json:"last,omitempty"`
	BrokenAt     string `json:"broken_at,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// auditVerifyPage is verify's page; auditBetweenPages, a test's hook.
var (
	auditVerifyPage   int64 = 500
	auditBetweenPages func()
)

// auditVerifyTries bounds the restarts when the head moves past the check.
const auditVerifyTries = 3

// AuditVerify checks the chain, oldest first: each entry's hash is its
// fields', and each follows the previous entry. The first entry's own
// previous is not checked (the retention trims the head). A cut of the tail
// is not detected (§6 Q3 B). The stream is live: an append's trim that
// passes the check's cursor between pages (reviewer-codex) starts the check
// again from the new head; one still moving after auditVerifyTries is
// inconclusive, never a break.
func (b *Bus) AuditVerify(ctx context.Context) (AuditCheck, error) {
	for range auditVerifyTries {
		c, moved, err := b.auditVerifyOnce(ctx)
		if err != nil || !moved {
			return c, err
		}
	}
	return AuditCheck{Inconclusive: true, Reason: "the head moved past the check at every try (appends trimming by age): run it again"}, nil
}

// auditVerifyOnce is one pass; moved: the head was trimmed past its cursor.
func (b *Bus) auditVerifyOnce(ctx context.Context) (c AuditCheck, moved bool, err error) {
	prev, start := "", "-"
	for {
		ms, err := b.r.XRangeN(ctx, AuditKey(b.project), start, "+", auditVerifyPage).Result()
		if err != nil {
			return c, false, err
		}
		for _, m := range ms {
			e := auditOf(m)
			switch {
			case e.Hash != AuditHash(e):
				c.BrokenAt, c.Reason = e.ID, "its hash does not match its fields: edited, or not written by the audit writer"
				return c, false, nil
			case c.Entries > 0 && e.Prev != prev:
				// The entry read last trimmed with everything before it: the
				// retention passed the cursor, not a deletion.
				if trimmed, err := b.auditTrimmedPast(ctx, c.Last); err != nil || trimmed {
					return c, trimmed, err
				}
				c.BrokenAt, c.Reason = e.ID, "it does not follow the previous entry: one before it was deleted, or it was inserted"
				return c, false, nil
			}
			if c.Entries == 0 {
				c.First = e.ID
			}
			c.Entries++
			c.Last, prev = e.ID, e.Hash
		}
		if int64(len(ms)) < auditVerifyPage {
			c.OK = true
			return c, false, nil
		}
		start = "(" + ms[len(ms)-1].ID
		if auditBetweenPages != nil {
			auditBetweenPages()
		}
	}
}

// auditTrimmedPast: the stream now starts after id (or is empty): the head
// was trimmed past it.
func (b *Bus) auditTrimmedPast(ctx context.Context, id string) (bool, error) {
	first, err := b.r.XRangeN(ctx, AuditKey(b.project), "-", "+", 1).Result()
	if err != nil {
		return false, err
	}
	return len(first) == 0 || idLess(id, first[0].ID), nil
}
