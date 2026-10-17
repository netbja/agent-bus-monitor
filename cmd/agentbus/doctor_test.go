package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/models"
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
[roles.architect]
tier = "pop"
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
	reg("architect", 17, "claude-opus-5-5", "s-old")     // refused two days ago: stale, not a verdict
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
				return usage.UsedModel{Model: "claude-opus-5-5", ModelError: "404 not_found_error model", ErrorTS: now.UnixMilli()}, nil
			case "s-old":
				return usage.UsedModel{Model: "claude-opus-5-5", ModelError: "404 not_found_error model", ErrorTS: now.Add(-48 * time.Hour).UnixMilli()}, nil
			}
			return usage.UsedModel{}, errors.New("no transcript")
		},
		Now: func() time.Time { return now },
	}
	rows, err := doctorRows(ctx, b, d)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"coder": "ok", "foureyes": "drift", "master": "ok", "deploy": "drift", "sentinel": "drift", "docs": "unknown model", "architect": "ok"}
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

// v0.15.2: the table turns a matching launch into gone or newer, from a
// fresh complete read only, and resolves a tier.
func TestDoctorCatalogueVerdicts(t *testing.T) {
	now := time.Now()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"openai": {OK: true, ReadAt: now.UnixMilli(), Rows: []models.Row{
			{ID: "gpt-6.1-sol", Tier: models.Workhorse},
			{ID: "gpt-6-sol", Tier: models.Legacy, Newer: "gpt-6.1-sol"},
			{ID: "gpt-6-astra", Tier: models.Frontier},
		}},
		"stale": {OK: false, ReadAt: now.Add(-10 * 24 * time.Hour).UnixMilli()},
	}}
	d := doctorDeps{Table: tbl, Now: func() time.Time { return now }}
	for _, c := range []struct{ provider, launched, want string }{
		{"openai", "gpt-6.1-sol", ""},
		{"openai", "gpt-6-sol", "newer"},
		{"openai", "gpt-5-old", "gone"},
		{"stale", "anything", ""}, // an old catalogue judges nothing
	} {
		if v, _ := catalogueVerdict(d, c.provider, c.launched); v != c.want {
			t.Errorf("%s/%s: %q, want %q", c.provider, c.launched, v, c.want)
		}
	}
	cfg := models.Config{Families: map[string][]string{"grok": {"grok-4.7", "grok-4.6"}}}
	tbl.Providers["xai"] = &models.Provider{OK: true, ReadAt: now.UnixMilli(), Rows: []models.Row{{ID: "grok-4.7"}, {ID: "grok-4.6"}}}
	d.Config = cfg
	if v, why := catalogueVerdict(d, "xai", "grok-4.6"); v != "newer" || !strings.Contains(why, "grok-4.7") {
		t.Fatalf("family order: %q %q", v, why)
	}
}

func TestDoctorPartialCatalogueNeverGone(t *testing.T) {
	now := time.Now()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"anthropic": {OK: true, ReadAt: now.UnixMilli(), Partial: true, Rows: []models.Row{{ID: "opus", Alias: true}}},
	}}
	d := doctorDeps{Table: tbl, Now: func() time.Time { return now }}
	if v, _ := catalogueVerdict(d, "anthropic", "claude-opus-5-5"); v != "" {
		t.Fatalf("an id a partial catalogue does not list was judged %q", v)
	}
}

// Codex re-check: "newer" only names a model the fresh catalogue lists.
func TestDoctorNewerMustExist(t *testing.T) {
	now := time.Now()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"openai": {OK: true, ReadAt: now.UnixMilli(), Rows: []models.Row{
			{ID: "gpt-6-sol", Tier: models.Legacy, Newer: "gpt-7-missing"},
		}},
	}}
	d := doctorDeps{Table: tbl, Now: func() time.Time { return now }}
	if v, _ := catalogueVerdict(d, "openai", "gpt-6-sol"); v != "" {
		t.Fatalf("newer pointed at a missing model: %q", v)
	}
}
