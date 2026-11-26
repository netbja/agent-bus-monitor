package bus

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// Observed (v0.14.3): what the dispatcher SAW in each agent's pane, kept
// apart from what the agent DECLARED. An agent that works without declaring
// `working` still shows as busy; its declared state is never rewritten.

// ObservedKey is the {p}:observed hash: agent → Observation JSON. The whole
// hash expires ObservedTTL after the last write, so a stopped dispatcher
// leaves no stale "busy" behind. It proves nothing about the project: it is
// not in projectKinds and HasKeysBesidesDispatch ignores it.
func ObservedKey(project string) string { return project + ":observed" }

// ObservedTTL bounds how long an observation outlives its dispatcher.
const ObservedTTL int64 = 120000 // ms

// Observation is one agent's pane as last inspected.
type Observation struct {
	Busy  bool   `json:"busy"`            // the client is running a turn (the host's status or busy signature)
	State string `json:"state,omitempty"` // the host's agent status: working, idle, blocked, done, unknown
	At    int64  `json:"at"`              // Unix ms, Redis TIME of the inspection
	// Since (v0.15) is the Redis TIME of the first reading of the current
	// state by the same process: it identifies an episode (one bell for one
	// blocked tab). PID is the process inspected.
	Since int64 `json:"since,omitempty"`
	PID   int   `json:"pid,omitempty"`
	// ScreenHash is an opaque digest of a stable inspected screen. Screen text
	// is never written to Redis; repeats support the health runaway warning.
	ScreenHash    string `json:"screen_hash,omitempty"`
	ScreenRepeats int    `json:"screen_repeats,omitempty"`
	// ScreenSince (additive, agent-screen-blocked §3) is the Redis TIME of
	// the first reading of ScreenHash by PID: how long the screen has stood
	// unchanged (a stuck screen); 0 with no hash.
	ScreenSince int64 `json:"screen_since,omitempty"`
	// Update (client reload spec, additive): the client installed now is not
	// the one this process runs. ClientJudged: the dispatcher could tell
	// (a launch fingerprint or the Claude Code layout), whatever the answer.
	Update       *ClientUpdate `json:"update,omitempty"`
	ClientJudged bool          `json:"client_judged,omitempty"`
	// BgProcs and BgOldest (additive, background-work-visible step 2): the
	// process source's active non-helper descendants of the agent, and the
	// oldest one's start (Redis clock, Unix ms; 0 when none). No process
	// name or argument is ever published.
	BgProcs  int   `json:"bg_procs,omitempty"`
	BgOldest int64 `json:"bg_oldest,omitempty"`
	// MemBytes, TopBytes and CPUPct (additive, agent-resource-limits §5):
	// the agent's terminal tree's resident memory, its largest process's,
	// and its CPU over the dispatcher's window as a percentage of one core
	// (nil while unknown). 0 bytes: not read (a host without /proc).
	MemBytes int64 `json:"mem_bytes,omitempty"`
	TopBytes int64 `json:"top_bytes,omitempty"`
	CPUPct   *int  `json:"cpu_pct,omitempty"`
}

// ClientUpdate is an installed client newer than the running one. Labels are
// for display and may be empty; Hash identifies the installed version (the
// update episode); Since is the Redis TIME of its first observation.
type ClientUpdate struct {
	Client    string `json:"client,omitempty"`
	Running   string `json:"running,omitempty"`
	Installed string `json:"installed,omitempty"`
	Hash      string `json:"hash"`
	Source    string `json:"source"` // fingerprint or layout
	Since     int64  `json:"since,omitempty"`
}

// Fresh reports whether the observation is younger than ObservedTTL at
// nowMs: a reading carried forward past a failed inspection ages out.
func (o Observation) Fresh(nowMs int64) bool { return nowMs-o.At < ObservedTTL }

// observedReplace: 0 not the holder, else 1; the hash becomes exactly the
// given agent/observation pairs (none = deleted) and expires after ARGV[2] ms.
var observedReplace = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
if #ARGV > 2 then
  redis.call('HSET', KEYS[2], unpack(ARGV, 3))
  redis.call('PEXPIRE', KEYS[2], ARGV[2])
end
return 1`)

// FencedPutObserved replaces {p}:observed while token holds the dispatch lease.
func (b *Bus) FencedPutObserved(ctx context.Context, token string, obs map[string]Observation) error {
	argv := []interface{}{token, ObservedTTL}
	for a, o := range obs {
		if !ValidName(a) {
			continue
		}
		v, err := json.Marshal(o)
		if err != nil {
			return err
		}
		argv = append(argv, a, string(v))
	}
	n, err := observedReplace.Run(ctx, b.r, []string{DispatchKey(b.project), ObservedKey(b.project)}, argv...).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}

// Observed reads {p}:observed; a malformed entry is skipped.
func (b *Bus) Observed(ctx context.Context) (map[string]Observation, error) {
	raw, err := b.r.HGetAll(ctx, ObservedKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Observation, len(raw))
	for a, v := range raw {
		var o Observation
		if json.Unmarshal([]byte(v), &o) == nil {
			out[a] = o
		}
	}
	return out, nil
}
