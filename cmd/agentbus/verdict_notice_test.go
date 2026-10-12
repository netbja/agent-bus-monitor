package main

import (
	"context"
	"strings"
	"testing"
)

// A mission review is a challenge the conductor opened on the REVIEWER: the
// verdict must not report it as missing on the author's gate.
func TestVerdictGateNotice(t *testing.T) {
	b, _ := dialMain(t)
	ctx := context.Background()
	if err := b.OpenChallenge(ctx, "claude", "roles-go-t3-4", "conductor|review"); err != nil {
		t.Fatal(err)
	}
	if got := verdictGateNotice(ctx, b, "codex", "claude", "roles-go-t3-4"); !strings.Contains(got, "the conductor closes it") || strings.Contains(got, "no open challenge") {
		t.Fatalf("mission review notice = %q", got)
	}
	if err := b.OpenChallenge(ctx, "codex", "r1", "claude|please"); err != nil {
		t.Fatal(err)
	}
	if got := verdictGateNotice(ctx, b, "codex", "claude", "r1"); got != "" {
		t.Fatalf("classic gate on the author: notice %q, want none", got)
	}
	if open, _ := b.OpenChallenges(ctx, "codex"); len(open) != 0 {
		t.Fatalf("the author's gate was not resolved: %v", open)
	}
	if got := verdictGateNotice(ctx, b, "codex", "claude", "typo"); !strings.Contains(got, `no open challenge "typo"`) {
		t.Fatalf("unknown ref notice = %q", got)
	}
	if open, _ := b.OpenChallenges(ctx, "claude"); len(open) != 1 {
		t.Fatalf("the reviewer's mission gate must stay for the conductor: %v", open)
	}
}
