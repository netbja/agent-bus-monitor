package models

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/BurntSushi/toml"
)

// ConfigPath is ~/.config/agentbus/models.toml: a person's tier rules and
// family orders, for what no source says (§10.2, §12.1).
func ConfigPath() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "agentbus", "models.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agentbus", "models.toml")
}

// Config is models.toml.
//
//	[[tier]]
//	match = "^grok-.*-build-fast$"   # a regexp on the model id
//	tier = "fast"
//
//	[families.grok]
//	order = ["grok-4.7", "grok-4.6"]  # newest first: the evidence for "newer"
type Config struct {
	Tiers    TierMap
	Families map[string][]string
}

// LoadConfig reads models.toml; a missing file is the built-in rules only.
func LoadConfig(path string) (Config, error) {
	cfg := Config{Tiers: DefaultTiers(), Families: map[string][]string{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	var raw struct {
		Tier []struct {
			Match string `toml:"match"`
			Tier  string `toml:"tier"`
		} `toml:"tier"`
		Families map[string]struct {
			Order []string `toml:"order"`
		} `toml:"families"`
	}
	if _, err := toml.Decode(string(b), &raw); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	var mine TierMap
	for i, r := range raw.Tier {
		re, err := regexp.Compile(r.Match)
		if err != nil || !ValidTier(r.Tier) {
			return cfg, fmt.Errorf("%s: tier rule %d: a valid regexp and a tier (frontier, workhorse, fast, legacy)", path, i+1)
		}
		mine = append(mine, TierRule{re, r.Tier})
	}
	cfg.Tiers = append(mine, cfg.Tiers...) // the person's rules first
	for name, f := range raw.Families {
		cfg.Families[name] = f.Order
	}
	return cfg, nil
}

// NewerByOrder is the newest id of a person's family order that is newer
// than id, or "" (no evidence).
func (c Config) NewerByOrder(id string) string {
	for _, order := range c.Families {
		for i, o := range order {
			if BaseID(o) == BaseID(id) && i > 0 {
				return order[0]
			}
		}
	}
	return ""
}
