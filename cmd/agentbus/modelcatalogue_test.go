package main

import (
	"context"
	"errors"
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
	if len(in.OldSources) != 1 || in.OldSources[0].Provider != "moonshot" || !strings.Contains(in.OldSources[0].Hint, "kimi") {
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
