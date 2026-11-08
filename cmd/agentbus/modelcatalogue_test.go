package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// model-detection §3.5: the reader turns the table and roles.toml into the
// check's input: findings on configured models only, old sources and long
// failures of the providers a profile uses, and the table's events.
func TestModelCatalogueReader(t *testing.T) {
	now := time.Now()
	cache, manifest := checkFixture(t, now)
	tbl, _ := models.Load(cache)
	tbl.Providers["moonshot"].SourceAt = now.Add(-12 * 24 * time.Hour).UnixMilli()
	tbl.Providers["google"].ReadAt = now.Add(-3 * 24 * time.Hour).UnixMilli() // failing for 3 days
	tbl.Events = []models.Event{{At: now.Add(-time.Hour).UnixMilli(), Provider: "xai", Kind: "added", ID: "grok-4.7"}}
	tbl.Providers["unused"] = &models.Provider{OK: false, At: now.UnixMilli(), Error: "not installed"} // no profile: never a finding
	if err := tbl.Save(cache); err != nil {
		t.Fatal(err)
	}
	r := catalogueReader{cache: cache, manifest: manifest, readers: nil, now: func() time.Time { return now }}
	in := r.input(context.Background())
	if !in.Known {
		t.Fatalf("unknown: %s", in.ReadErr)
	}
	keys := map[string]bool{}
	for _, f := range in.Findings {
		keys[f.Key()] = true
	}
	for _, k := range []string{"old|model|gpt-5-old|gone|", "sol|model|gpt-6-sol|newer|gpt-6.1-sol", "grok|model|grok-4.6|newer?|grok-4.7"} {
		if !keys[k] {
			t.Errorf("finding %s missing: %v", k, keys)
		}
	}
	if len(in.Findings) != 3 {
		t.Errorf("findings %v (ok, partial, untiered and unknown are none)", keys)
	}
	if len(in.OldSources) != 1 || in.OldSources[0].Provider != "moonshot" || !strings.Contains(in.OldSources[0].Hint, "agentbus models --refresh") {
		t.Errorf("old sources %+v", in.OldSources)
	}
	if len(in.FailedReads) != 1 || in.FailedReads[0].Provider != "google" || in.FailedReads[0].For < 72*time.Hour-time.Minute {
		t.Errorf("failed reads %+v", in.FailedReads)
	}
	if len(in.Events) != 1 || in.Events[0].ID != "grok-4.7" {
		t.Errorf("events %+v", in.Events)
	}
	if out := health.EvaluateModelCatalogue(in); out.Status != health.Warn {
		t.Fatalf("outcome %+v", out)
	}
	r.manifest = func() (*rolecfg.Manifest, error) { return nil, errors.New("no roles.toml") }
	if in := r.input(context.Background()); in.Known || in.ReadErr != "role manifest unavailable" {
		t.Fatalf("no manifest: %+v", in)
	}
}

// The check refreshes a table over 24 h old first, bounded, under the lock.
func TestModelCatalogueRefreshesFirst(t *testing.T) {
	now := time.Now()
	cache, manifest := checkFixture(t, now)
	age(t, cache, 25*time.Hour)
	calls := 0
	r := catalogueReader{cache: cache, manifest: manifest, readers: models.Readers(countingEnv(&calls, nil)), now: func() time.Time { return now }}
	r.input(context.Background())
	if calls != 1 {
		t.Fatalf("refresh calls %d", calls)
	}
	r.input(context.Background())
	if calls != 1 {
		t.Fatalf("a young table was refreshed again: %d", calls)
	}
}

