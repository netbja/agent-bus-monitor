package bus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

// PlanDraftKey holds the one draft plan awaiting the person's yes
// (onboarding §8.1, spec
// docs/superpowers/specs/2026-11-27-project-onboarding-design.md). The
// architect writes it, so it is not master's only: the key decides
// nothing, TypedApproval does (§8.4).
func PlanDraftKey(project string) string { return project + ":plan:draft" }

// PlanDraft is {p}:plan:draft: the file and the build sheet at a commit,
// what it replaces and stops (the qid's inputs), the adoption question and
// its ask, the recorder, and the Redis TIME of the write. File and Build
// are opaque bytes, as PlanRecord.File. BuildPath is where the build sheet
// was read (plan draft's --build), so plan load rereads the same file.
type PlanDraft struct {
	Plan      string `json:"plan"`
	Path      string `json:"path"`
	Commit    string `json:"commit"`
	File      string `json:"file"`
	Build     string `json:"build"`
	BuildPath string `json:"build_path"`
	Replaces  string `json:"replaces"` // the active plan's <path>@<commit> at drafting, "" none
	Stops     string `json:"stops"`    // the in-hand tasks the draft drops, sorted, comma-joined
	QID       string `json:"qid"`
	Ask       string `json:"ask"`
	By        string `json:"by"`
	At        int64  `json:"at"`

	raw string // the record as read, for a compare-and-set
}

// StopIDs are the record's stops as a list.
func (d PlanDraft) StopIDs() []string {
	if d.Stops == "" {
		return nil
	}
	return strings.Split(d.Stops, ",")
}

// PlanDraftQID is a draft's question id (§8.1 step 4): "plan-" and 16 hex
// digits of SHA-256(path, plan, build sheet, replaces, the stopped ids
// sorted and comma-joined), NUL between each. The person's yes binds them
// all.
func PlanDraftQID(path string, file, build []byte, replaces string, stops []string) string {
	h := sha256.New()
	for i, part := range [][]byte{[]byte(path), file, build, []byte(replaces), []byte(joinSorted(stops))} {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write(part)
	}
	return "plan-" + hex.EncodeToString(h.Sum(nil))[:16]
}

func joinSorted(ids []string) string {
	s := append([]string(nil), ids...)
	sort.Strings(s)
	return strings.Join(s, ",")
}

// The draft's errors.
var (
	// ErrDraftChanged: the record is not the one read (a concurrent draft,
	// a re-draft or a drop since).
	ErrDraftChanged = errors.New("the draft changed after the approval check: load again")
	// ErrNoDraft: no draft is recorded.
	ErrNoDraft = errors.New("no draft plan is recorded")
	// ErrDraftRecorded: a plain load while a draft is recorded.
	ErrDraftRecorded = errors.New("a draft plan is recorded: plan load loads only the approved draft (plan draft show)")
	// ErrActiveChanged: the active plan is not the one the draft replaces.
	ErrActiveChanged = errors.New("the active plan changed since the draft: the architect re-drafts")
	// ErrNotApproved: the person's yes does not hold in the script's re-check.
	ErrNotApproved = errors.New("the person's yes to the draft no longer holds")
)

func draftErr(err error) error {
	if err == nil {
		return nil
	}
	for code, e := range map[string]error{"PLAN_DRAFT_CHANGED": ErrDraftChanged, "PLAN_DRAFT_RECORDED": ErrDraftRecorded,
		"PLAN_ACTIVE_CHANGED": ErrActiveChanged, "PLAN_NOT_APPROVED": ErrNotApproved} {
		if strings.Contains(err.Error(), code) {
			return fmt.Errorf("%w (%s)", e, code)
		}
	}
	return err
}

// PlanDraft reads the draft; ok is false when none is recorded.
func (b *Bus) PlanDraft(ctx context.Context) (PlanDraft, bool, error) {
	raw, err := b.r.Get(ctx, PlanDraftKey(b.project)).Result()
	if errors.Is(err, redis.Nil) {
		return PlanDraft{}, false, nil
	}
	if err != nil {
		return PlanDraft{}, false, err
	}
	var d PlanDraft
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return PlanDraft{}, false, fmt.Errorf("plan draft record: %w", err)
	}
	d.raw = raw
	return d, true, nil
}

// planDraftSet: KEYS draft. ARGV the record read ("" none), the new record
// (no at; "" deletes). Refuses PLAN_DRAFT_CHANGED when the record is not
// the one read.
var planDraftSet = redis.NewScript(`
local cur = redis.call('GET', KEYS[1]) or ''
if cur ~= ARGV[1] then return redis.error_reply('PLAN_DRAFT_CHANGED') end
if ARGV[2] == '' then
  redis.call('DEL', KEYS[1])
  return 1
end
local t = redis.call('TIME')
local rec = cjson.decode(ARGV[2])
rec.at = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('SET', KEYS[1], cjson.encode(rec))
return 1`)

// SetPlanDraft records d over prev, the record its caller read (the zero
// PlanDraft: none). Any other record refuses with ErrDraftChanged (§8.1
// step 7).
func (b *Bus) SetPlanDraft(ctx context.Context, d, prev PlanDraft) error {
	d.At = 0
	rec, _ := json.Marshal(d)
	return draftErr(planDraftSet.Run(ctx, b.r, []string{PlanDraftKey(b.project)}, prev.raw, string(rec)).Err())
}

// DropPlanDraft deletes prev, the record its caller read; another refuses
// with ErrDraftChanged.
func (b *Bus) DropPlanDraft(ctx context.Context, prev PlanDraft) error {
	if prev.raw == "" {
		return ErrNoDraft
	}
	return draftErr(planDraftSet.Run(ctx, b.r, []string{PlanDraftKey(b.project)}, prev.raw, "").Err())
}

