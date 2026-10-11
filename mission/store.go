package mission

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/netbja/agent-bus-monitor/bus"
)

// ChangeRecord is a loaded change: which file at which commit, and its state
// (active or aborted). The plan itself stays in Git; only its hash is kept.
type ChangeRecord struct {
	Change         string `json:"change"`
	Path           string `json:"path"`
	Commit         string `json:"commit"`
	FileHash       string `json:"file_hash"`
	Base           string `json:"base"`
	Branch         string `json:"branch"`
	Rev            int    `json:"rev"`
	State          string `json:"state"`
	LoadedAt       int64  `json:"loaded_at"`
	DirtyEscalated bool   `json:"dirty_escalated,omitempty"` // a dirty integration worktree was reported
}

// Attempt is one request for a mission. Outcome is empty while it runs, then
// superseded, check_failed, rejected or approved.
type Attempt struct {
	N       int    `json:"n"`
	Task    string `json:"task"`
	Agent   string `json:"agent"`
	Base    string `json:"base,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Reason  string `json:"reason,omitempty"`
	By      string `json:"by,omitempty"`     // the task that replaced it
	Closed  bool   `json:"closed,omitempty"` // board entry superseded and agent told
}

// MissionRecord is everything the conductor knows about one mission.
type MissionRecord struct {
	ID              string    `json:"id"`
	Hash            string    `json:"hash"`
	State           State     `json:"state"`
	Attempt         int       `json:"attempt"`
	Rejections      int       `json:"rejections"`
	Current         Attempt   `json:"current"`
	History         []Attempt `json:"history,omitempty"`
	Reviewer        string    `json:"reviewer,omitempty"`
	Subject         string    `json:"subject,omitempty"`
	ReviewRef       string    `json:"review_ref,omitempty"`
	ReviewOpen      bool      `json:"review_open,omitempty"` // the reviewer's gate is open for ReviewRef
	Escalated       bool      `json:"escalated,omitempty"`   // the blocked state was escalated
	LogPath         string    `json:"log_path,omitempty"`
	CheckTail       string    `json:"check_tail,omitempty"`
	PreHead         string    `json:"pre_head,omitempty"`
	TestedHead      string    `json:"tested_head,omitempty"`
	TestedOK        bool      `json:"tested_ok,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	IntegratedUnder string    `json:"integrated_under,omitempty"` // reload --force kept it integrated: "rev <n> <hash>"
	AssignedAgent   string    `json:"assigned_agent,omitempty"`   // set by reassign: the agent of every later attempt
	SkipBy          string    `json:"skip_by,omitempty"`          // a skip whose ledger entry is still owed
	SkipReason      string    `json:"skip_reason,omitempty"`
	Updated         int64     `json:"updated"`
}

// Store reads and writes change and mission records. Every write goes through
// the conductor's fenced scripts with Token; reads need no lease.
type Store struct {
	B     *bus.Bus
	Token string
}

const changeField = bus.ChangeRecordField

func (s Store) Change(ctx context.Context, c string) (ChangeRecord, bool, error) {
	h, err := s.B.ReadMissionHash(ctx, bus.ChangeKey(s.B.Project(), c))
	if err != nil || h[changeField] == "" {
		return ChangeRecord{}, false, err
	}
	var r ChangeRecord
	if err := json.Unmarshal([]byte(h[changeField]), &r); err != nil {
		return ChangeRecord{}, false, fmt.Errorf("change %s: %w", c, err)
	}
	return r, true, nil
}

func (s Store) PutChange(ctx context.Context, r ChangeRecord) error {
	b, _ := json.Marshal(r)
	return s.B.FencedHashSet(ctx, s.Token, bus.ChangeKey(s.B.Project(), r.Change), changeField, string(b))
}

func (s Store) Missions(ctx context.Context, c string) (map[string]MissionRecord, error) {
	h, err := s.B.ReadMissionHash(ctx, bus.MissionsKey(s.B.Project(), c))
	if err != nil {
		return nil, err
	}
	out := make(map[string]MissionRecord, len(h))
	for id, v := range h {
		var r MissionRecord
		if err := json.Unmarshal([]byte(v), &r); err != nil {
			return nil, fmt.Errorf("mission %s/%s: %w", c, id, err)
		}
		out[id] = r
	}
	return out, nil
}

// PutMission stamps Updated from Redis TIME, then writes the record.
func (s Store) PutMission(ctx context.Context, c string, r MissionRecord) error {
	now, err := s.B.ServerNowMS(ctx)
	if err != nil {
		return err
	}
	r.Updated = now
	b, _ := json.Marshal(r)
	return s.B.FencedHashSet(ctx, s.Token, bus.MissionsKey(s.B.Project(), c), r.ID, string(b))
}

func (s Store) Changes(ctx context.Context) ([]string, error) { return s.B.ChangeNames(ctx) }

// PutBatch writes mission records and the change record in one fenced script,
// each record stamped with Redis TIME.
func (s Store) PutBatch(ctx context.Context, records []MissionRecord, ch ChangeRecord) error {
	now, err := s.B.ServerNowMS(ctx)
	if err != nil {
		return err
	}
	ms := make(map[string]string, len(records))
	for _, r := range records {
		r.Updated = now
		b, _ := json.Marshal(r)
		ms[r.ID] = string(b)
	}
	cb, _ := json.Marshal(ch)
	return s.B.FencedMissionBatch(ctx, s.Token, ch.Change, ms, string(cb))
}
