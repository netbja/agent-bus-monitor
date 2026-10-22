package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// "same provider" beside an independent verdict whose reviewer shares the
// author's provider; the state and exit code do not change.
func TestVerdictsShowSameProvider(t *testing.T) {
	now := time.Now()
	same := []bus.Verdict{{Subject: "pr:9", Author: "coder", Reviewer: "foureyes", Decision: "approve", TS: now.UnixMilli(),
		AuthorProvider: "anthropic", ReviewerProvider: "anthropic"}}
	out, code := verdictsReport("pr:9", same, now)
	if code != 0 || !strings.Contains(out, "(same provider)") {
		t.Fatalf("code %d out %q", code, out)
	}
	for _, v := range []bus.Verdict{
		{Subject: "pr:9", Author: "coder", Reviewer: "foureyes", Decision: "approve", TS: now.UnixMilli(), AuthorProvider: "anthropic", ReviewerProvider: "openai"},
		{Subject: "pr:9", Author: "coder", Reviewer: "foureyes", Decision: "approve", TS: now.UnixMilli()},
	} {
		if out, _ := verdictsReport("pr:9", []bus.Verdict{v}, now); strings.Contains(out, "same provider") {
			t.Fatalf("%+v marked: %q", v, out)
		}
	}
}
