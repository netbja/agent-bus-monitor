package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The background half of the turn hooks (background-work-visible step 1):
// Claude Code's Stop and SubagentStop carry background_tasks and
// session_crons (recorded in testdata/hooks); the hook reports them, cleaned,
// as the agent's {p}:bgtasks record; SessionStart forgets it. A task's
// command and description, a cron's prompt and every raw id stay here.

// hookTask and hookCron are what the payload carries that the hook reads.
type hookTask struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	AgentType string `json:"agent_type"`
}

type hookCron struct {
	ID        string `json:"id"`
	Schedule  string `json:"schedule"`
	Recurring bool   `json:"recurring"`
}

// bgReport is what one hook event reports: the cleaned lists, or drop.
type bgReport struct {
	event string
	drop  bool
	tasks []bus.BgTask
	crons []bus.BgCron
}

// String is the report as a test greps it for what must never leave.
func (r bgReport) String() string {
	return fmt.Sprintf("%s drop=%v tasks=%+v crons=%+v", r.event, r.drop, r.tasks, r.crons)
}

// bgReportOf cleans the payload's lists: ids digested, labels known or
// "other", a sub-agent's name kept when it is a name, a cron kept when its
// schedule has cron's shape. stopped is a SubagentStop's own agent_id: the
// list Claude sends with it still shows that sub-agent running, so it is
// left out (reviewer-codex; the recorded claude-subagentstop.json).
func bgReportOf(event, stopped string, tasks []hookTask, crons []hookCron) bgReport {
	r := bgReport{event: event}
	for _, t := range tasks {
		if stopped != "" && t.ID == stopped {
			continue
		}
		bt := bus.BgTask{IDHash: bus.BgIDHash(t.ID), Type: bus.BgLabel(bus.BgTaskTypes, t.Type), Status: bus.BgLabel(bus.BgTaskStatuses, t.Status)}
		if bt.Type == "subagent" {
			bt.AgentType = bus.BgAgentType(t.AgentType)
		}
		r.tasks = append(r.tasks, bt)
	}
	for _, c := range crons {
		if bus.BgSchedule(c.Schedule) {
			r.crons = append(r.crons, bus.BgCron{IDHash: bus.BgIDHash(c.ID), Schedule: strings.TrimSpace(c.Schedule), Recurring: c.Recurring})
		}
	}
	return r
}

// turnBg sends a report; tests replace it.
var turnBg = realTurnBg

// realTurnBg dials the broker within ctx, proves the caller's process as
// status does, and writes or drops the record. Unproved, nothing is written:
// the record must name the agent's own process.
func realTurnBg(ctx context.Context, project, agent string, r bgReport) error {
	client, err := bus.ConnectContext(ctx, "")
	if err != nil {
		return err
	}
	defer client.Close()
	b, err := bus.Open(client, project)
	if err != nil {
		return err
	}
	if r.drop {
		return b.DropBgTasks(ctx, agent)
	}
	who := statusIdent(ctx)
	if who.PID <= 0 {
		return fmt.Errorf("background tasks of %s: process not proved", agent)
	}
	return b.WriteBgTasks(ctx, agent, r.event, who.PID, r.tasks, r.crons)
}
