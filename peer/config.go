// Package peer is a local model as a test peer on the bus (spec
// docs/superpowers/specs/2026-10-31-local-llm-peer-design.md): the launch
// spec and the endpoint it names, an OpenAI-compatible client, read-only
// tools, and the turn loop. It writes nothing to the repository and runs no
// command; agentbus peer (cmd/agentbus) wires it to the bus.
package peer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// Spec is the launcher's resolved launch.json for an adapter client: the
// profile's fields, the role, the manifest, the project and the role prompt.
type Spec struct {
	Model    string         `json:"model"`
	Client   string         `json:"client"`
	Provider string         `json:"provider"`
	Options  map[string]any `json:"options"`
	Profile  string         `json:"profile"`
	Role     string         `json:"role"`
	Manifest string         `json:"manifest"`
	Project  string         `json:"project"`
	Prompt   string         `json:"prompt"`
}

// LoadSpec reads the launch spec the launcher hands an adapter.
func LoadSpec(path string) (Spec, error) {
	var s Spec
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("launch spec: %w", err)
	}
	return s, nil
}

// Settings is what a turn needs: the endpoint and the turn's bounds.
type Settings struct {
	Endpoint    string // the [llm.<name>] table's name
	URL         string
	Model       string
	TurnTimeout time.Duration // options.turn_timeout, default 10 minutes
	MaxTokens   int           // options.max_tokens, default 1024
}

const (
	defaultTurnTimeout = 10 * time.Minute
	defaultMaxTokens   = 1024
)

// EndpointFP is a short fingerprint of an endpoint's URL: what the peer
// records of the URL it resolved, so health can tell that config.toml has
// changed it since, without the URL (its path) ever reaching the bus. Not a
// secret: the bus is no boundary against the local user, who reads
// config.toml anyway.
func EndpointFP(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:6])
}

// Resolve joins the spec with config.toml's endpoint it names. The profile's
// model must be the endpoint's: the one name is written twice but checked,
// and an error names both, never the URL.
func Resolve(spec Spec, cfg *config.File) (Settings, error) {
	name, _ := spec.Options["endpoint"].(string)
	if name == "" {
		return Settings{}, fmt.Errorf("the profile sets no options.endpoint (the [llm.<name>] table of config.toml)")
	}
	ep, ok := cfg.Endpoint(name)
	if !ok {
		return Settings{}, fmt.Errorf("config.toml has no valid [llm.%s] (see agentbus config)", name)
	}
	if spec.Model != ep.Model {
		return Settings{}, fmt.Errorf("the profile's model %q is not [llm.%s]'s model %q: write the same name in both", spec.Model, name, ep.Model)
	}
	s := Settings{Endpoint: name, URL: ep.URL, Model: ep.Model, TurnTimeout: defaultTurnTimeout, MaxTokens: defaultMaxTokens}
	if v, ok := spec.Options["turn_timeout"]; ok {
		raw, _ := v.(string)
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Settings{}, fmt.Errorf("options.turn_timeout must be a positive duration such as \"10m\"")
		}
		s.TurnTimeout = d
	}
	if v, ok := spec.Options["max_tokens"]; ok {
		n, ok := wholeNumber(v)
		if !ok || n <= 0 {
			return Settings{}, fmt.Errorf("options.max_tokens must be a positive whole number")
		}
		s.MaxTokens = n
	}
	return s, nil
}

// wholeNumber reads a JSON or TOML number that is whole.
func wholeNumber(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case int64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}
