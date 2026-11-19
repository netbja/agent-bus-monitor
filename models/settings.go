package models

import (
	"os"

	"github.com/netbja/agent-bus-monitor/config"
)

// SettingsGetenv reads models.source_max_age (a test replaces it).
var SettingsGetenv = os.Getenv

// LoadConfigWithSettings is models.toml with models.source_max_age from the
// settings (config.toml or its env).
func LoadConfigWithSettings(path string) (Config, error) {
	cfg, err := LoadConfig(path)
	cfg.SourceMaxAge = config.Machine(SettingsGetenv).Duration("models.source_max_age")
	return cfg, err
}
