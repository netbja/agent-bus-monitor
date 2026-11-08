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
	return catalogueReader{cache: models.CachePath(), config: models.ConfigPath(), readers: models.Readers(models.RealEnv()), now: time.Now,
		manifest: func() (*rolecfg.Manifest, error) {
			path, err := manifestPath(os.Environ())
			if err != nil {
				return nil, err
			}
			return rolecfg.Load(path)
		}}
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
			in.OldSources = append(in.OldSources, health.OldSource{Provider: name, Age: now.Sub(time.UnixMilli(p.SourceAt)), Hint: sourceHints[name]})
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
