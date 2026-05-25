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
