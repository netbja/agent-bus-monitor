package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// The merge queue (spec docs/superpowers/specs/2026-11-05-merge-queue-design.md):
// what waits for the person's merge, derived from the verdict ledger and the
// forge (or git), never remembered. The derivation is package mergequeue; the
// dispatch service caches it in {p}:mergequeue (MergeQueueRecord).

// MergeApproval is one independent approve of an item's head.
type MergeApproval struct {
	Reviewer string `json:"reviewer"`
	At       int64  `json:"at"`      // Unix ms (the verdict's)
	Message  string `json:"message"` // what the reviewer wrote: the checks it ran
}

// MergeItem is one change waiting for the person's merge: an open PR (or a
// branch, under the git fallback) whose head is a subject's deciding approved
// head.
type MergeItem struct {
	Number     int             `json:"number,omitempty"` // the PR or MR; 0 under the git fallback
	Title      string          `json:"title,omitempty"`
	URL        string          `json:"url,omitempty"`
	Branch     string          `json:"branch"`
	Head       string          `json:"head"`
	Subjects   []string        `json:"subjects"`
	Approvals  []MergeApproval `json:"approvals"`
	ApprovedAt int64           `json:"approved_at"` // Unix ms of the deciding approve
}

// MergeConflict is an approved PR the forge reports conflicting with its own
// base (the PR's, which may differ from delivery.base: reviewer-codex): a
// rebase onto Base for master, never a merge for the person.
type MergeConflict struct {
	Number int    `json:"number"`
	Base   string `json:"base"`
}

// UnmarshalJSON also reads the field's first form, a bare PR number: a
// conflict with no base named (reviewer-codex: a cache it wrote still reads).
func (c *MergeConflict) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*c = MergeConflict{Number: n}
		return nil
	}
	type plain MergeConflict
	return json.Unmarshal(b, (*plain)(c))
}

// MergeUndelivered is an approved head master has not delivered
// (master-idle §3.1): no branch tip on origin, and the base holds none of
// its change (mergequeue.Delivery). Subject is the first of its subjects;
// Branch a local branch whose tip it is, or "".
type MergeUndelivered struct {
	Subject    string `json:"subject"`
	Head       string `json:"head"`
	Branch     string `json:"branch"`
	ApprovedAt int64  `json:"approved_at"` // Unix ms of the deciding approve
}

// MergeDrop is a head master withdrew: it will never be delivered
// (master-idle §3.1, merge-queue drop). At is Redis TIME, Unix ms.
type MergeDrop struct {
	Reason string `json:"reason"`
	By     string `json:"by"`
	At     int64  `json:"at"`
}

// MergeQueueDroppedKey is {p}:mergequeue:dropped, one field per dropped head
// (the full SHA): an agent's write (drop, undrop), pruned by the merge-queue
// pass past MergeDropKeep.
func MergeQueueDroppedKey(project string) string { return project + ":mergequeue:dropped" }

// MergeDropKeep: a drop older than this is pruned (the 72 h an approve is
// counted, health.ReviewQueueWindow): past it the head is out anyway.
const MergeDropKeep = 72 * time.Hour

