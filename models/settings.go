package models

import "github.com/netbja/agent-bus-monitor/config"

// LoadConfigWithSettings is models.toml with models.source_max_age from the
// settings (config.toml or its env), read through getenv.
func LoadConfigWithSettings(path string, getenv func(string) string) (Config, error) {
	cfg, err := LoadConfig(path)
	cfg.SourceMaxAge = config.Machine(getenv).Duration("models.source_max_age")
	return cfg, err
}