// §3.5: a client update forces its provider's refresh, once per installed
// version; a failed read is tried again at the next pass.
func TestRefreshForClientUpdates(t *testing.T) {
	now := time.Now()
	cache, _ := checkFixture(t, now)
	reads := map[string]int{}
	fail := false
	reader := func(p, id string) models.Reader {
		return models.Reader{Provider: p, Read: func(context.Context) ([]models.Row, string, error) {
			reads[p]++
			if fail {
				return nil, "", errors.New("timeout")
			}
			return []models.Row{{Provider: p, ID: id}}, "", nil
		}}
	}
	readers := []models.Reader{reader("xai", "grok-5"), reader("openai", "gpt-7")}
	refresh := func(updates map[string]string) []string {
		got, err := refreshForClients(context.Background(), cache, nil, readers, updates, now)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := refresh(map[string]string{"grok": "1.2.0"}); len(got) != 1 || got[0] != "xai" || reads["xai"] != 1 || reads["openai"] != 0 {
		t.Fatalf("refreshed %v, reads %v", got, reads)
	}
	if tbl, _ := models.Load(cache); tbl.Providers["xai"].Rows[0].ID != "grok-5" || tbl.Clients["xai"] != "1.2.0" {
		t.Fatalf("table %+v %v", tbl.Providers["xai"], tbl.Clients)
	}
	if got := refresh(map[string]string{"grok": "1.2.0"}); len(got) != 0 || reads["xai"] != 1 {
		t.Fatalf("the same version refreshed again: %v", got)
	}
	fail = true
	refresh(map[string]string{"codex": "0.9"})
	if tbl, _ := models.Load(cache); tbl.Clients["openai"] != "" {
		t.Fatal("a failed read was recorded as done")
	}
	fail = false
	if got := refresh(map[string]string{"codex": "0.9", "unknown-client": "1"}); len(got) != 1 || got[0] != "openai" {
		t.Fatalf("retry: %v", got)
	}
}

// The client-updates evidence names the clients to refresh: outdated ones
// with an installed version only.
func TestClientUpdates(t *testing.T) {
	got := clientUpdates([]health.ClientEvidence{
		{Agent: "a", Outdated: true, Client: "grok", Installed: "1.2.0"},
		{Agent: "b", Outdated: false, Client: "codex", Installed: "0.9"},
		{Agent: "c", Outdated: true, Client: "kimi-cli"},
	})
	if len(got) != 1 || got["grok"] != "1.2.0" {
		t.Fatalf("%v", got)
	}
}

// §3.1, G5: the doctor task records what a Claude alias resolves to, from
// the used model of the transcripts; a move writes one alias event; a held
// lock skips.
func TestRecordAliases(t *testing.T) {
	now := time.Now()
	cache, _ := checkFixture(t, now)
	fresh := now.Add(-time.Hour).UnixMilli()
	rows := []doctorRow{
		{Agent: "coder", Configured: "opus", Used: "claude-opus-5-5-20261001", usedAt: fresh},
		{Agent: "master", Configured: "sonnet", Used: "claude-sonnet-5-5", usedAt: fresh},
		{Agent: "ui", Configured: "gpt-6-sol", Used: "gpt-6-sol", usedAt: fresh},                                    // not an alias
		{Agent: "docs", Configured: "haiku"},                                                                        // no used model
		{Agent: "old", Configured: "haiku", Used: "claude-haiku-4-5", usedAt: now.Add(-48 * time.Hour).UnixMilli()}, // stale
	}
	if err := recordAliases(cache, rows, now); err != nil {
		t.Fatal(err)
	}
	tbl, _ := models.Load(cache)
	if a := tbl.Aliases["anthropic"]; a["opus"].ID != "claude-opus-5-5" || a["sonnet"].ID != "claude-sonnet-5-5" || len(a) != 2 || len(tbl.Events) != 0 { // haiku: stale, unrecorded
		t.Fatalf("first resolution, silent: %+v %+v", a, tbl.Events)
	}
	rows[0].Used, rows[0].usedAt = "claude-opus-5-6", now.Add(30*time.Minute).UnixMilli()
	rows = append(rows, doctorRow{Agent: "arch", Configured: "opus", Used: "claude-opus-5-5", usedAt: fresh}) // older use: never moves it back
	if err := recordAliases(cache, rows, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	tbl, _ = models.Load(cache)
	if len(tbl.Events) != 1 || tbl.Events[0].Kind != "alias" || tbl.Events[0].From != "claude-opus-5-5" || tbl.Events[0].To != "claude-opus-5-6" {
		t.Fatalf("events %+v", tbl.Events)
	}
}

// foureyes: the dispatcher never runs repository code, so its model readers
// (the model-catalogue check, the daily refresh) are the built-in clients
// only, never an adapter's argv from roles.toml.
func TestDispatcherReadsNoAdapter(t *testing.T) {
	providers := func(rs []models.Reader) string {
		var p []string
		for _, r := range rs {
			p = append(p, r.Provider)
		}
		return strings.Join(p, ",")
	}
	// roles.toml names an adapter with a models argv: still not read.
	path := t.TempDir() + "/roles.toml"
	m := checkManifest + "[adapters.deep]\nexecutable = \"./d\"\nmodels = [\"./d\"]\n[profiles.ds]\nclient = \"deep\"\nprovider = \"deepseek\"\nmodel = \"v4\"\n"
	if err := os.WriteFile(path, []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROLES_TOML", path)
	if got := modelReaders(models.Env{}, manifestFromEnv, io.Discard); len(got) != len(models.Readers(models.Env{}))+1 {
		t.Fatalf("the fixture's adapter is not seen by modelReaders: %d readers", len(got))
	}
	builtin := providers(models.Readers(models.Env{}))
	if got := providers(dispatcherModelReaders()); got != builtin {
		t.Fatalf("dispatcher readers %s, want the built-in %s", got, builtin)
	}
	if got := providers(realCatalogueReader().readers); got != builtin {
		t.Fatalf("model-catalogue readers %s, want the built-in %s", got, builtin)
	}
}

// An adapter's provider, old: the hint is the command a person runs.
func TestCatalogueAdapterHint(t *testing.T) {
	now := time.Now()
	cache, _ := checkFixture(t, now)
	path := t.TempDir() + "/roles.toml"
	m := checkManifest + "[adapters.deep]\nexecutable = \"./d\"\nmodels = [\"./d\"]\n[profiles.ds]\nclient = \"deep\"\nprovider = \"deepseek\"\nmodel = \"v4\"\n"
	if err := os.WriteFile(path, []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
	tbl, _ := models.Load(cache)
	tbl.Providers["deepseek"] = &models.Provider{OK: true, At: now.UnixMilli(), ReadAt: now.UnixMilli(), SourceAt: now.Add(-9 * 24 * time.Hour).UnixMilli(), Rows: []models.Row{{ID: "v4"}}}
	if err := tbl.Save(cache); err != nil {
		t.Fatal(err)
	}
	r := catalogueReader{cache: cache, manifest: func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) }, now: func() time.Time { return now }}
	for _, o := range r.input(context.Background()).OldSources {
		if o.Provider == "deepseek" {
			if o.Hint != "agentbus models --refresh" {
				t.Fatalf("hint %q", o.Hint)
			}
			return
		}
	}
	t.Fatal("the adapter's old source is not named")
}
