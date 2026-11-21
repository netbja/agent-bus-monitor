package brokerrt

import "testing"

// broker.runtime picks the binary and the compose commands; unset, docker.
func TestRuntimeOf(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			switch k {
			case "AGENT_BUS_BROKER_RUNTIME":
				return v
			case "XDG_CONFIG_HOME":
				return t.TempDir() // no config.toml
			}
			return ""
		}
	}
	for v, want := range map[string][3]string{
		"":       {"docker", "docker compose", "docker-compose"},
		"docker": {"docker", "docker compose", "docker-compose"},
		"podman": {"podman", "podman compose", "podman-compose"},
		"lxc":    {"docker", "docker compose", "docker-compose"}, // not a runtime: skipped, the default
	} {
		r := Of(env(v))
		if got := [3]string{r.Bin(), r.Compose(), r.Standalone()}; got != want {
			t.Errorf("%q: %v, want %v", v, got, want)
		}
	}
}
