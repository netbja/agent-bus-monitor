package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// PlanKey holds the active plan (v0.22, spec
// docs/superpowers/specs/2026-10-22-plan-view-design.md): the parsed file,
// its path and commit, and the Redis TIME of the load. PlanFactsKey holds
// what master recorded per task (PR, done, note). Neither is in
// projectKinds: a plan alone never makes a project appear in --list.
func PlanKey(project string) string      { return project + ":plan" }
func PlanFactsKey(project string) string { return project + ":plan:facts" }

// PlanRecord is {p}:plan.
type PlanRecord struct {
	Path     string          `json:"path"`
	Commit   string          `json:"commit"`
	LoadedAt int64           `json:"loaded_at"` // Unix ms, Redis TIME
	Tasks    []string        `json:"tasks"`     // the task ids, for the fact scripts
	File     json.RawMessage `json:"file"`
}

// PlanFact is what master recorded about one task. Times are Unix ms from
// Redis TIME; At is the newest of them.
type PlanFact struct {
	PR     int    `json:"pr,omitempty"`
	Linked int64  `json:"linked,omitempty"`
	Done   int64  `json:"done,omitempty"`
	Note   string `json:"note,omitempty"`
	At     int64  `json:"at,omitempty"`
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
	rec, _ := json.Marshal(PlanRecord{Path: path, Commit: commit, Tasks: tasks, File: json.RawMessage(file)})
	return planLoad.Run(ctx, b.r, []string{PlanKey(b.project), PlanFactsKey(b.project)}, string(rec)).Err()
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

var planLoad = redis.NewScript(`
local t = redis.call('TIME')
local rec = cjson.decode(ARGV[1])
rec.loaded_at = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local keep = {}
for _, id in ipairs(rec.tasks) do keep[id] = true end
for _, f in ipairs(redis.call('HKEYS', KEYS[2])) do
  if not keep[f] then redis.call('HDEL', KEYS[2], f) end
end
redis.call('SET', KEYS[1], cjson.encode(rec))
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
	return b.r.Del(ctx, PlanKey(b.project), PlanFactsKey(b.project)).Err()
}

// PlanLink records the PR of a task.
func (b *Bus) PlanLink(ctx context.Context, task string, pr int) error {
	if pr <= 0 {
		return fmt.Errorf("invalid PR number %d", pr)
	}
	return b.planFact(ctx, task, "link", fmt.Sprint(pr))
}

// PlanDone records that the person merged the task (or did their step).
func (b *Bus) PlanDone(ctx context.Context, task string) error {
	return b.planFact(ctx, task, "done", "")
}

// PlanNote records a one-line note on a task.
func (b *Bus) PlanNote(ctx context.Context, task, note string) error {
	if note == "" || len([]rune(note)) > PlanNoteMax || strings.ContainsAny(note, "\r\n") {
		return fmt.Errorf("a note is one line of 1 to %d characters", PlanNoteMax)
	}
	return b.planFact(ctx, task, "note", note)
}

func (b *Bus) planFact(ctx context.Context, task, op, value string) error {
	if !ValidName(task) {
		return fmt.Errorf("invalid task %q", task)
	}
	err := planFactSet.Run(ctx, b.r, []string{PlanKey(b.project), PlanFactsKey(b.project)}, task, op, value).Err()
	if err != nil && strings.Contains(err.Error(), "PLAN_") {
		switch {
		case strings.Contains(err.Error(), "PLAN_NONE"):
			return errors.New("no plan is loaded (agentbus plan load <path>@<commit>)")
		case strings.Contains(err.Error(), "PLAN_TASK"):
			return fmt.Errorf("task %q is not in the loaded plan", task)
		}
	}
	return err
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
if ARGV[2] == 'link' then f.pr = tonumber(ARGV[3]); f.linked = now
elseif ARGV[2] == 'done' then f.done = now
elseif ARGV[2] == 'note' then f.note = ARGV[3] end
f.at = now
redis.call('HSET', KEYS[2], ARGV[1], cjson.encode(f))
return 1`)
