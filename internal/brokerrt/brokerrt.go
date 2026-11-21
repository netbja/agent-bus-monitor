// Package brokerrt names the broker's container runtime (broker-hardening
// spec §3 step 3): docker or podman, from the broker.runtime setting. Every
// caller of the broker's container (the installer, the health checks,
// busmon) names its binary and its compose command through here, and
// nowhere else (TestNoRuntimeLiteral).
package brokerrt

import "github.com/netbja/agent-bus-monitor/config"

// Runtime is a container runtime that runs the broker.
type Runtime string

const (
	Docker Runtime = "docker"
	Podman Runtime = "podman"
)

// Of is broker.runtime as configured (env, then config.toml), docker by
// default.
func Of(getenv func(string) string) Runtime {
	if Runtime(config.Machine(getenv).String("broker.runtime")) == Podman {
		return Podman
	}
	return Docker
}

// Bin is the runtime's command.
func (r Runtime) Bin() string {
	if r == Podman {
		return string(Podman)
	}
	return string(Docker)
}

// Compose is the runtime's compose command, its plugin form: "docker
// compose" or "podman compose".
func (r Runtime) Compose() string { return r.Bin() + " compose" }

// Standalone is the standalone compose command the plugin's absence falls
// back to: docker-compose or podman-compose.
func (r Runtime) Standalone() string { return r.Bin() + "-compose" }
