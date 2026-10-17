package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/usage"
)

const doctorManifest = `schema_version = 2
[defaults]
profile = "main"
[profiles.main]
client = "claude-code"
provider = "anthropic"
model = "claude-opus-5-5"
[profiles.alias]
client = "claude-code"
provider = "anthropic"
model = "opus"
[roles.coder]
tier = "boot"
[roles.foureyes]
tier = "boot"
[roles.master]
tier = "boot"
profile = "alias"
[roles.deploy]
tier = "pop"
[roles.sentinel]
tier = "boot"
[roles.docs]
tier = "pop"
`

// v0.15.1 §7, §11.6–7: configured vs launched vs used, with aliases, dated
// ids, the process command line for agents that recorded no model, stale
// transcripts and model errors.
func TestDoctorVerdicts(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	path := filepath.Join(t.TempDir(), "roles.toml")
	if err := os.WriteFile(path, []byte(doctorManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := func(agent string, pid int, model, session string) {
		if _, err := b.Status(ctx, agent, "idle", "", bus.AgentIdent{Pane: "p-" + agent, PID: pid, PIDStart: 1, Model: model, Session: session, Client: "claude-code", Provider: "anthropic"}); err != nil {
			t.Fatal(err)
		}
	}
	reg("coder", 11, "claude-opus-5-5", "s-coder")       // ok, and its transcript agrees (dated id)
	reg("foureyes", 12, "claude-opus-4-8", "")           // drift: the manifest moved on
	reg("master", 13, "opus", "s-master")                // alias: whatever it resolves to is fine
	reg("deploy", 14, "", "")                            // no model recorded: the process says
	reg("sentinel", 15, "claude-opus-5-5", "s-sentinel") // its transcript shows another model
	reg("docs", 16, "claude-opus-5-5", "s-docs")         // the API refused the model
	now := time.Now()
	d := doctorDeps{
		Manifest:  func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) },
		ProcStart: func(int) (uint64, error) { return 1, nil },
		Cmdline: func(pid int) ([]string, error) {
			if pid == 14 {
				return []string{"claude", "--model", "claude-sonnet-5", "--name", "x"}, nil
			}
			return nil, errors.New("none")
		},
		Transcript: func(s string) (usage.UsedModel, error) {
			switch s {
			case "s-coder":
				return usage.UsedModel{Model: "claude-opus-5-5-20261001", TS: now.UnixMilli()}, nil
			case "s-master":
				return usage.UsedModel{Model: "claude-opus-5-5", TS: now.UnixMilli()}, nil
			case "s-sentinel":
				return usage.UsedModel{Model: "claude-haiku-4-5", TS: now.UnixMilli()}, nil
			case "s-docs":
				return usage.UsedModel{Model: "claude-opus-5-5", ModelError: "404 not_found_error model", TS: now.UnixMilli()}, nil
			}
			return usage.UsedModel{}, errors.New("no transcript")
		},
		Now: func() time.Time { return now },
	}
	rows, err := doctorRows(ctx, b, d)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"coder": "ok", "foureyes": "drift", "master": "ok", "deploy": "drift", "sentinel": "drift", "docs": "unknown model"}
	got := map[string]string{}
	for _, row := range rows {
		got[row.Agent] = row.Verdict
		if row.Agent == "deploy" && (row.Launched != "claude-sonnet-5" || row.LaunchedBy != "process") {
			t.Errorf("deploy launched %+v", row)
		}
	}
	for a, v := range want {
		if got[a] != v {
			t.Errorf("%s: verdict %q, want %q", a, got[a], v)
		}
	}
	var out, errw bytes.Buffer
	if code := runDoctor(ctx, b, d, []string{"--publish"}, &out, &errw); code != 3 {
		t.Fatalf("exit %d, want 3 on drift: %s", code, errw.String())
	}
	v, _ := b.ModelVerdicts(ctx)
	if v["foureyes"].Verdict != "drift" || v["foureyes"].PID != 12 || v["foureyes"].At == 0 {
		t.Fatalf("published %+v", v["foureyes"])
	}
}
