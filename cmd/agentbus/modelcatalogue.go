package main

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// model-catalogue's reader (spec 2026-11-08-model-detection-design.md §3.5):
// the machine's model table, refreshed first when it is over 24 h old, and
// roles.toml judged as `agentbus models check` judges it. The judgement is
// health.EvaluateModelCatalogue.

// catalogueRefreshBudget bounds the check's refresh, inside its own task
// deadline (30 s): the same 10 s as `agentbus models`.
const catalogueRefreshBudget = 10 * time.Second

// sourceHints says how to refresh a provider's catalogue whose source is
// old; the providers whose readers ask their server live are never old.
var sourceHints = map[string]string{
	"openai":   "open codex once: it rewrites models_cache.json",
	"moonshot": "open kimi once, or update Kimi Code",
}

type catalogueReader struct {
	cache    string
	config   string // models.toml
	manifest func() (*rolecfg.Manifest, error)
	readers  []models.Reader
	now      func() time.Time
}

func realCatalogueReader() catalogueReader {
	return catalogueReader{cache: models.CachePath(), config: models.ConfigPath(), readers: dispatcherModelReaders(), now: time.Now, manifest: manifestFromEnv}
}

// dispatcherModelReaders are the readers the dispatcher may run (the
// model-catalogue check, the daily refresh): the built-in clients only, from
// PATH. An adapter's models argv is relative to roles.toml, in the
// repository agents commit to, and the dispatcher never runs repository
// code (foureyes): adapters are read by agentbus models, run by a person or
// an agent.
func dispatcherModelReaders() []models.Reader { return models.Readers(models.RealEnv()) }

// manifestFromEnv is roles.toml as the launcher finds it.
func manifestFromEnv() (*rolecfg.Manifest, error) {
	path, err := manifestPath(os.Environ())
	if err != nil {
		return nil, err
	}
	return rolecfg.Load(path)
}

func (r catalogueReader) run(ctx context.Context) (health.Outcome, error) {
	return health.EvaluateModelCatalogue(r.input(ctx)), nil
}

func (r catalogueReader) input(ctx context.Context) health.ModelCatalogueInput {
	now := r.now()
	in := health.ModelCatalogueInput{Now: now}
	cfg, err := loadModelsConfig(r.config)
	if err != nil {
		in.ReadErr = "models.toml unreadable"
		return in
	}
	// G7: no service needed; a held lock or a failed reader keeps the table as it is.
	_, _, _ = refreshTable(ctx, r.cache, cfg.Tiers, r.readers, now, catalogueRefreshBudget)
	tbl, err := models.Load(r.cache)
	if err != nil {
		in.ReadErr = "model table unreadable"
		return in
	}
	m, err := r.manifest()
	if err != nil {
		in.ReadErr = "role manifest unavailable"
		return in
	}
	in.Known = true
	report := modelsCheckReport(tbl, cfg, m, now)
	used := map[string]bool{}
	for _, row := range report.Profiles {
		used[row.Provider] = true
		switch row.Verdict {
		case "gone", "newer", "newer?":
			field, model := "model", row.Model
			if row.Tier != "" {
				field, model = "tier", row.Tier
			}
			in.Findings = append(in.Findings, health.CatalogueFinding{Profile: row.Profile, Field: field, Model: model, Verdict: row.Verdict, Candidate: row.Candidate})
		}
	}
	providers := make([]string, 0, len(used))
	for p := range used {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	for _, name := range providers {
		p := tbl.Providers[name]
		if p == nil {
			continue
		}
		if p.SourceAt > 0 && cfg.SourceMaxAge > 0 && now.Sub(time.UnixMilli(p.SourceAt)) > cfg.SourceMaxAge {
			hint, ok := sourceHints[name]
			if !ok { // an adapter's provider: the dispatcher never reads it
				hint = "agentbus models --refresh"
			}
			in.OldSources = append(in.OldSources, health.OldSource{Provider: name, Age: now.Sub(time.UnixMilli(p.SourceAt)), Hint: hint})
		}
		if !p.OK && p.At > 0 {
			since := p.ReadAt // the last good read; never one: the first attempt counts
			if since == 0 {
				since = p.At
			}
			in.FailedReads = append(in.FailedReads, health.FailedRead{Provider: name, Error: p.Error, For: now.Sub(time.UnixMilli(since))})
		}
	}
	for i := len(tbl.Events) - 1; i >= 0; i-- { // newest first
		e := tbl.Events[i]
		in.Events = append(in.Events, health.CatalogueEvent{At: time.UnixMilli(e.At), Provider: e.Provider, Kind: e.Kind, ID: e.ID, To: e.To})
	}
	return in
}

// refreshForClients refreshes, under the table's lock, the provider of each
// updated client (client -> its installed version) whose catalogue was not
// read since that version (§3.5: a new client brings a new catalogue). A
// held lock skips; a failed read is not recorded, so the next pass tries
// again. It returns the providers read.
func refreshForClients(ctx context.Context, path string, tiers models.TierMap, readers []models.Reader, updates map[string]string, now time.Time) ([]string, error) {
	unlock, held, err := lockTable(path)
	if err != nil || held {
		return nil, err
	}
	defer unlock()
	tbl, err := models.Load(path)
	if err != nil {
		return nil, err
	}
	want := map[string]string{} // provider -> version
	for client, version := range updates {
		if p := models.ProviderOfClient(client); p != "" && version != "" && tbl.Clients[p] != version {
			want[p] = version
		}
	}
	var pick []models.Reader
	for _, r := range readers {
		if _, ok := want[r.Provider]; ok {
			pick = append(pick, r)
		}
	}
	if len(pick) == 0 {
		return nil, nil
	}
	failed := tbl.Refresh(ctx, pick, tiers, now)
	var done []string
	for _, r := range pick {
		if _, bad := failed[r.Provider]; bad {
			continue
		}
		if tbl.Clients == nil {
			tbl.Clients = map[string]string{}
		}
		tbl.Clients[r.Provider] = want[r.Provider]
		done = append(done, r.Provider)
	}
	sort.Strings(done)
	return done, tbl.Save(path)
}

// recordAliases (§3.1, G5): each running Claude alias and the id its
// transcript shows it used become the table's alias record, a move an
// alias event. Under the table's lock; a held lock skips.
func recordAliases(path string, rows []doctorRow, now time.Time) error {
	unlock, held, err := lockTable(path)
	if err != nil || held {
		return err
	}
	defer unlock()
	tbl, err := models.Load(path)
	if err != nil {
		return err
	}
	// The newest fresh use per alias: an older transcript never moves an
	// alias back (two agents on opus, one not used since the move).
	newest := map[string]doctorRow{}
	for _, r := range rows {
		if !claudeAlias.MatchString(r.Configured) || r.Used == "" || r.usedAt == 0 || now.Sub(time.UnixMilli(r.usedAt)) > 24*time.Hour {
			continue
		}
		if cur, ok := newest[r.Configured]; !ok || r.usedAt > cur.usedAt {
			newest[r.Configured] = r
		}
	}
	aliases := make([]string, 0, len(newest))
	for a := range newest {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	changed := false
	for _, alias := range aliases {
		id := models.BaseID(newest[alias].Used)
		if cur, ok := tbl.Aliases["anthropic"][alias]; ok && cur.ID == id {
			continue
		}
		tbl.RecordAlias("anthropic", alias, id, now)
		changed = true
	}
	if !changed {
		return nil
	}
	return tbl.Save(path)
}
