package bus

import (
	"context"
	"encoding/json"
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
	ApprovedAt int64           `json:"approved_at"`    // Unix ms of the deciding approve
	Task       string          `json:"task,omitempty"` // the plan task whose review subject is one of Subjects
}

// MergeQueueKey is {p}:mergequeue: the dispatch service's cache of the
// derivation (a hash), replaced by every pass; runtime, not in projectKinds,
// ignored by HasKeysBesidesDispatch.
func MergeQueueKey(project string) string { return project + ":mergequeue" }

// MergeQueueTTL bounds the cache: a dispatcher gone, it expires, and an
// expired key reads unknown.
const MergeQueueTTL = 15 * time.Minute

// MergeQueueRecord is the cache as read. ItemsAt is the last successful
// pass, the one that read Items; AttemptedAt the last pass, failed or not
// (reviewer-codex: an attempt is not an observation). Reason is "" after a
// success, else what could not be read; both times are Redis TIME, Unix ms.
type MergeQueueRecord struct {
	AttemptedAt int64
	ItemsAt     int64
	Source      string // gh, or git
	Forge       string // github, gitlab, bitbucket or local (forge.Resolve's kind)
	Base        string // the base branch (delivery.base)
	Reason      string
	Items       []MergeItem
	Unapproved  int // open PRs not on an approved head
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
// ARGV[9] base. A failure writes
// the attempt, the source and the reason only.
var mergeQueuePut = redis.NewScript(fenceCheck + `
local t = redis.call('TIME')
local now = tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
redis.call('HSET', KEYS[2], 'attempted_at', now, 'source', ARGV[3], 'reason', ARGV[4], 'forge', ARGV[8], 'base', ARGV[9])
if ARGV[2] == '1' then
  redis.call('HSET', KEYS[2], 'items_at', now, 'items', ARGV[5], 'unapproved', ARGV[6])
end
redis.call('PEXPIRE', KEYS[2], ARGV[7])
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
	ok := "0"
	if rec.Reason == "" {
		ok = "1"
	}
	return notHolder(mergeQueuePut.Run(ctx, b.r, []string{DispatchKey(b.project), MergeQueueKey(b.project)},
		token, ok, rec.Source, rec.Reason, string(js), rec.Unapproved, MergeQueueTTL.Milliseconds(), rec.Forge, rec.Base).Err())
}
