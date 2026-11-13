package bus

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

const resumeSession = "0f1e2d3c-aaaa-bbbb-cccc-000000000001"

// The caller proof (spec 2026-11-10-herdr-exit-design.md 4.7, 8): a write
// lands only when one ancestor of the hook is the agent's stored verified
// process, pid AND start; at most 32 ancestors.
func TestRecordResumeNeedsAnAncestor(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	verifiedAgent(t, b, "idle", "x") // pid 4242, start 99
	long := make([]ProcID, 32)
	for i := range long {
		long[i] = ProcID{PID: 1000 + i, Start: 1}
	}
	for name, c := range map[string]struct {
		chain []ProcID
		ok    bool
	}{
		"the client is the parent":         {[]ProcID{{4242, 99}}, true},
		"a grandchild through a shell":     {[]ProcID{{777, 5}, {4242, 99}, {1, 1}}, true},
		"the 32nd ancestor":                {append(long[:31:31], ProcID{4242, 99}), true},
		"not a descendant":                 {[]ProcID{{777, 5}, {1, 1}}, false},
		"the pid reused, another start":    {[]ProcID{{777, 5}, {4242, 98}}, false},
		"a chain longer than 32":           {append(slices.Clone(long), ProcID{4242, 99}), false},
		"no chain":                         {nil, false},
		"a zero start matches no identity": {[]ProcID{{4242, 0}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			b.r.Del(ctx, ResumeKey(b.project))
			err := b.RecordResume(ctx, "coder", c.chain, "", resumeSession)
			got, gerr := b.ResumeOf(ctx, "coder")
			if gerr != nil {
				t.Fatal(gerr)
			}
			if c.ok {
				if err != nil || got == nil || got.Profile != "" || got.Session != resumeSession || got.At <= 0 {
					t.Fatalf("not written: %v %+v", err, got)
				}
				return
			}
			if !errors.Is(err, ErrNotProved) || got != nil {
				t.Fatalf("written without a proof: %v %+v", err, got)
			}
		})
	}
	if err := b.RecordResume(ctx, "nobody", []ProcID{{4242, 99}}, "", resumeSession); !errors.Is(err, ErrNotProved) {
		t.Fatalf("no snapshot: %v", err)
	}
	// An agent with no verified process: a zero ancestor matches nothing.
	if _, err := b.Status(ctx, "kimi", "idle", "unverified", AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordResume(ctx, "kimi", []ProcID{{0, 0}}, "", resumeSession); !errors.Is(err, ErrNotProved) {
		t.Fatalf("an unverified agent: %v", err)
	}
}

// What a write needs besides the proof: a valid agent, a session id, and a
// profile that is a name ("" the role's own).
func TestRecordResumeRefuses(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	verifiedAgent(t, b, "idle", "x")
	ok := []ProcID{{4242, 99}}
	for name, c := range map[string]struct {
		agent, session, profile string
	}{
		"agent":              {"Bad Agent", resumeSession, ""},
		"session":            {"coder", "../etc", ""},
		"a command":          {"coder", resumeSession, "sh -c 'touch /tmp/x'"},
		"a path":             {"coder", resumeSession, "../profiles/x"},
		"an uppercase start": {"coder", resumeSession, "Opus"},
	} {
		if err := b.RecordResume(ctx, c.agent, ok, c.profile, c.session); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if got, _ := b.ResumeOf(ctx, "coder"); got != nil {
		t.Fatalf("written: %+v", got)
	}
}

// The record outlives the process: a status, a hand-off and the agent's
// death leave it; the next proved session replaces it; forget clears it.
func TestResumeOutlivesTheProcess(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	who := verifiedAgent(t, b, "idle", "x")
	if err := b.RecordResume(ctx, "coder", []ProcID{{4242, 99}}, "", resumeSession); err != nil {
		t.Fatal(err)
	}
	first, _ := b.ResumeOf(ctx, "coder")
	if _, err := b.Status(ctx, "coder", "working", "on it", who); err != nil {
		t.Fatal(err)
	}
	if err := b.ProveSession(ctx, "coder", who.PID, who.PIDStart, resumeSession, 100); err != nil {
		t.Fatal(err)
	}
	rep, err := b.Report(ctx, "coder", ReportNote, "hand-off: next y")
	if err != nil {
		t.Fatal(err)
	}
	who.Session = resumeSession
	if _, err := b.DeclareHandoff(ctx, "coder", rep, who); err != nil {
		t.Fatal(err)
	}
	// Its death: a new process takes the role.
	if _, err := b.Status(ctx, "coder", "idle", "relaunched", AgentIdent{Pane: "p2", PID: 5151, PIDStart: 7}); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.ResumeOf(ctx, "coder"); got == nil || got.Session != first.Session || got.At != first.At {
		t.Fatalf("lost: %+v", got)
	}
	all, err := b.ResumeRecords(ctx)
	if err != nil || len(all) != 1 || all["coder"].Session != resumeSession {
		t.Fatalf("resumes %+v %v", all, err)
	}
	// The old process can no longer write: the stored identity moved.
	if err := b.RecordResume(ctx, "coder", []ProcID{{4242, 99}}, "", resumeSession); !errors.Is(err, ErrNotProved) {
		t.Fatalf("the dead process wrote: %v", err)
	}
	const next = "0f1e2d3c-aaaa-bbbb-cccc-000000000002"
	if err := b.RecordResume(ctx, "coder", []ProcID{{5151, 7}}, "claude-sonnet", next); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.ResumeOf(ctx, "coder"); got == nil || got.Session != next || got.Profile != "claude-sonnet" {
		t.Fatalf("not replaced: %+v", got)
	}
	if _, err := b.ForgetAgent(ctx, "coder"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.ResumeOf(ctx, "coder"); got != nil {
		t.Fatalf("forget kept it: %+v", got)
	}
}

// A field that is no record reads as none, never as an error that would
// stop a start.
func TestResumeReadsAMalformedFieldAsNone(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	b.r.HSet(ctx, ResumeKey(b.project), "coder", "not json", "kimi", `{"session":"x"}`,
		"grok", `{"session":"`+resumeSession+`","profile":"sh -c id"}`)
	if got, err := b.ResumeOf(ctx, "coder"); err != nil || got != nil {
		t.Fatalf("coder %+v %v", got, err)
	}
	if all, err := b.ResumeRecords(ctx); err != nil || len(all) != 0 {
		t.Fatalf("all %+v %v", all, err)
	}
}

// resume is a record: a backup restores it, and it proves the project.
func TestResumeKeyIsARecord(t *testing.T) {
	if c, kind := ClassOf("demo", ResumeKey("demo")); c != KeyRecord || kind != "resume" {
		t.Fatalf("%v %q", c, kind)
	}
	if !projectKinds["resume"] {
		t.Fatal("resume is not a project kind")
	}
}

// Security finding S1 (herdr security review): the record is a closed set,
// {session, profile, at}, never a command. A field written by hand with an
// argv (a broker writer that skips the proof) is read without it: a
// consumer rebuilds the command from the template with
// rolecfg.ResumeArgv and the record's re-validated session and profile, so
// what the broker holds can never run.
func TestResumeRecordCarriesNoCommand(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	verifiedAgent(t, b, "idle", "x")
	if err := b.RecordResume(ctx, "coder", []ProcID{{4242, 99}}, "claude-sonnet", resumeSession); err != nil {
		t.Fatal(err)
	}
	raw, err := b.r.HGet(ctx, ResumeKey(b.project), "coder").Result()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"at", "profile", "session"}) {
		t.Fatalf("stored fields %v: want at, profile, session only", keys)
	}
	b.r.HSet(ctx, ResumeKey(b.project), "coder", `{"argv":["sh","-c","touch /tmp/x"],"session":"`+resumeSession+`","at":1}`)
	got, err := b.ResumeOf(ctx, "coder")
	if err != nil || got == nil || *got != (ResumeRecord{Session: resumeSession, At: 1}) {
		t.Fatalf("a hostile field read as %+v %v", got, err)
	}
}
