package peer

import (
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/netbja/agent-bus-monitor/config"
)

// EndpointFPKey is the host's endpoint fingerprint key, beside config.toml;
// with no configuration directory there is none (never one in the cwd).
func EndpointFPKey(getenv func(string) string) ([]byte, string, error) {
	p := config.Path(getenv)
	if p == "" {
		return nil, "", fmt.Errorf("no configuration directory")
	}
	return FPKey(filepath.Dir(p))
}

// EndpointHost is an endpoint's host and port, never its path: what agentbus
// config shows of it.
func EndpointHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "?"
	}
	return u.Host
}
