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

// The glob in ProjectKeys is built from the caller's string, so ValidName is the
// only thing between `--delete '*'` and an erased broker. A nil client is
// deliberate: the guard must fire before anything is dialled, so a nil-pointer
// panic here would itself be the failure.
func TestProjectKeysRejectsGlobsBeforeDialing(t *testing.T) {
	for _, bad := range []string{"*", "", "a:b", "de*mo", "demo?", "[a-z]", "Demo", "*:status"} {
		if keys, err := ProjectKeys(context.Background(), nil, bad); err == nil {
			t.Errorf("ProjectKeys(%q) = %v, nil; want an error", bad, keys)
		}
	}
	for _, bad := range []string{"*", "", "a:b", "de*mo"} {
		if n, err := DeleteProject(context.Background(), nil, bad); err == nil || n != 0 {
			t.Errorf("DeleteProject(%q) = %d, %v; want 0, error", bad, n, err)
		}
	}
}

// "demo:*" must not reach "demo-2574:status" — the trailing colon makes the
// prefix exact, so deleting a project cannot take a same-prefixed sibling.
func TestProjectKeysIsAnExactPrefix(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	sibling := b.project + "-sib"
	if err := b.r.Set(ctx, sibling+":pilot", "master", time.Minute).Err(); err != nil {
		t.Fatalf("seed sibling: %v", err)
	}
	t.Cleanup(func() { b.r.Del(ctx, PilotKey(sibling)) })

	if _, err := b.Status(ctx, "coder", "working", "hi", AgentIdent{}); err != nil {
		t.Fatalf("Status: %v", err)
	}

	keys, err := ProjectKeys(ctx, b.r, b.project)
	if err != nil {
		t.Fatalf("ProjectKeys: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("ProjectKeys found nothing for a project that just published")
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, b.project+":") {
			t.Errorf("key %q is not owned by %q", k, b.project)
		}
	}
	if n, _ := b.r.Exists(ctx, PilotKey(sibling)).Result(); n != 1 {
		t.Error("the sibling project's key vanished")
	}
}

func TestDeleteProjectRemovesEverything(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	// a consumer group is the documented difference from Bus.Purge: XTRIM keeps
	// it, DEL takes it with the stream.
	if err := b.r.XGroupCreateMkStream(ctx, StreamKey(b.project, "cmd"), "coder", "0").Err(); err != nil {
		t.Fatalf("XGroupCreateMkStream: %v", err)
	}
	if _, err := b.Status(ctx, "coder", "working", "hi", AgentIdent{}); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := b.Report(ctx, "coder", "note", "hello"); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if err := b.Pilot(ctx, "master", time.Minute); err != nil {
		t.Fatalf("Pilot: %v", err)
	}
	if err := b.OpenChallenge(ctx, "coder", "ref1", "meta"); err != nil {
		t.Fatalf("OpenChallenge: %v", err)
	}
	if err := b.Arm(ctx, "coder", "host", time.Minute); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	if err := b.SetUsage(ctx, "coder", UsageSnapshot{Ctx: "1k ctx"}); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}

	before, err := ProjectKeys(ctx, b.r, b.project)
	if err != nil {
		t.Fatalf("ProjectKeys: %v", err)
	}
	if len(before) < 6 {
		t.Fatalf("expected the full footprint, got %v", before)
	}

	n, err := DeleteProject(ctx, b.r, b.project)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if int(n) != len(before) {
		t.Errorf("deleted %d keys, want %d (%v)", n, len(before), before)
	}

	after, err := ProjectKeys(ctx, b.r, b.project)
	if err != nil {
		t.Fatalf("ProjectKeys after delete: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("keys survived the delete: %v", after)
	}
	if groups, err := b.r.XInfoGroups(ctx, StreamKey(b.project, "cmd")).Result(); err == nil && len(groups) > 0 {
		t.Errorf("consumer group survived the delete: %v", groups)
	}

	list, err := Projects(ctx, b.r)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	for _, p := range list {
		if p.Project == b.project {
			t.Error("the deleted project is still discoverable by Projects")
		}
	}
}

// Deleting a project that is not there is a no-op success, so a repeated cleanup
// script does not fail on its second run.
func TestDeleteProjectOnAbsentProjectIsANoOp(t *testing.T) {
	b := dialTest(t)
	n, err := DeleteProject(context.Background(), b.r, b.project+"-nope")
	if err != nil || n != 0 {
		t.Errorf("DeleteProject(absent) = %d, %v; want 0, nil", n, err)
	}
}