// mergeDrop: KEYS[1] the dropped hash; ARGV[1] head, ARGV[2] reason, ARGV[3] by.
var mergeDrop = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode({reason = ARGV[2], by = ARGV[3], at = now}))
return 1`)

// DropHead withdraws head (a full SHA) with its reason, as by; the time is
// Redis's. The caller checks the head and who may drop.
func (b *Bus) DropHead(ctx context.Context, head, reason, by string) error {
	if !ValidHead(head) {
		return fmt.Errorf("invalid head %q", head)
	}
	return mergeDrop.Run(ctx, b.r, []string{MergeQueueDroppedKey(b.project)}, head, reason, by).Err()
}

// UndropHead puts a dropped head back; false when it was not dropped.
func (b *Bus) UndropHead(ctx context.Context, head string) (bool, error) {
	n, err := b.r.HDel(ctx, MergeQueueDroppedKey(b.project), head).Result()
	return n > 0, err
}

// DroppedHeads are the dropped heads; a corrupt field is skipped.
func (b *Bus) DroppedHeads(ctx context.Context) (map[string]MergeDrop, error) {
	h, err := b.r.HGetAll(ctx, MergeQueueDroppedKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := map[string]MergeDrop{}
	for head, js := range h {
		var d MergeDrop
		if json.Unmarshal([]byte(js), &d) == nil && ValidHead(head) {
			out[head] = d
		}
	}
	return out, nil
}

// MergeQueueKey is {p}:mergequeue: the dispatch service's cache of the
// derivation (a hash), replaced by every pass; runtime, not in projectKinds,
// ignored by HasKeysBesidesDispatch.
func MergeQueueKey(project string) string { return project + ":mergequeue" }

// MergeQueueDueKey is {p}:mergequeue:due, a delivery's mark: the broker's time
// in milliseconds when agentbus deliver last delivered (merge-popup-setting,
// the person's choice A). The dispatcher only reads it: a mark it has not
// passed for runs a pass at once, so a PR is a row in For you within seconds.
func MergeQueueDueKey(project string) string { return project + ":mergequeue:due" }

// mergeQueueDueTTL: a mark older than this has long been passed for.
const mergeQueueDueTTL = time.Hour

// MarkMergeQueueDue marks the merge queue due, with the broker's time.
func (b *Bus) MarkMergeQueueDue(ctx context.Context) error {
	now, err := b.r.Time(ctx).Result()
	if err != nil {
		return err
	}
	return b.r.Set(ctx, MergeQueueDueKey(b.project), now.UnixMilli(), mergeQueueDueTTL).Err()
}

// MergeQueueDue is the last delivery's mark, 0 when there is none.
func (b *Bus) MergeQueueDue(ctx context.Context) (int64, error) {
	v, err := b.r.Get(ctx, MergeQueueDueKey(b.project)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return v, err
}

// MergeQueueTTL bounds the cache: a dispatcher gone, it expires, and an
// expired key reads unknown.
const MergeQueueTTL = 15 * time.Minute

// MergeQueueRecord is the cache as read. ItemsAt is the last successful
// pass, the one that read Items; AttemptedAt the last pass, failed or not
// (reviewer-codex: an attempt is not an observation). Reason is "" after a
// success, else what could not be read; both times are Redis TIME, Unix ms.
type MergeQueueRecord struct {
	AttemptedAt int64       `json:"attempted_at"`
	ItemsAt     int64       `json:"items_at"`
	Source      string      `json:"source"` // gh, or git
	Forge       string      `json:"forge"`  // github, gitlab, bitbucket, none or local (forge.Resolve's kind)
	Base        string      `json:"base"`   // the base branch (delivery.base)
	Reason      string      `json:"reason,omitempty"`
	Items       []MergeItem `json:"items"`
	Unapproved  int         `json:"unapproved"` // open PRs not on an approved head
	// Conflicting are the approved PRs that conflict with their base: no
	// merge for the person, a rebase for master. Not in Items.
	Conflicting []MergeConflict `json:"conflicting,omitempty"`
	// Undelivered are the approved heads master has not delivered (forge
	// and none modes; empty in local mode): master-idle's first source.
	Undelivered []MergeUndelivered `json:"undelivered,omitempty"`
}

// MergeQueue reads the cache; a missing key is the zero record (unknown).
func (b *Bus) MergeQueue(ctx context.Context) (MergeQueueRecord, error) {
	h, err := b.r.HGetAll(ctx, MergeQueueKey(b.project)).Result()
	if err != nil {
		return MergeQueueRecord{}, err
	}
	rec := MergeQueueRecord{Source: h["source"], Forge: h["forge"], Base: h["base"], Reason: h["reason"]}
	rec.AttemptedAt, _ = strconv.ParseInt(h["attempted_at"], 10, 64)
	rec.ItemsAt, _ = strconv.ParseInt(h["items_at"], 10, 64)
	rec.Unapproved, _ = strconv.Atoi(h["unapproved"])
	if s := h["conflicting"]; s != "" {
		if err := json.Unmarshal([]byte(s), &rec.Conflicting); err != nil {
			return MergeQueueRecord{}, fmt.Errorf("merge queue: a corrupt conflicting field: %w", err)
		}
	}
	if s := h["undelivered"]; s != "" {
		if err := json.Unmarshal([]byte(s), &rec.Undelivered); err != nil {
			return MergeQueueRecord{}, fmt.Errorf("merge queue: a corrupt undelivered field: %w", err)
		}
	}
	if s := h["items"]; s != "" {
		if err := json.Unmarshal([]byte(s), &rec.Items); err != nil {
			return MergeQueueRecord{}, fmt.Errorf("merge queue: a corrupt items field: %w", err)
		}
	}
	return rec, nil
}

// mergeQueuePut: KEYS[1] the dispatch lease, KEYS[2] the cache; ARGV[1]
// token, ARGV[2] "1" a success, ARGV[3] source, ARGV[4] reason, ARGV[5] items
// (JSON, kept opaque), ARGV[6] unapproved, ARGV[7] ttl ms, ARGV[8] forge,
// ARGV[9] base, ARGV[10] conflicting (JSON), ARGV[11] undelivered (JSON).
// A failure writes the attempt, the source and the reason only. Either way
// KEYS[3], the dropped heads, loses each drop older than ARGV[12] ms (a
// corrupt one too).
var mergeQueuePut = redis.NewScript(fenceCheck + `
local t = redis.call('TIME')
local now = tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
redis.call('HSET', KEYS[2], 'attempted_at', now, 'source', ARGV[3], 'reason', ARGV[4], 'forge', ARGV[8], 'base', ARGV[9])
if ARGV[2] == '1' then
  redis.call('HSET', KEYS[2], 'items_at', now, 'items', ARGV[5], 'unapproved', ARGV[6], 'conflicting', ARGV[10], 'undelivered', ARGV[11])