// PlanDraftApproved is §8.4's adoption check on the recorded draft: its
// qid recomputed from the bytes it stores is its qid, and TypedApproval
// passes for its ask, qid and "yes". It returns the approving entry.
func (b *Bus) PlanDraftApproved(ctx context.Context, d PlanDraft) (string, error) {
	if q := PlanDraftQID(d.Path, []byte(d.File), []byte(d.Build), d.Replaces, d.StopIDs()); q != d.QID {
		return "", fmt.Errorf("the draft's record does not hash to its qid (%s ≠ %s)", q, d.QID)
	}
	return b.TypedApproval(ctx, d.Ask, d.QID, "yes")
}

// afterDraftCheck runs between LoadPlanDraft's two parts (a test's race).
var afterDraftCheck func()

// LoadPlanDraft loads the recorded draft (§8.5): file and build are the
// bytes read from Git at commit. Part 1, here, recomputes the qid from them
// and the active plan, and checks the person's yes (TypedApproval, with the
// archive when one is set). Part 2, planLoadDraft, re-checks what the
// broker holds and only then sets the plan, keeping the facts of tasks
// still in it and deleting the ignore set and the draft. It returns the
// approving entry, kept as PlanRecord.Approved.
func (b *Bus) LoadPlanDraft(ctx context.Context, path, commit string, file, build []byte, tasks []string) (string, error) {
	d, ok, err := b.PlanDraft(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNoDraft
	}
	replaces := ""
	if rec, ok, err := b.Plan(ctx); err != nil {
		return "", err
	} else if ok {
		replaces = rec.Path + "@" + rec.Commit
	}
	if q := PlanDraftQID(path, file, build, replaces, d.StopIDs()); q != d.QID {
		return "", fmt.Errorf("this file is not the draft the person approved (qid %s ≠ %s)", q, d.QID)
	}
	entry, err := b.TypedApproval(ctx, d.Ask, d.QID, "yes")
	if err != nil {
		return "", fmt.Errorf("a draft awaits the person's yes: %w", err)
	}
	if afterDraftCheck != nil {
		afterDraftCheck()
	}
	return entry, b.runPlanLoadDraft(ctx, draftLoadArgs{qid: d.QID, ask: d.Ask, path: path, commit: commit,
		file: string(file), build: string(build), entry: entry, tasks: tasks})
}

type draftLoadArgs struct {
	qid, ask, path, commit, file, build, entry string
	tasks                                      []string
}

// planLoadDraft: KEYS plan, facts, ignore, draft, asks, cmd. ARGV qid, ask,
// path, file, build, entry, the new plan record (no loaded_at).
var planLoadDraft = redis.NewScript(`
local raw = redis.call('GET', KEYS[4])
if not raw then return redis.error_reply('PLAN_DRAFT_CHANGED') end
local d = cjson.decode(raw)
if d.qid ~= ARGV[1] or d.ask ~= ARGV[2] or d.path ~= ARGV[3] or d.file ~= ARGV[4] or d.build ~= ARGV[5] then
  return redis.error_reply('PLAN_DRAFT_CHANGED')
end
local active = ''
local cur = redis.call('GET', KEYS[1])
if cur then
  local c = cjson.decode(cur)
  active = c.path .. '@' .. c.commit
end
if active ~= (d.replaces or '') then return redis.error_reply('PLAN_ACTIVE_CHANGED') end
if redis.call('HEXISTS', KEYS[5], ARGV[2]) == 1 then
  local a = redis.call('HGET', KEYS[5], ARGV[2] .. '|a|' .. ARGV[1])
  if not a then return redis.error_reply('PLAN_NOT_APPROVED') end
  a = cjson.decode(a)
  if a.choice ~= 'yes' or a.withdrawn == true or a.by == d.by then return redis.error_reply('PLAN_NOT_APPROVED') end
end
local es = redis.call('XRANGE', KEYS[6], ARGV[6], ARGV[6])
if #es > 0 then
  local f = {}
  local kv = es[1][2]
  for i = 1, #kv, 2 do f[kv[i]] = kv[i + 1] end
  if f.type ~= 'reply' or f.ref ~= ARGV[2] or f.qid ~= ARGV[1] or f.typed ~= 'yes' or f.said ~= 'yes' then
    return redis.error_reply('PLAN_NOT_APPROVED')
  end
end
local t = redis.call('TIME')
local rec = cjson.decode(ARGV[7])
rec.loaded_at = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local keep = {}
for _, id in ipairs(rec.tasks) do keep[id] = true end
for _, f in ipairs(redis.call('HKEYS', KEYS[2])) do
  if not keep[f] then redis.call('HDEL', KEYS[2], f) end
end
redis.call('SET', KEYS[1], cjson.encode(rec))
redis.call('DEL', KEYS[3], KEYS[4])
return 1`)

func (b *Bus) runPlanLoadDraft(ctx context.Context, a draftLoadArgs) error {
	rec, _ := json.Marshal(PlanRecord{Path: a.path, Commit: a.commit, Tasks: a.tasks, File: a.file, Approved: a.entry})
	p := b.project
	return draftErr(planLoadDraft.Run(ctx, b.r, []string{PlanKey(p), PlanFactsKey(p), PlanIgnoreKey(p), PlanDraftKey(p), AsksKey(p), StreamKey(p, "cmd")},
		a.qid, a.ask, a.path, a.file, a.build, a.entry, string(rec)).Err())
}
