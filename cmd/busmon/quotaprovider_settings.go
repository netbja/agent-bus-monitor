package main

import (
	"fmt"

	"github.com/netbja/agent-bus-monitor/config"
)

// quota-per-provider (the person, 2026-11-25): the page's Services group
// shows the quota defaults and each provider's own warn, drain and hold,
// the person's to set; the dispatcher reads them each minute (AppliesLive:
// the save message says so).
func init() {
	settingLabels["dispatch.quota_warn"] = "Quota: warn at (every provider)"
	settingLabels["dispatch.quota_drain"] = "Quota: drain the run at (every provider)"
	settingLabels["dispatch.quota_hold"] = "Quota: hold at (every provider)"
	settingHelps["dispatch.quota_warn"] = "percent of a provider's window: master warned, agents with work told to finish their step; a provider's own value below overrides it"
	settingHelps["dispatch.quota_drain"] = "percent of master's provider's window: the run drains, nothing new starts; unset, it follows the warning"
	settingHelps["dispatch.quota_hold"] = "percent of a provider's window: that provider's agents are held until it resets, the others run; master's held pauses the project"
	words := map[string]string{"warn": "warn at", "drain": "drain the run at", "hold": "hold at"}
	for _, p := range config.QuotaProviders {
		for _, level := range config.QuotaLevelNames {
			key := config.QuotaProviderKey(p, level)
			servicesOnPage[key] = true
			settingLabels[key] = fmt.Sprintf("Quota %s: %s", p, words[level])
			settingHelps[key] = fmt.Sprintf("%s's own %s threshold, a percent of its window; 0 uses the default (dispatch.quota_%s). "+
				"warn <= drain <= hold once resolved, else %s uses the defaults; per project in config.toml [project.<name>.quota.%s]; agents cannot change it",
				p, level, level, p, p)
		}
	}
	for _, k := range []string{"dispatch.quota_warn", "dispatch.quota_drain", "dispatch.quota_hold"} {
		servicesOnPage[k] = true
	}
}
