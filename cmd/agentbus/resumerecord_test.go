package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// Herdr exit step 8 (spec 4.7): the SessionStart hook hands its ancestry and
// the resume command to a detached recorder, which writes {p}:resume; inside
// herdr or not, beside herdr's own report.
func TestReportResumeHandsTheCommandToTheRecorder(t *testing.T) {
	f := newResumeFixture(t)
	f.chain = []dispatch.ProcID{{PID: 40, Start: 400}, {PID: 30, Start: 300}}
	f.hook(clearStart)
	log := filepath.Join(filepath.Dir(f.manifest), "state", "agentbus", "demo", "resume.log")
	want := []string{"roles", "record-resume", log, "demo", "coder", "0c9e2a51-7d4b-4f0e-9a51-2c3d4e5f6a7b", "--", "40:400", "30:300", "--",
		"env", "ROLES_TOML=" + f.manifest, "/r/scripts/agent-launch", "coder", "demo", "--resume", "0c9e2a51-7d4b-4f0e-9a51-2c3d4e5f6a7b"}
	if got := f.spawnedVerb("record-resume"); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("recorder argv\n got %q\nwant %q", got, want)
	}
	if f.spawnedVerb("report-resume-send") == nil || f.spawnedVerb("prove-session") == nil {
		t.Fatalf("herdr's report or the prover dropped: %q", f.spawned)
	}

	// Outside herdr: the record only (the herdr report and its prover stay herdr's).
	for _, v := range []string{"HERDR_ENV=", "HERDR_PANE_ID=", "HERDR_BIN_PATH="} {
		f.hook(clearStart, v)
		if f.spawnedVerb("record-resume") == nil || f.spawnedVerb("report-resume-send") != nil || f.spawnedVerb("prove-session") != nil {
			t.Errorf("without %s: %q", v, f.spawned)
		}
	}
	// Nothing to prove with, or no agent: no recorder.
	for name, c := range map[string]struct {
		stdin string
		chain []dispatch.ProcID
		env   string
	}{
		"no ancestry (macOS)": {clearStart, nil, ""},
		"a subagent":          {strings.Replace(clearStart, `"source"`, `"agent_id":"a1","source"`, 1), f.chain, ""},
		"not a session id":    {strings.Replace(clearStart, "0c9e2a51-7d4b", "../../etc", 1), f.chain, ""},
		"no agent":            {clearStart, f.chain, "AGENT_BUS_AGENT="},
		"no manifest":         {clearStart, f.chain, "ROLES_TOML="},
		"a role not in it":    {clearStart, f.chain, "AGENT_BUS_AGENT=nobody"},
	} {
		f.chain = c.chain
		f.hook(c.stdin, c.env)
		if got := f.spawnedVerb("record-resume"); got != nil {
			t.Errorf("%s: recorder spawned %q", name, got)
		}
	}
}

// fakeRecorder is the bus as the recorder sees it.
type fakeResumeBus struct {
	agent, session string
	chain          []bus.ProcID
	argv           []string
	err            error
}

func (b *fakeResumeBus) RecordResume(_ context.Context, agent string, chain []bus.ProcID, argv []string, session string) error {
	b.agent, b.chain, b.argv, b.session = agent, chain, argv, session
	return b.err
}

// record-resume parses its arguments and hands them to the bus, which proves
// the caller; every outcome is one line of the log.
func TestRecordResumeSend(t *testing.T) {
	const sess = "0c9e2a51-7d4b-4f0e-9a51-2c3d4e5f6a7b"
	log := filepath.Join(t.TempDir(), "resume.log")
	b := &fakeResumeBus{}
	open := func(project string) (resumeRecorder, func(), error) {
		if project != "demo" {
			t.Fatalf("project %q", project)
		}
		return b, func() {}, nil
	}
	recordResumeSend([]string{log, "demo", "coder", sess, "--", "40:400", "30:300", "--", "env", "agent-launch", "coder", "demo", "--resume", sess}, open)
	if b.agent != "coder" || b.session != sess || len(b.chain) != 2 || b.chain[1] != (bus.ProcID{PID: 30, Start: 300}) ||
		strings.Join(b.argv, " ") != "env agent-launch coder demo --resume "+sess {
		t.Fatalf("recorded %+v", b)
	}
	if out, _ := os.ReadFile(log); !strings.Contains(string(out), "resume recorded: coder "+sess) {
		t.Fatalf("log %q", out)
	}
	b.err = bus.ErrNotProved
	recordResumeSend([]string{log, "demo", "coder", sess, "--", "40:400", "--", "agent-launch"}, open)
	if out, _ := os.ReadFile(log); !strings.Contains(string(out), "resume not recorded: "+bus.ErrNotProved.Error()) {
		t.Fatalf("log %q", out)
	}
	for name, args := range map[string][]string{
		"no separator":   {log, "demo", "coder", sess, "40:400", "agent-launch"},
		"no command":     {log, "demo", "coder", sess, "--", "40:400", "--"},
		"no chain":       {log, "demo", "coder", sess, "--", "--", "agent-launch"},
		"a bad ancestor": {log, "demo", "coder", sess, "--", "40-400", "--", "agent-launch"},
		"a zero pid":     {log, "demo", "coder", sess, "--", "0:400", "--", "agent-launch"},
	} {
		b.agent = ""
		recordResumeSend(args, open)
		if b.agent != "" {
			t.Errorf("%s: recorded %+v", name, b)
		}
	}
	recordResumeSend([]string{log, "demo", "coder", sess, "--", "40:400", "--", "agent-launch"}, func(string) (resumeRecorder, func(), error) {
		return nil, nil, errors.New("no broker")
	})
	if out, _ := os.ReadFile(log); !strings.Contains(string(out), "resume not recorded: no broker") {
		t.Fatalf("log %q", out)
	}
}
