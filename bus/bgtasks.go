package bus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// An agent's background tasks as its client's hooks report them
// (background-work-visible §4): {p}:bgtasks:<agent>, apart from the declared
// record {p}:background:<agent> (the agent's words), so neither replaces nor
// clears the other. Written by the agent's own Stop and SubagentStop hooks,
// dropped at SessionStart; it lives an hour after its last write.
//
// Never stored: a task's command or description, a cron's prompt, a task's
// raw id (only its digest, which keeps first_seen across turn ends).

// BgTasksKey is an agent's observed background tasks.
func BgTasksKey(project, agent string) string { return project + ":bgtasks:" + agent }

// BgTasksTTL is the record's life after its last write.
const BgTasksTTL = time.Hour

// BgTasks is the record.
type BgTasks struct {
	Source string   `json:"source"` // "hooks"
	Event  string   `json:"event"`  // Stop or SubagentStop
	At     int64    `json:"at"`     // that event's write, Redis TIME, Unix ms
	PID    int      `json:"pid"`    // the agent's proved process (the parent's, for a SubagentStop)
	Tasks  []BgTask `json:"tasks"`
	Crons  []BgCron `json:"crons"`
}

// BgTask is one task in flight, its labels sanitised by the hook.
type BgTask struct {
	IDHash    string `json:"id_hash"`
	Type      string `json:"type"`
	AgentType string `json:"agent_type,omitempty"` // a subagent's, [A-Za-z0-9_-]{1,32}
	Status    string `json:"status"`
	FirstSeen int64  `json:"first_seen"` // Redis TIME, Unix ms: when agentbus first saw it
}

// BgCron is one scheduled wakeup: never counted as running work.
type BgCron struct {
	IDHash    string `json:"id_hash"`
	Schedule  string `json:"schedule"`
	Recurring bool   `json:"recurring"`
}

// BgIDHash is a task's or a cron's id as stored: a digest, never the id.
func BgIDHash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8])
}

var (
	bgHashRE  = regexp.MustCompile(`^[0-9a-f]{16}$`)
	bgLabelRE = regexp.MustCompile(`^[a-z][a-z ]{0,31}$`)       // Claude's types and statuses: "shell", "cloud session", "running"
	bgAgentRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)     // a sub-agent's name
	bgCronRE  = regexp.MustCompile(`^(\S+ ){4}\S+$`)            // the five-field shape
	bgCronOK  = regexp.MustCompile(`^[0-9*/,\- A-Za-z]{1,64}$`) // and only cron's characters
)

// BgTaskTypes are Claude Code's task types (its hook reference, 2026-11-18);
// any other is stored as "other" (Claude falls back to its raw discriminant).
var BgTaskTypes = map[string]bool{"shell": true, "subagent": true, "monitor": true, "workflow": true,
	"teammate": true, "cloud session": true, "MCP task": true, "other": true}

// BgTaskStatuses are the statuses the recorded payloads showed
// (cmd/agentbus/testdata/hooks, 2026-11-20); any other is stored as "other".
var BgTaskStatuses = map[string]bool{"running": true, "other": true}

// BgLabel is a type or a status as stored: known, else "other".
func BgLabel(known map[string]bool, v string) string {
	if known[v] {
		return v
	}
	return "other"
}

// BgAgentType is a sub-agent's name as stored: its name when it is one, else
// "" (the row then reads "subagent" only: a name the person wrote can be
// anything).
func BgAgentType(v string) string {
	if bgAgentRE.MatchString(v) {
		return v
	}
	return ""
}

// BgSchedule reports whether s has a cron expression's shape and characters.
func BgSchedule(s string) bool {
	s = strings.TrimSpace(s)
	return bgCronRE.MatchString(s) && bgCronOK.MatchString(s)
}

