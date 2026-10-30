package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func pauseFixture(now time.Time) (*pauseDeps, *bus.PauseRecord) {
	rec := &bus.PauseRecord{}
	d := &pauseDeps{
		Set: func(_ context.Context, by, notify, reason string, until time.Time) (bus.PauseRecord, error) {
			*rec = bus.PauseRecord{Until: until.UnixMilli(), By: by, Notify: notify, Reason: reason}
			return *rec, nil
		},
		Get: func(context.Context) (*bus.PauseRecord, error) {
			if rec.Until == 0 {
				return nil, nil
			}
			return rec, nil
		},
		Resume: func(context.Context, string, string) (bool, error) {
			ok := rec.Until != 0
			*rec = bus.PauseRecord{}
			return ok, nil
		},
		Budgets: func(context.Context) (map[string]bus.BudgetSnapshot, error) {
			return map[string]bus.BudgetSnapshot{"anthropic": {SessionReset: now.Add(90 * time.Minute).UTC().Format(time.RFC3339)}}, nil
		},
		Now: func() time.Time { return now },
		Env: func(string) string { return "" },
	}
	return d, rec
}

func TestPauseDeadlines(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 30, 0, 0, time.Local)
	for _, c := range []struct {
		args []string
		want time.Time
	}{
		{[]string{"--for", "2h", "quota"}, now.Add(2 * time.Hour)},
		{[]string{"--until", "02:15"}, time.Date(2026, 10, 15, 2, 15, 0, 0, time.Local)},
		{[]string{"--until", "00:10"}, time.Date(2026, 10, 16, 0, 10, 0, 0, time.Local)}, // passed today: tomorrow
		{[]string{"--until-reset"}, now.Add(90*time.Minute + resetMargin)},
	} {
		d, rec := pauseFixture(now)
		var out, errw bytes.Buffer
		if code := runPause(context.Background(), *d, "master", c.args, &out, &errw); code != 0 {
			t.Fatalf("%v: code %d %s", c.args, code, errw.String())
		}
		if got := rec.UntilTime(); !got.Equal(c.want) || rec.Notify != "master" {
			t.Fatalf("%v: until %v notify %q, want %v", c.args, got, rec.Notify, c.want)
		}
	}
}

func TestPauseRefusesNoDeadlineAndShowsStatus(t *testing.T) {
	now := time.Now()
	d, _ := pauseFixture(now)
	var out, errw bytes.Buffer
	if code := runPause(context.Background(), *d, "master", []string{"quota"}, &out, &errw); code != 2 {
		t.Fatalf("no deadline: code %d", code)
	}
	if code := runPause(context.Background(), *d, "master", []string{"--status"}, &out, &errw); code != 1 || !strings.Contains(out.String(), "not paused") {
		t.Fatalf("status before: %d %q", code, out.String())
	}
	d.Env = func(k string) string {
		if k == "AGENT_BUS_ESCALATE_TO" {
			return "lead"
		}
		return ""
	}
	runPause(context.Background(), *d, "charlie", []string{"--for", "90m", "quota"}, &out, &errw)
	out.Reset()
	if code := runPause(context.Background(), *d, "x", []string{"--status"}, &out, &errw); code != 0 || !strings.Contains(out.String(), "in 1h30m") || !strings.Contains(out.String(), "resume goes to lead") {
		t.Fatalf("status: %d %q", code, out.String())
	}
	out.Reset()
	runResume(context.Background(), *d, "charlie", nil, &out, &errw)
	if !strings.Contains(out.String(), "resumed") {
		t.Fatalf("resume: %q", out.String())
	}
}
