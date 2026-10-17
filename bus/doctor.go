package bus

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// DoctorKey (v0.15.1 §7.2) holds the last `roles doctor --publish` verdict
// per agent, for busmon, which reads Redis only. Not a project marker.
func DoctorKey(project string) string { return project + ":doctor" }

// ModelVerdict is one agent's configured, launched and used model, judged
// for the process PID (a relaunched agent is no longer concerned).
type ModelVerdict struct {
	PID        int    `json:"pid,omitempty"`
	Configured string `json:"configured,omitempty"`
	Launched   string `json:"launched,omitempty"`
	Used       string `json:"used,omitempty"`
	Verdict    string `json:"verdict"`
	Why        string `json:"why,omitempty"`
	At         int64  `json:"at"` // Unix ms, Redis TIME
}

var doctorReplace = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('DEL', KEYS[1])
for i = 1, #ARGV, 2 do
  local v = cjson.decode(ARGV[i + 1])
  v['at'] = now
  redis.call('HSET', KEYS[1], ARGV[i], cjson.encode(v))
end
return 1`)

// PutModelVerdicts replaces the published verdicts.
func (b *Bus) PutModelVerdicts(ctx context.Context, v map[string]ModelVerdict) error {
	var argv []interface{}
	for a, mv := range v {
		if !ValidName(a) {
			continue
		}
		raw, err := json.Marshal(mv)
		if err != nil {
			return err
		}
		argv = append(argv, a, string(raw))
	}
	return doctorReplace.Run(ctx, b.r, []string{DoctorKey(b.project)}, argv...).Err()
}

// ModelVerdicts reads them.
func (b *Bus) ModelVerdicts(ctx context.Context) (map[string]ModelVerdict, error) {
	raw, err := b.r.HGetAll(ctx, DoctorKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]ModelVerdict, len(raw))
	for a, s := range raw {
		var v ModelVerdict
		if json.Unmarshal([]byte(s), &v) == nil {
			out[a] = v
		}
	}
	return out, nil
}