// validBgRecord refuses what the hook must have cleaned: the writer is the
// last line before Redis.
func validBgRecord(tasks []BgTask, crons []BgCron) error {
	for _, t := range tasks {
		if !bgHashRE.MatchString(t.IDHash) || !BgTaskTypes[t.Type] || !BgTaskStatuses[t.Status] ||
			(t.AgentType != "" && !bgAgentRE.MatchString(t.AgentType)) {
			return fmt.Errorf("background task not sanitised: %+v", t)
		}
	}
	for _, c := range crons {
		if !bgHashRE.MatchString(c.IDHash) || !bgCronRE.MatchString(c.Schedule) || !bgCronOK.MatchString(c.Schedule) {
			return fmt.Errorf("scheduled wakeup not sanitised: %+v", c)
		}
	}
	return nil
}

// WriteBgTasks records what event reported for agent, whose process is pid.
// One script: the event's Redis TIME is its at; a task seen before (by id
// hash, same pid) keeps its first_seen, a new one takes at; a record of
// another pid is replaced whole; an empty list is a reading too (none in
// flight).
func (b *Bus) WriteBgTasks(ctx context.Context, agent, event string, pid int, tasks []BgTask, crons []BgCron) error {
	if !ValidName(agent) || (event != "Stop" && event != "SubagentStop") || pid <= 0 {
		return fmt.Errorf("invalid background record: agent %q, event %q, pid %d", agent, event, pid)
	}
	if err := validBgRecord(tasks, crons); err != nil {
		return err
	}
	if tasks == nil {
		tasks = []BgTask{}
	}
	if crons == nil {
		crons = []BgCron{}
	}
	in, err := json.Marshal(BgTasks{Source: "hooks", Event: event, PID: pid, Tasks: tasks, Crons: crons})
	if err != nil {
		return err
	}
	return bgTasksWrite.Run(ctx, b.r, []string{BgTasksKey(b.project, agent)}, string(in), BgTasksTTL.Milliseconds()).Err()
}

var bgTasksWrite = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local rec = cjson.decode(ARGV[1])
local seen = {}
local old = redis.call('GET', KEYS[1])
if old then
  local ok, o = pcall(cjson.decode, old)
  if ok and type(o) == 'table' and o.pid == rec.pid and type(o.tasks) == 'table' then
    for _, x in ipairs(o.tasks) do seen[x.id_hash] = x.first_seen end
  end
end
for _, x in ipairs(rec.tasks) do x.first_seen = seen[x.id_hash] or now end
rec.at = now
local out = cjson.encode(rec)
out = string.gsub(out, '"tasks":{}', '"tasks":[]')
out = string.gsub(out, '"crons":{}', '"crons":[]')
redis.call('SET', KEYS[1], out, 'PX', ARGV[2])
return 1`)

// DropBgTasks forgets an agent's record (its SessionStart: nothing in flight
// yet in a new session).
func (b *Bus) DropBgTasks(ctx context.Context, agent string) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	return b.r.Del(ctx, BgTasksKey(b.project, agent)).Err()
}

// BgTasks reads an agent's record; ok is false when there is none (never
// reported, dropped, or past its hour).
func (b *Bus) BgTasks(ctx context.Context, agent string) (BgTasks, bool, error) {
	raw, err := b.r.Get(ctx, BgTasksKey(b.project, agent)).Result()
	if errors.Is(err, redis.Nil) {
		return BgTasks{}, false, nil
	}
	if err != nil {
		return BgTasks{}, false, err
	}
	var r BgTasks
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return BgTasks{}, false, fmt.Errorf("background record of %s: %w", agent, err)
	}
	return r, true, nil
}

// BgTasksOf reads the agents' records in one round trip, for busmon's poll:
// an agent without one (or with one that does not decode) is left out.
func (b *Bus) BgTasksOf(ctx context.Context, agents []string) (map[string]BgTasks, error) {
	pipe := b.r.Pipeline()
	cmds := map[string]*redis.StringCmd{}
	for _, a := range agents {
		if ValidName(a) {
			cmds[a] = pipe.Get(ctx, BgTasksKey(b.project, a))
		}
	}
	out := map[string]BgTasks{}
	if len(cmds) == 0 {
		return out, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	for a, c := range cmds {
		raw, err := c.Result()
		if err != nil {
			continue
		}
		var r BgTasks
		if json.Unmarshal([]byte(raw), &r) == nil {
			out[a] = r
		}
	}
	return out, nil
}
