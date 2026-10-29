package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestHumanIdentity(t *testing.T) {
	for _, c := range []struct{ flag, env, want string }{
		{"", "", "pilot"}, {"charlie", "", "charlie"}, {"", "alice", "alice"}, {"charlie", "alice", "charlie"},
	} {
		if got, err := humanIdentity(c.flag, c.env); err != nil || got != c.want {
			t.Fatalf("humanIdentity(%q, %q) = %q, %v; want %q", c.flag, c.env, got, err, c.want)
		}
	}
	if _, err := humanIdentity("Jane Doe", ""); err == nil {
		t.Fatal("an invalid name was accepted")
	}
}

func TestHumanLabel(t *testing.T) {
	for _, c := range []struct{ self, pilot, want string }{
		{"charlie", "charlie", GlyphHuman + " Charlie [human · pilot]"},
		{"pilot", "", GlyphHuman + " Pilot [human]"},
		{"charlie", "codex", GlyphHuman + " Charlie [human]"},
	} {
		if got := humanLabel(c.self, c.pilot); got != c.want {
			t.Fatalf("humanLabel(%q, %q) = %q, want %q", c.self, c.pilot, got, c.want)
		}
	}
}

func TestLookEventShowsHumans(t *testing.T) {
	look := lookEvent(bus.Event{Kind: "cmd", Type: "directive", From: "charlie", Target: "codex", Message: "x", Human: true})
	if look.From != GlyphHuman+" Charlie" {
		t.Fatalf("human from = %q", look.From)
	}
	if look := lookEvent(bus.Event{Kind: "cmd", Type: "directive", From: "codex", Target: "claude", Message: "x"}); look.From != "codex" {
		t.Fatalf("agent from = %q", look.From)
	}
}

func TestHeaderShowsTheHuman(t *testing.T) {
	now := time.Now()
	for _, c := range []struct{ pilot, want string }{{"charlie", "[human · pilot]"}, {"", "Charlie [human]"}} {
		lines := headerLines(headerData{project: "demo", self: "charlie", pilot: c.pilot, now: now, health: health{ok: true, since: now}}, 160, TierWide)
		text := ""
		for _, l := range lines {
			for _, s := range l {
				text += s.Text
			}
		}
		if !strings.Contains(text, c.want) {
			t.Fatalf("header %q lacks %q", text, c.want)
		}
	}
}

// failFor fails HumanCmd for one target only.
type failFor struct {
	*fakeWriter
	target string
}

func (f failFor) HumanCmd(ctx context.Context, from, target, command string) (string, error) {
	if target == f.target {
		return "", errors.New("connection reset")
	}
	return f.fakeWriter.HumanCmd(ctx, from, target, command)
}
