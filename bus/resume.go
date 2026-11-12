package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
)

// {p}:resume (herdr exit step 8, spec 2026-11-10-herdr-exit-design.md 4.7):
// the command that resumes each agent's last proved session, field = agent,
// value = JSON {argv, session, at}. A hash of its own, not a field of the
// {p}:agents snapshot: it must outlive the process and every later status
// that does not carry it, which no merge rule there does. Cleared only by
// forget; replaced by the next proved session's report.

// ResumeKey is the project's resume hash.
func ResumeKey(project string) string { return project + ":resume" }

// ResumeRecord is one agent's resume: the argv agent-launch runs, the session
// it resumes, and when it was recorded (Redis TIME, ms).
type ResumeRecord struct {
	Argv    []string `json:"argv"`
	Session string   `json:"session"`
	At      int64    `json:"at"`
}

// ProcID is one process for its whole life: a pid and its start time.
type ProcID struct {
	PID   int
	Start uint64
}

// MaxResumeAncestors bounds the hook's ancestor chain: its client is its
// parent, or a few shells up, never 32 levels.
const MaxResumeAncestors = 32

// RecordResume records argv as the command that resumes agent's session. The
// caller is the client's SessionStart hook, never the agent's process itself,
// so the proof is its ancestor chain (nearest first): the write lands only
// when one ancestor is the agent's stored verified process, pid and start
// time alike (a reused pid differs in its start), in one optimistic
// transaction on {p}:agents; anything else is ErrNotProved and writes
// nothing. The proof is self-attested like the turn hooks': it stops an
// honest mistake, not a same-user process that lies.
func (b *Bus) RecordResume(ctx context.Context, agent string, chain []ProcID, argv []string, session string) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	if !ValidSessionID(session) {
		return fmt.Errorf("record resume: %q is not a session id", session)
	}
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("record resume: no command")
	}
	if len(chain) == 0 || len(chain) > MaxResumeAncestors {
		return ErrNotProved
	}
	agentsKey, resumeKey := AgentsKey(b.project), ResumeKey(b.project)
	txf := func(tx *redis.Tx) error {
		raw, err := tx.HGet(ctx, agentsKey, agent).Result()
		if err == redis.Nil {
			return ErrNotProved
		}
		if err != nil {
			return err
		}
		var s AgentSnapshot
		if json.Unmarshal([]byte(raw), &s) != nil || s.PID <= 0 || s.PIDStart == 0 {
			return ErrNotProved
		}
		proved := false
		for _, p := range chain {
			if p.PID == s.PID && p.Start == s.PIDStart {
				proved = true
			}
		}
		if !proved {
			return ErrNotProved
		}
		now, err := tx.Time(ctx).Result()
		if err != nil {
			return err
		}
		rec, err := json.Marshal(ResumeRecord{Argv: argv, Session: session, At: now.UnixMilli()})
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HSet(ctx, resumeKey, agent, rec)
			return nil
		})
		return err
	}
	for i := 0; i < 50; i++ { // a busy {p}:agents fails the WATCH: back off and retry, as updateSnapshot
		err := b.r.Watch(ctx, txf, agentsKey)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1+rand.IntN(5)) * time.Millisecond):
		}
	}
	return redis.TxFailedErr
}

// parseResume reads a field; anything that is no usable record is none.
func parseResume(raw string) *ResumeRecord {
	var r ResumeRecord
	if json.Unmarshal([]byte(raw), &r) != nil || len(r.Argv) == 0 || r.Argv[0] == "" || !ValidSessionID(r.Session) {
		return nil
	}
	return &r
}

// ResumeOf is agent's record, nil when none (or not one).
func (b *Bus) ResumeOf(ctx context.Context, agent string) (*ResumeRecord, error) {
	raw, err := b.r.HGet(ctx, ResumeKey(b.project), agent).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseResume(raw), nil
}

// ResumeRecords is every agent's record.
func (b *Bus) ResumeRecords(ctx context.Context) (map[string]ResumeRecord, error) {
	all, err := b.r.HGetAll(ctx, ResumeKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]ResumeRecord, len(all))
	for agent, raw := range all {
		if r := parseResume(raw); r != nil && ValidName(agent) {
			out[agent] = *r
		}
	}
	return out, nil
}
