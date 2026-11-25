package config

// quota-per-provider (the person, 2026-11-25): the quota thresholds have a
// default for every provider (dispatch.quota_warn, _drain, _hold) and an
// optional value per provider, in config.toml's [quota.<provider>] table and
// per project in [project.<p>.quota.<provider>]: one provider reaching its
// own hold holds only its agents. The person's alone, like board.wip_limit:
// busmon's Shift+C or config.toml, no env variable; 0 is the default's.

// QuotaProviders are the providers a value may be set for: the ones the
// model table and the quota readers know (models/readers.go, quota/).
var QuotaProviders = []string{"anthropic", "google", "moonshot", "openai", "xai"}

// QuotaLevelNames are a provider's three thresholds, in their order.
var QuotaLevelNames = []string{"warn", "drain", "hold"}

// QuotaProviderKey is provider's level key: quota.<provider>.<level>.
func QuotaProviderKey(provider, level string) string { return "quota." + provider + "." + level }

func quotaProviderKeys() []*Key {
	var out []*Key
	for _, p := range QuotaProviders {
		for _, level := range QuotaLevelNames {
			out = append(out, &Key{Name: QuotaProviderKey(p, level), Kind: Float, Default: 0.0, ReadBy: "dispatch, agentbus quota",
				Project: true, PersonEdit: true, file: filePercent})
		}
	}
	return out
}

func init() {
	for _, p := range QuotaProviders {
		for _, level := range QuotaLevelNames {
			groups[QuotaProviderKey(p, level)] = GroupServices
			applies[QuotaProviderKey(p, level)] = AppliesLive // read with the thresholds, each pass
		}
	}
}
