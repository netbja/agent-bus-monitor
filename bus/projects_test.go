package bus

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProjectFromKey(t *testing.T) {
	cases := []struct {
		key  string
		want string // "" means: not a project key
	}{
		{"dev:status", "dev"},
		{"dev:report", "dev"},
		{"dev:notify", "dev"},
		{"dev:cmd", "dev"},
		{"dev:agents", "dev"},
		{"dev:usage", "dev"},
		{"dev:budget", "dev"},
		{"dev:verdicts", "dev"},
		{"dev:pilot", "dev"},
		{"ai-tradex-solana:status", "ai-tradex-solana"},

		// two-colon keys: the "project" keeps a colon, so ValidName rejects them
		// and they need no special case in the scanner.
		{"dev:gate:coder", ""},
		{"dev:armed:coder", ""},

		// an agent literally named "agents" must not forge a project either
		{"dev:gate:agents", ""},

		// foreign keys on a shared broker
		{"dev:bogus", ""},
		{"sessions", ""},
		{"celery-task-meta-1", ""},
		{"Dev:status", ""}, // uppercase fails ValidName
		{":status", ""},
	}
	for _, c := range cases {
		got, ok := projectFromKey(c.key)
		if c.want == "" {
			if ok {
				t.Errorf("projectFromKey(%q) = %q, true; want not-a-project", c.key, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("projectFromKey(%q) = %q, %v; want %q, true", c.key, got, ok, c.want)
		}
	}
}

func TestSortProjectsNewestFirst(t *testing.T) {
	list := []ProjectSummary{
		{Project: "old", LastTS: 100},
		{Project: "newest", LastTS: 900},
		{Project: "b-tie", LastTS: 500},
		{Project: "a-tie", LastTS: 500},
		{Project: "never", LastTS: 0},
	}
	sortProjects(list)
	want := []string{"newest", "a-tie", "b-tie", "old", "never"}
	for i, w := range want {
		if list[i].Project != w {
			t.Fatalf("order[%d] = %q, want %q (full: %+v)", i, list[i].Project, w, list)
		}
	}
}

func TestProjectsFindsALiveProject(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if _, err := b.Status(ctx, "coder", "working", "hello", AgentIdent{}); err != nil {
		t.Fatalf("Status: %v", err)
	}
	// An open challenge writes {p}:gate:coder — the key most likely to forge a
	// phantom project, so the discovery path is exercised with one present.
	if err := b.OpenChallenge(ctx, "coder", "ref1", "meta"); err != nil {
		t.Fatalf("OpenChallenge: %v", err)
	}
	t.Cleanup(func() { b.r.Del(ctx, GateKey(b.project, "coder")) })

	list, err := Projects(ctx, b.r)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}

	var got *ProjectSummary
	for i := range list {
		if strings.Contains(list[i].Project, ":") {
			t.Errorf("discovered a project with a colon in its name: %q", list[i].Project)
		}
		if list[i].Project == b.project {
			got = &list[i]
		}
	}
	if got == nil {
		t.Fatalf("Projects did not find %q (found %d projects)", b.project, len(list))
	}
	if got.Agents != 1 {
		t.Errorf("Agents = %d, want 1", got.Agents)
	}
	if got.LastTS <= 0 {
		t.Errorf("LastTS = %d, want a real stamp", got.LastTS)
	}
	if got.Master != "" {
		t.Errorf("Master = %q, want empty (no pilot lease taken)", got.Master)
	}

	// the pilot lease is what busmon shows as MASTER
	if err := b.Pilot(ctx, "master", time.Minute); err != nil {
		t.Fatalf("Pilot: %v", err)
	}
	list, err = Projects(ctx, b.r)
	if err != nil {
		t.Fatalf("Projects after Pilot: %v", err)
	}
	for _, p := range list {
		if p.Project == b.project && p.Master != "master" {
			t.Errorf("Master = %q, want %q", p.Master, "master")
		}
	}
}

// A project purged by `busmon --reset` must still be dated: XTRIM empties the
// streams but leaves the :agents hash, which is why summary reads both.
func TestProjectsDatesAPurgedProject(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if _, err := b.Status(ctx, "coder", "working", "hello", AgentIdent{}); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := b.Purge(ctx, activityKinds); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if ts, err := b.lastStreamTS(ctx); err != nil || ts != 0 {
		t.Fatalf("lastStreamTS after purge = %d, %v; want 0, nil", ts, err)
	}

	s, err := b.summary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if s.LastTS <= 0 {
		t.Errorf("LastTS = %d after purge, want the :agents hash stamp", s.LastTS)
	}
	if s.Agents != 1 {
		t.Errorf("Agents = %d after purge, want 1", s.Agents)
	}
}
