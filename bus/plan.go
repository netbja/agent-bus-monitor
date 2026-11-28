package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/redis/go-redis/v9"
)

// PlanKey holds the active plan (v0.22, spec
// docs/superpowers/specs/2026-10-22-plan-view-design.md): the parsed file,
// its path and commit, and the Redis TIME of the load. PlanFactsKey holds
// what master recorded per task (PR, done, note). Neither is in
// projectKinds: a plan alone never makes a project appear in --list.
func PlanKey(project string) string      { return project + ":plan" }
func PlanFactsKey(project string) string { return project + ":plan:facts" }

// PlanIgnoreKey is the set of PRs master said belong to no task of the
// loaded plan (plan-drift: notes, a release, a hotfix); a new load or a
// clear drops it with the plan.
func PlanIgnoreKey(project string) string { return project + ":plan:ignore" }

// PlanRecord is {p}:plan.
type PlanRecord struct {
	Path     string   `json:"path"`
	Commit   string   `json:"commit"`
	LoadedAt int64    `json:"loaded_at"` // Unix ms, Redis TIME
	Tasks    []string `json:"tasks"`     // the task ids, for the fact scripts
	File     string   `json:"file"`      // the file's bytes, opaque: cjson would rewrite [] as {}
	// Approved is the reply entry of the person's yes when the plan was loaded
	// as an approved draft (onboarding §8.5); absent on a plain load.
	Approved string `json:"approved,omitempty"`
}

// PlanFact is what master recorded about one task. Times are Unix ms from
// Redis TIME; At is the newest of them.
type PlanFact struct {
	PR     int    `json:"pr,omitempty"`
	Ref    string `json:"ref,omitempty"` // <branch>@<sha>: a delivery with no PR (forge-delivery's link and local modes)
	Linked int64  `json:"linked,omitempty"`
	Done   int64  `json:"done,omitempty"`
	Note   string `json:"note,omitempty"`
	At     int64  `json:"at,omitempty"`
	// Priority (views-redesign §4, step 4b): the person's "high", "normal" or
	// "low", over the plan file's; absent = the file's. It is no progress, so At
	// keeps its time.
	Priority   string `json:"priority,omitempty"`
	PriorityBy string `json:"priority_by,omitempty"`
	PriorityAt int64  `json:"priority_at,omitempty"` // Redis TIME, Unix ms
	// Head is the merged head plan done recorded for a task with a check
	// (onboarding §9.3); its done counts only at the head its approving
	// verdict checked (§9.2). Absent on every other done.
	Head string `json:"head,omitempty"`
	// Approved and ApprovedQID: a person's task with a check (onboarding
	// §9.4), the reply entry of their typed passes and the question id it
	// answered; plan done writes them, derive reads them.
	Approved    string `json:"approved,omitempty"`
	ApprovedQID string `json:"approved_qid,omitempty"`
}

// PlanNoteMax bounds a task's note (one line).
const PlanNoteMax = 200

// ErrNoProject: the broker holds no key of this project, so a plan would
// create one nobody lists (most likely a mistyped project).
var ErrNoProject = errors.New("no such project on this broker")

// LoadPlan replaces the active plan. Facts of tasks still in the plan are
// kept, the others dropped, in the same script.
func (b *Bus) LoadPlan(ctx context.Context, path, commit string, file []byte, tasks []string) error {
	known, err := b.hasProjectKind(ctx)
	if err != nil {
		return err
	}
	if !known {
		return fmt.Errorf("%w: %s", ErrNoProject, b.project)
	}
	rec, _ := json.Marshal(PlanRecord{Path: path, Commit: commit, Tasks: tasks, File: string(file)})
	return draftErr(planLoad.Run(ctx, b.r, []string{PlanKey(b.project), PlanFactsKey(b.project), PlanIgnoreKey(b.project), PlanDraftKey(b.project)}, string(rec)).Err())
}

func (b *Bus) hasProjectKind(ctx context.Context) (bool, error) {
	keys, err := ProjectKeys(ctx, b.r, b.project)
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if _, ok := projectFromKey(k); ok {
			return true, nil
		}
	}
	return false, nil
}

