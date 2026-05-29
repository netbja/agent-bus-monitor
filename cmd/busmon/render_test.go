package main

import (
	"strings"
	"testing"
	"time"
)

func TestPilotLabel(t *testing.T) {
	if got := pilotLabel(""); !strings.Contains(got, "autonome") {
		t.Fatalf("pilotLabel(\"\") = %q, want it to mention autonome", got)
	}
	if got := pilotLabel("hermes"); !strings.Contains(got, "piloté par hermes") {
		t.Fatalf("pilotLabel(hermes) = %q, want it to mention 'piloté par hermes'", got)
	}
}

func TestEntryTime(t *testing.T) {
	if got, want := entryTime("1779707136877-0"), time.UnixMilli(1779707136877); !got.Equal(want) {
		t.Fatalf("entryTime = %v, want %v", got, want)
	}
	if entryTime("bogus").IsZero() {
		t.Fatal("entryTime(bogus) returned zero time, want a fallback")
	}
}

func TestSelPos(t *testing.T) {
	feed := []feedLine{{id: "1"}, {id: "2"}, {id: "3"}}
	for _, tc := range []struct {
		id   string
		want int
	}{
		{"1", 0},
		{"3", 2},
		{"99", -1}, // scrolled out of the capped feed
		{"", -1},   // live mode: no selection
	} {
		if got := selPos(feed, tc.id); got != tc.want {
			t.Errorf("selPos(feed, %q) = %d, want %d", tc.id, got, tc.want)
		}
	}
	if got := selPos(nil, "1"); got != -1 {
		t.Errorf("selPos(nil, _) = %d, want -1", got)
	}
}

func TestSelectionTitle(t *testing.T) {
	got := selectionTitle(3, 42)
	if !strings.Contains(got, "3/42") {
		t.Errorf("selectionTitle(3,42) = %q, want it to show 3/42", got)
	}
	if !strings.Contains(got, "copier") {
		t.Errorf("selectionTitle = %q, want it to mention the copy key", got)
	}
}