end
redis.call('PEXPIRE', KEYS[2], ARGV[7])
local drops = redis.call('HGETALL', KEYS[3])
for i = 1, #drops, 2 do
  local ok, d = pcall(cjson.decode, drops[i + 1])
  if not ok or type(d) ~= 'table' or type(d.at) ~= 'number' or d.at < tonumber(now) - tonumber(ARGV[12]) then
    redis.call('HDEL', KEYS[3], drops[i])
  end
end
return 1`)

// FencedPutMergeQueue stores one pass: a success (rec.Reason == "") replaces
// the items and both times; a failure moves the attempt and the reason only,
// keeping the last items and their time. Only the lease holder writes.
func (b *Bus) FencedPutMergeQueue(ctx context.Context, token string, rec MergeQueueRecord) error {
	items := rec.Items
	if items == nil {
		items = []MergeItem{}
	}
	js, err := json.Marshal(items)
	if err != nil {
		return err
	}
	conflicting := rec.Conflicting
	if conflicting == nil {
		conflicting = []MergeConflict{}
	}
	cjs, err := json.Marshal(conflicting)
	if err != nil {
		return err
	}
	undelivered := rec.Undelivered
	if undelivered == nil {
		undelivered = []MergeUndelivered{}
	}
	ujs, err := json.Marshal(undelivered)
	if err != nil {
		return err
	}
	ok := "0"
	if rec.Reason == "" {
		ok = "1"
	}
	return notHolder(mergeQueuePut.Run(ctx, b.r, []string{DispatchKey(b.project), MergeQueueKey(b.project), MergeQueueDroppedKey(b.project)},
		token, ok, rec.Source, rec.Reason, string(js), rec.Unapproved, MergeQueueTTL.Milliseconds(), rec.Forge, rec.Base, string(cjs), string(ujs), MergeDropKeep.Milliseconds()).Err())
}