// planLoad: KEYS plan, facts, ignore, draft. A recorded draft refuses: it
// loads only through LoadPlanDraft (onboarding §8.5).
var planLoad = redis.NewScript(`
if redis.call('EXISTS', KEYS[4]) == 1 then return redis.error_reply('PLAN_DRAFT_RECORDED') end
local t = redis.call('TIME')
local rec = cjson.decode(ARGV[1])
rec.loaded_at = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local keep = {}
for _, id in ipairs(rec.tasks) do keep[id] = true end
for _, f in ipairs(redis.call('HKEYS', KEYS[2])) do
  if not keep[f] then redis.call('HDEL', KEYS[2], f) end
end
redis.call('SET', KEYS[1], cjson.encode(rec))
redis.call('DEL', KEYS[3])
return 1`)

// Plan reads the active plan; ok is false when none is loaded.
func (b *Bus) Plan(ctx context.Context) (PlanRecord, bool, error) {
	raw, err := b.r.Get(ctx, PlanKey(b.project)).Result()
	if errors.Is(err, redis.Nil) {
		return PlanRecord{}, false, nil
	}
	if err != nil {
		return PlanRecord{}, false, err
	}
	var rec PlanRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return PlanRecord{}, false, fmt.Errorf("plan record: %w", err)
	}
	return rec, true, nil
}

// PlanFacts reads every task's recorded facts.
func (b *Bus) PlanFacts(ctx context.Context) (map[string]PlanFact, error) {
	all, err := b.r.HGetAll(ctx, PlanFactsKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]PlanFact, len(all))
	for id, v := range all {
		var f PlanFact
		if json.Unmarshal([]byte(v), &f) == nil {
			out[id] = f
		}
	}
	return out, nil
}

// ClearPlan deletes the plan and its facts.
func (b *Bus) ClearPlan(ctx context.Context) error {
	return b.r.Del(ctx, PlanKey(b.project), PlanFactsKey(b.project), PlanIgnoreKey(b.project)).Err()
}

