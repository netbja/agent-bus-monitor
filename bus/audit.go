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

// AuditActions are §2's acts, the only actions taken.
var AuditActions = []string{"approve", "answer", "merge", "push", "deliver", "force", "ci-handoff", "update", "reinstall",
	"quota-pause", "pause", "resume", "suspend", "retire", "retro-apply"}

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

// auditAppend reads the last entry's hash, chains e onto it and appends it,
// trimming by age (MINID at Redis TIME minus ARGV[1] ms), in one script: two
// writers never chain onto the same entry. ARGV[2..9] are e's fields after
// At, in auditFields' order.
var auditAppend = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local prev = ''
local last = redis.call('XREVRANGE', KEYS[1], '+', '-', 'COUNT', 1)
if #last > 0 then
  local f = last[1][2]
  for i = 1, #f, 2 do
    if f[i] == 'hash' then prev = f[i + 1] end
  end
end
local canon = prev .. '\n' .. tostring(now)
for i = 2, 9 do canon = canon .. '\n' .. ARGV[i] end
local hash = redis.sha1hex(canon)
local minid = tostring(now - tonumber(ARGV[1])) .. '-0'
return redis.call('XADD', KEYS[1], 'MINID', minid, '*', 'at', tostring(now),
  'action', ARGV[2], 'actor', ARGV[3], 'human', ARGV[4], 'typed', ARGV[5], 'target', ARGV[6],
  'ref', ARGV[7], 'outcome', ARGV[8], 'detail', ARGV[9], 'prev', prev, 'hash', hash)`)

// AuditWith is Audit with retention: e checked (an entry §2 does not allow
// is refused, nothing written), its free fields each replaced whole by
// [redacted] on a credential shape (§3: never a span; the line is still
// written), then appended and chained.
func (b *Bus) AuditWith(ctx context.Context, e AuditEntry, retention time.Duration) (string, error) {
	if err := e.valid(); err != nil {
		return "", err
	}
	if retention <= 0 {
		retention = DefaultAuditRetention
	}
	e.Target, e.Ref, e.Detail = health.Redact(e.Target), health.Redact(e.Ref), health.Redact(e.Detail)
	args := []any{retention.Milliseconds()}
	for _, f := range auditFields(e)[1:] {
		args = append(args, f)
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

// AuditCheck is AuditVerify's answer.
type AuditCheck struct {
	OK       bool   `json:"ok"`
	Entries  int    `json:"entries"`
	First    string `json:"first,omitempty"`
	Last     string `json:"last,omitempty"`
	BrokenAt string `json:"broken_at,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// AuditVerify checks the chain, oldest first: each entry's hash is its
// fields', and each follows the previous entry. The first entry's own
// previous is not checked (the retention trims the head). A cut of the tail
// is not detected (§6 Q3 B).
func (b *Bus) AuditVerify(ctx context.Context) (AuditCheck, error) {
	var c AuditCheck
	prev, start := "", "-"
	for {
		ms, err := b.r.XRangeN(ctx, AuditKey(b.project), start, "+", 500).Result()
		if err != nil {
			return c, err
		}
		for _, m := range ms {
			e := auditOf(m)
			switch {
			case e.Hash != AuditHash(e):
				c.BrokenAt, c.Reason = e.ID, "its hash does not match its fields: edited, or not written by the audit writer"
			case c.Entries > 0 && e.Prev != prev:
				c.BrokenAt, c.Reason = e.ID, "it does not follow the previous entry: one before it was deleted, or it was inserted"
			}
			if c.BrokenAt != "" {
				return c, nil
			}
			if c.Entries == 0 {
				c.First = e.ID
			}
			c.Entries++
			c.Last, prev = e.ID, e.Hash
		}
		if len(ms) < 500 {
			c.OK = true
			return c, nil
		}
		start = "(" + ms[len(ms)-1].ID
	}
}
