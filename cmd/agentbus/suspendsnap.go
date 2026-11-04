package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/service"
)

// The suspend's snapshot (spec §6): what the host remembers about a suspended
// project, for resume and for the person. Identifiers, states and counts
// only: no message, report, ask or gate text, no status message, no
// environment value but the herdr session. It is never written into Redis.

const suspendSnapshotFile = "suspend.json"

type suspendSnapshot struct {
	V             int                    `json:"v"`
	Project       string                 `json:"project"`
	TakenAt       int64                  `json:"taken_at"`
	Host          string                 `json:"host"`
	By            string                 `json:"by"`
	Reason        string                 `json:"reason,omitempty"`
	Herdr         snapHerdr              `json:"herdr"`
	Services      map[string]snapService `json:"services"`
	PauseReplaced *snapPause             `json:"pause_replaced,omitempty"`
	Agents        map[string]snapAgent   `json:"agents"`
	Open          snapOpen               `json:"open"`
	Unknown       []string               `json:"unknown,omitempty"`
}

type snapHerdr struct {
	Session string `json:"session,omitempty"`
}

type snapService struct {
	WasActive bool `json:"was_active"`
	Stopped   bool `json:"stopped"`
}

type snapPause struct {
	Reason string `json:"reason,omitempty"`
	By     string `json:"by,omitempty"`
	Until  int64  `json:"until"`
}

type snapAgent struct {
	Tier          string `json:"tier,omitempty"`
	Client        string `json:"client,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Pane          string `json:"pane,omitempty"`
	Session       string `json:"session,omitempty"` // the verified IdentSession, never Session
	State         string `json:"state"`
	BusyAtSuspend string `json:"busy_at_suspend,omitempty"` // why, when --force went on with it busy
	Gone          bool   `json:"gone,omitempty"`            // its verified process proved gone
	HandoffReport string `json:"handoff_report,omitempty"`
}

type snapOpen struct {
	Asks     []snapAsk        `json:"asks"`
	Gates    []snapGate       `json:"gates"`
	Board    []snapTask       `json:"board"`
	Waiting  map[string]int64 `json:"waiting"`
	Handoffs []string         `json:"handoffs"`
}

type snapAsk struct {
	ID        string `json:"id"`
	Agent     string `json:"agent"`
	Questions int    `json:"questions"`
}

type snapGate struct {
	Agent      string `json:"agent"`
	Ref        string `json:"ref"`
	Challenger string `json:"challenger"`
	Subject    string `json:"subject,omitempty"`
}

type snapTask struct {
	Task  string `json:"task"`
	Owner string `json:"owner"`
	State string `json:"state"`
}

// counts is the snapshot in one line.
func (s suspendSnapshot) counts() string {
	open := len(s.Open.Asks) + len(s.Open.Gates) + len(s.Open.Board) + len(s.Open.Handoffs)
	for _, n := range s.Open.Waiting {
		if n > 0 {
			open++
		}
	}
	return fmt.Sprintf("%d agents, %d open items", len(s.Agents), open)
}

// readSnapshotFacts reads what the snapshot records. A read that fails is a
// line in Unknown and its list stays nil; an empty list means read, none.
func readSnapshotFacts(ctx context.Context, p string, b *bus.Bus, rec *bus.PauseRecord, busy []busyAgent, d suspendDeps) suspendSnapshot {
	s := suspendSnapshot{V: 1, Project: p, Host: rec.Host, By: rec.By, Reason: health.Redact(rec.Reason),
		Services: map[string]snapService{}, Agents: map[string]snapAgent{}, Open: snapOpen{Waiting: map[string]int64{}}}
	unknown := func(what string, err error) { s.Unknown = append(s.Unknown, what+": "+err.Error()) }
	if now, err := b.ServerTime(ctx); err == nil {
		s.TakenAt = now.UnixMilli()
	} else {
		unknown("server time", err)
		s.TakenAt = rec.At
	}
	if rec.Prev != nil {
		s.PauseReplaced = &snapPause{Reason: health.Redact(rec.Prev.Reason), By: rec.Prev.By, Until: rec.Prev.Until}
	}
	if paths, err := service.PathsFrom(d.Getenv); err == nil {
		if env, err := service.LoadEnvFile(paths.EnvFile(p)); err == nil {
			s.Herdr.Session = env["HERDR_SESSION"]
		} else if !os.IsNotExist(err) {
			unknown("herdr session", err)
		}
	}
	why := map[string]string{}
	for _, a := range busy {
		why[a.Agent] = a.Why
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		unknown("agents", err)
	}
	handoffs, herr := b.Handoffs(ctx)
	if herr != nil {
		unknown("hand-offs", herr)
	} else {
		s.Open.Handoffs = []string{}
	}
	for name, a := range snaps {
		a := a
		ag := snapAgent{Tier: a.Tier, Client: a.Client, Provider: a.Provider, Model: a.Model, Pane: a.Pane,
			Session: a.IdentSession, State: a.State, BusyAtSuspend: why[name], Gone: d.ProcStart != nil && dispatch.ProcGone(&a, d.ProcStart)}
		if h, ok := handoffs[name]; ok {
			ag.HandoffReport = h.Report
			s.Open.Handoffs = append(s.Open.Handoffs, name)
		}
		s.Agents[name] = ag
		if o, err := b.Obligation(ctx, name, bus.DefaultHorizon); err != nil {
			unknown("messages waiting for "+name, err)
		} else if n := o.Pending + int64(o.Unread); n > 0 {
			s.Open.Waiting[name] = n
		}
	}
	sort.Strings(s.Open.Handoffs)
	if asks, err := b.Asks(ctx); err != nil {
		unknown("asks", err)
	} else {
		s.Open.Asks = []snapAsk{}
		for _, a := range asks {
			if n := a.Open(); n > 0 {
				s.Open.Asks = append(s.Open.Asks, snapAsk{ID: a.ID, Agent: a.From, Questions: n})
			}
		}
	}
	if gates, err := b.AllGates(ctx); err != nil {
		unknown("gates", err)
	} else {
		s.Open.Gates = []snapGate{}
		for agent, refs := range gates {
			for ref, meta := range refs {
				g := bus.ParseGateMeta(meta)
				s.Open.Gates = append(s.Open.Gates, snapGate{Agent: agent, Ref: ref, Challenger: g.Challenger, Subject: g.Subject})
			}
		}
		sort.Slice(s.Open.Gates, func(i, j int) bool { return s.Open.Gates[i].Ref < s.Open.Gates[j].Ref })
	}
	if board, err := b.Board(ctx); err != nil {
		unknown("board", err)
	} else {
		s.Open.Board = []snapTask{}
		for task, e := range board {
			if e.State != "done" {
				s.Open.Board = append(s.Open.Board, snapTask{Task: task, Owner: e.Owner, State: e.State})
			}
		}
		sort.Slice(s.Open.Board, func(i, j int) bool { return s.Open.Board[i].Task < s.Open.Board[j].Task })
	}
	return s
}

// writeSnapshot writes the file 0600 in a 0700 folder, through a temporary
// file renamed over it.
func writeSnapshot(path string, s suspendSnapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".suspend-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func loadSnapshot(path string) (suspendSnapshot, error) {
	var s suspendSnapshot
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	if s.V != 1 {
		return s, fmt.Errorf("%s: version %d unknown", path, s.V)
	}
	return s, nil
}