// planIgnore adds a PR to the ignore set, only while a plan is loaded.
var planIgnore = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return redis.error_reply('PLAN_NONE') end
return redis.call('SADD', KEYS[2], ARGV[1])`)

// PlanIgnore records that PR belongs to no task of the loaded plan: its
// merge no longer warns plan-drift.
func (b *Bus) PlanIgnore(ctx context.Context, pr int) error {
	if pr <= 0 {
		return fmt.Errorf("invalid PR number %d", pr)
	}
	err := planIgnore.Run(ctx, b.r, []string{PlanKey(b.project), PlanIgnoreKey(b.project)}, pr).Err()
	if err != nil && strings.Contains(err.Error(), "PLAN_NONE") {
		return errors.New("no plan is loaded (agentbus plan load <path>@<commit>)")
	}
	return err
}

// PlanIgnored is the set of ignored PRs.
func (b *Bus) PlanIgnored(ctx context.Context) (map[int]bool, error) {
	all, err := b.r.SMembers(ctx, PlanIgnoreKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[int]bool, len(all))
	for _, v := range all {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			out[n] = true
		}
	}
	return out, nil
}

// PlanLink records the PR of a task.
func (b *Bus) PlanLink(ctx context.Context, task string, pr int) error {
	if pr <= 0 {
		return fmt.Errorf("invalid PR number %d", pr)
	}
	return b.planFact(ctx, task, "link", fmt.Sprint(pr))
}

// PlanLinkRef records a delivered ref of a task, <branch>@<full sha>, for
// a delivery that opened no PR (forge-delivery: link and local modes); it
// replaces a PR link, as a later PR link replaces it.
func (b *Bus) PlanLinkRef(ctx context.Context, task, ref string) error {
	i := strings.LastIndex(ref, "@") // a branch may hold "@": the sha follows the last one
	if i < 0 {
		return fmt.Errorf("a ref is <branch>@<full commit sha>, not %q", ref)
	}
	branch, sha := ref[:i], ref[i+1:]
	if len(branch) > 255 || !config.ValidBranchName(branch) || !ValidHead(sha) {
		return fmt.Errorf("a ref is <branch>@<full commit sha>, not %q", ref)
	}
	return b.planFact(ctx, task, "ref", ref)
}

// PlanDone records that the person merged the task (or did their step).
func (b *Bus) PlanDone(ctx context.Context, task string) error {
	return b.planFact(ctx, task, "done", "")
}

// PlanDoneAt records the task done at head, the merged head plan done
// checked (onboarding §9.3): done and head in the same script, so a done
// never stands without the head it was checked at.
func (b *Bus) PlanDoneAt(ctx context.Context, task, head string) error {
	if !ValidHead(head) {
		return fmt.Errorf("invalid head %q", head)
	}
	return b.planFact(ctx, task, "done", head)
}

// PlanDoneApproved records a person's task with a check done (onboarding
// §9.4): done, the reply entry of their typed passes and the question id it
// answered, in the same script.
func (b *Bus) PlanDoneApproved(ctx context.Context, task, entry, qid string) error {
	if entry == "" || !strings.HasPrefix(qid, "check-") {
		return fmt.Errorf("an approval is a reply entry and a check- question id")
	}
	_, err := b.planFactRun(ctx, task, "pdone", entry, qid)
	return err
}

// PlanNote records a one-line note on a task.
func (b *Bus) PlanNote(ctx context.Context, task, note string) error {
	if note == "" || len([]rune(note)) > PlanNoteMax || strings.ContainsAny(note, "\r\n") {
		return fmt.Errorf("a note is one line of 1 to %d characters", PlanNoteMax)
	}
	return b.planFact(ctx, task, "note", note)
}

// SetPlanPriority sets a plan task's level, as the person by: priority,
// priority_by and priority_at (Redis TIME). normal is stored too, so it wins
// over the plan file's level. The same level as stored writes nothing
// (changed false). The
// task's other facts and their time are kept: a priority is no progress. A
// request sent for the task later starts at this level (RequestBy).
func (b *Bus) SetPlanPriority(ctx context.Context, task, level, by string) (changed bool, err error) {
	if !ValidPriority(level) {
		return false, fmt.Errorf("priority must be high, normal or low")
	}
	if !ValidName(by) {
		return false, fmt.Errorf("invalid person %q", by)
	}
	n, err := b.planFactRun(ctx, task, "prio", level, by)
	return n == 1, err
}

func (b *Bus) planFact(ctx context.Context, task, op, value string) error {
	_, err := b.planFactRun(ctx, task, op, value, "")
	return err
}

func (b *Bus) planFactRun(ctx context.Context, task, op, value, by string) (int64, error) {
	if !ValidName(task) {
		return 0, fmt.Errorf("invalid task %q", task)
	}
	n, err := planFactSet.Run(ctx, b.r, []string{PlanKey(b.project), PlanFactsKey(b.project)}, task, op, value, by).Int64()
	if err != nil && strings.Contains(err.Error(), "PLAN_") {
		switch {
		case strings.Contains(err.Error(), "PLAN_NONE"):
			return 0, errors.New("no plan is loaded (agentbus plan load <path>@<commit>)")
		case strings.Contains(err.Error(), "PLAN_TASK"):
			return 0, fmt.Errorf("task %q is not in the loaded plan", task)
		}
	}
	return n, err
}

var planFactSet = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return redis.error_reply('PLAN_NONE') end
local rec = cjson.decode(raw)
local known = false
for _, id in ipairs(rec.tasks) do if id == ARGV[1] then known = true end end
if not known then return redis.error_reply('PLAN_TASK') end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local f = {}
local old = redis.call('HGET', KEYS[2], ARGV[1])
if old then f = cjson.decode(old) end
if ARGV[2] == 'prio' then
  if f.priority == ARGV[3] then return 0 end
  f.priority = ARGV[3]; f.priority_by = ARGV[4]; f.priority_at = now
  redis.call('HSET', KEYS[2], ARGV[1], cjson.encode(f))
  return 1
end
if ARGV[2] == 'link' then f.pr = tonumber(ARGV[3]); f.ref = nil; f.linked = now
elseif ARGV[2] == 'ref' then f.ref = ARGV[3]; f.pr = nil; f.linked = now
elseif ARGV[2] == 'done' then
  f.done = now
  if ARGV[3] ~= '' then f.head = ARGV[3] else f.head = nil end
  f.approved = nil; f.approved_qid = nil
elseif ARGV[2] == 'pdone' then
  f.done = now; f.approved = ARGV[3]; f.approved_qid = ARGV[4]; f.head = nil
elseif ARGV[2] == 'note' then f.note = ARGV[3] end
f.at = now
redis.call('HSET', KEYS[2], ARGV[1], cjson.encode(f))
return 1`)
