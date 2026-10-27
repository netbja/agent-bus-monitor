package main

import (
	"strings"
	"testing"
	"time"
)

func dormantShared(now time.Time) *shared {
	st := demoShared(now)
	st.agents["claude-hl-master"] = &agentState{state: "working", message: "old team",
		stateAt: now.Add(-20 * 24 * time.Hour), lastSeen: now.Add(-20 * 24 * time.Hour)}
	return st
}

func TestTodaysSeparatorSaysToday(t *testing.T) {
	now := time.Date(2026, 10, 14, 22, 0, 0, 0, time.Local)
	todayFunc = func() time.Time { return now }
	t.Cleanup(func() { todayFunc = time.Now })
	today := activityRow(feedLine{sep: true, at: now.Add(-time.Hour)}, TierWide, 60)
	yesterday := activityRow(feedLine{sep: true, at: now.Add(-24 * time.Hour)}, TierWide, 60)
	if today.Width() != 60 || yesterday.Width() != 60 {
		t.Fatalf("widths %d %d", today.Width(), yesterday.Width())
	}
	found := false
	for _, s := range today {
		found = found || (s.Text == "(today)" && s.Role == RoleOK)
	}
	if !found {
		t.Fatalf("today = %+v", today)
	}
	for _, s := range yesterday {
		if strings.Contains(s.Text, "today") {
			t.Fatalf("yesterday = %+v", yesterday)
		}
	}
}

func TestTodaysSeparatorReadsDateDashToday(t *testing.T) {
	now := time.Date(2026, 10, 14, 22, 0, 0, 0, time.Local)
	todayFunc = func() time.Time { return now }
	t.Cleanup(func() { todayFunc = time.Now })
	got := activityRow(feedLine{sep: true, at: now}, TierWide, 40).text()
	if !strings.Contains(got, " Wed 2026-10-14 - (today) ") {
		t.Fatalf("separator %q", got)
	}
}
