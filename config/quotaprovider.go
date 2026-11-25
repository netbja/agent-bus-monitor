package config

import (
	"slices"
	"sort"
	"strings"
	"sync"
)

// quota-per-provider (the person, 2026-11-25): the quota thresholds have a
// default for every provider (dispatch.quota_warn, _drain, _hold) and an
// optional value per provider, in config.toml's [quota.<provider>] table and
// per project in [project.<p>.quota.<provider>]: one provider reaching its
// own hold holds only its agents. The person's alone, like board.wip_limit:
// busmon's Shift+C or config.toml, no env variable; 0 is the default's.
// Any valid provider id is taken (master's directive: the person uses many
// providers); one without a usage reader is stored and shown, and inert.

// QuotaProviders are the providers whose keys are registry rows, shown on
// Shift+C even when unset: the ones the model table and the built-in quota
// readers know (models/readers.go, quota/). Any other valid id has its keys
// through Lookup.
var QuotaProviders = []string{"anthropic", "google", "moonshot", "openai", "xai"}

// QuotaLevelNames are a provider's three thresholds, in their order.
var QuotaLevelNames = []string{"warn", "drain", "hold"}

// QuotaProviderKey is provider's level key: quota.<provider>.<level>.
func QuotaProviderKey(provider, level string) string { return "quota." + provider + "." + level }

// ParseQuotaProviderKey splits quota.<provider>.<level>: a valid provider id
// (a bus name) and one of QuotaLevelNames.
func ParseQuotaProviderKey(name string) (provider, level string, ok bool) {
	rest, ok := strings.CutPrefix(name, "quota.")
	if !ok {
		return "", "", false
	}
	provider, level, ok = strings.Cut(rest, ".")
	if !ok || !validName(provider) || !slices.Contains(QuotaLevelNames, level) {
		return "", "", false
	}
	return provider, level, true
}

func newQuotaProviderKey(provider, level string) *Key {
	return &Key{Name: QuotaProviderKey(provider, level), Kind: Float, Default: 0.0, ReadBy: "dispatch, agentbus quota",
		Project: true, PersonEdit: true, file: filePercent}
}

func quotaProviderKeys() []*Key {
	var out []*Key
	for _, p := range QuotaProviders {
		for _, level := range QuotaLevelNames {
			out = append(out, newQuotaProviderKey(p, level))
		}
	}
	return out
}

// quotaKeys are the rows Lookup made for providers off the registry, one per
// name, so a row is the same pointer at every call.
var quotaKeys sync.Map

// lookupQuotaProvider is the row of quota.<provider>.<level> for a provider
// off the registry.
func lookupQuotaProvider(name string) (*Key, bool) {
	p, level, ok := ParseQuotaProviderKey(name)
	if !ok {
		return nil, false
	}
	k, _ := quotaKeys.LoadOrStore(name, newQuotaProviderKey(p, level))
	return k.(*Key), true
}

// QuotaProviders are the providers f sets a threshold for, machine or
// project, sorted.
func (f *File) QuotaProviders() []string {
	if f == nil {
		return nil
	}
	seen := map[string]bool{}
	add := func(name string) {
		if p, _, ok := ParseQuotaProviderKey(name); ok {
			seen[p] = true
		}
	}
	for name := range f.values {
		add(name)
	}
	for _, keys := range f.projects {
		for name := range keys {
			add(name)
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
