package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/quota"
)

// quota-per-provider (the person, 2026-11-25): the page's Services group
// shows the quota defaults and each provider's own warn, drain and hold,
// the person's to set; the dispatcher reads them each minute (AppliesLive:
// the save message says so). The five with a built-in reader always; any
// other provider config.toml sets a threshold for, too, labelled when no
// usage reader reads it (stored, inert).
func init() {
	settingLabels["dispatch.quota_warn"] = "Quota: warn at (every provider)"
	settingLabels["dispatch.quota_drain"] = "Quota: drain the run at (every provider)"
	settingLabels["dispatch.quota_hold"] = "Quota: hold at (every provider)"
	settingHelps["dispatch.quota_warn"] = "percent of a provider's window: master warned, agents with work told to finish their step; a provider's own value below overrides it"
	settingHelps["dispatch.quota_drain"] = "percent of master's provider's window: the run drains, nothing new starts; unset, it follows the warning"
	settingHelps["dispatch.quota_hold"] = "percent of a provider's window: that provider's agents are held until it resets, the others run; master's held pauses the project"
	for _, k := range []string{"dispatch.quota_warn", "dispatch.quota_drain", "dispatch.quota_hold"} {
		servicesOnPage[k] = true
	}
}

var quotaLevelWords = map[string]string{"warn": "warn at", "drain": "drain the run at", "hold": "hold at"}

// settingKeys are the page's keys: the registry's, and the quota keys of
// each provider off the five that cfg sets a threshold for.
func settingKeys(cfg *config.File) []*config.Key {
	keys := config.Keys
	for _, p := range cfg.QuotaProviders() {
		if slices.Contains(config.QuotaProviders, p) {
			continue
		}
		for _, level := range config.QuotaLevelNames {
			if k, ok := config.Lookup(config.QuotaProviderKey(p, level)); ok {
				keys = append(slices.Clip(keys), k)
			}
		}
	}
	return keys
}

// quotaReaders are the providers a usage reader reads: the built-in ones
// found here and quota.toml's (getenv's config home), kind off excepted.
func quotaReaders(getenv func(string) string) map[string]bool {
	path := quota.ConfigPath()
	if d := getenv("XDG_CONFIG_HOME"); d != "" {
		path = filepath.Join(d, "agentbus", "quota.toml")
	}
	cfg, _ := quota.LoadConfig(path, exec.LookPath) // unreadable: the built-ins
	out := make(map[string]bool, len(cfg))
	for p := range cfg {
		out[p] = true
	}
	return out
}

// quotaSettingLabel is a provider quota key's label, "" for another key.
func quotaSettingLabel(name string, readers map[string]bool) string {
	p, level, ok := config.ParseQuotaProviderKey(name)
	if !ok {
		return ""
	}
	label := fmt.Sprintf("Quota %s: %s", p, quotaLevelWords[level])
	if !readers[p] {
		label += " (no usage reader)"
	}
	return label
}

// quotaSettingHelp is a provider quota key's help line, "" for another key.
func quotaSettingHelp(name string) string {
	p, level, ok := config.ParseQuotaProviderKey(name)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s's own %s threshold, a percent of its window; 0 uses the default (dispatch.quota_%s). "+
		"warn <= drain <= hold once resolved, else %s uses the defaults; per project in config.toml [project.<name>.quota.%s]; "+
		"without a usage reader it is kept and does nothing; agents cannot change it", p, level, level, p, p)
}
