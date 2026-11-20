package dispatchsvc

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/usage"
)

func usageRefresh(b *bus.Bus) func(context.Context, string) error {
	return func(ctx context.Context, token string) error {
		installed, err := b.ServicesInstalled(ctx)
		if err != nil || !installed {
			return err
		}
		agents, err := b.Agents(ctx)
		if err != nil {
			return err
		}
		rows, _ := usage.CollectAgentUsage(agents, usage.ProjectsRoot())
		names := make([]string, 0, len(rows))
		for name := range rows {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := b.FencedSetUsage(ctx, token, name, rows[name]); err != nil {
				return err
			}
		}
		return nil
	}
}

// modelsRefresh is the dispatcher's daily model refresh. G7 (model-detection
// §3.5): it no longer waits for the project's services, so a dispatcher run
// by hand refreshes too; the table is the machine's, not the project's.
func modelsRefresh() func(context.Context) error {
	return func(ctx context.Context) error { return dailyModelsRefresh(ctx) }
}

// serviceModelsGetenv reads models.source_max_age for the dispatch service's
// model chores, as modelsGetenv does for agentbus models (a test replaces
// both: isolateModelSettings).
var serviceModelsGetenv = os.Getenv

// dailyModelsRefresh is refreshModelsDaily (a test replaces it).
var dailyModelsRefresh = refreshModelsDaily

func refreshModelsDaily(ctx context.Context) error {
	cfg, err := models.LoadConfigWithSettings(models.ConfigPath(), serviceModelsGetenv)
	if err != nil {
		return err
	}
	// The dispatcher's: built-in readers only, never an adapter (repository code).
	return models.RefreshUnderLock(ctx, models.CachePath(), cfg.Tiers, dispatcherModelReaders(), time.Now())
}
