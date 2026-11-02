// Package peer is a local model as a test peer on the bus (spec
// docs/superpowers/specs/2026-10-31-local-llm-peer-design.md): the launch
// spec and the endpoint it names, an OpenAI-compatible client, read-only
// tools, and the turn loop. It writes nothing to the repository and runs no
// command; agentbus peer (cmd/agentbus) wires it to the bus.
package peer

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// changed it since. It is an HMAC under key, a host-local secret (FPKey): a
// bus reader holding the snapshot cannot test guessed URLs (a private path)
// against it offline (reviewer-codex). The peer and the health check run on
// the same host, so they share the key.
func EndpointFP(key []byte, url string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(url))
	return hex.EncodeToString(m.Sum(nil)[:8])
}

// fpKeyFile is EndpointFP's key, beside config.toml: in the configuration
// directory, not a cache, since a new key would make every running peer read
// as changed.
const fpKeyFile = "endpoint-fp.key"

// FPKey reads the host's fingerprint key from dir, creating it once: 32
// random bytes written whole to a temporary file (0600, in a 0700
// directory), then linked to its name, which fails if the name exists. The
// key is never visible half-written, and of two first callers one links, the
// other reads the winner's (reviewer-codex, foureyes). A file of another
// length (truncated, emptied by hand) is replaced by a new key, renamed over
// it, and note says so (foureyes, master): refused, it would stop every
// fingerprint for good. Peers that recorded the old one then read as
// changed until they restart.
func FPKey(dir string) (key []byte, note string, err error) {
	path := filepath.Join(dir, fpKeyFile)
	old, err := os.ReadFile(path)
	switch {
	case err == nil && len(old) == 32:
		return old, "", nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return nil, "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", err
	}
	tmp, err := os.CreateTemp(dir, fpKeyFile+".*")
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return nil, "", err
	}
	if err := tmp.Close(); err != nil {
		return nil, "", err
	}
	if old != nil { // there, of the wrong length: replaced whole
		if err := os.Rename(tmp.Name(), path); err != nil {
			return nil, "", err
		}
		return key, fmt.Sprintf("%s held %d bytes, not a key: replaced", path, len(old)), nil
	}
	if err := os.Link(tmp.Name(), path); errors.Is(err, os.ErrExist) {
		other, err := os.ReadFile(path)
		if err != nil || len(other) != 32 {
			return nil, "", fmt.Errorf("%s is not a fingerprint key", path)
		}
		return other, "", nil
	} else if err != nil {
		return nil, "", err
	}
	return key, "", nil
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
