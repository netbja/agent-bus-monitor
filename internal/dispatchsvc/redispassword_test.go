package dispatchsvc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/health"
)

// broker-hardening step 3: redis-password reads the broker's published
// ports with the configured runtime: docker filters on the port, podman
// (which has no publish filter) on the container's name; the same reading
// of what it prints.
func TestRedisPasswordCheckRuntime(t *testing.T) {
	for _, c := range []struct {
		runtime, want string
	}{
		{"", "docker ps --filter publish=6380 --format {{.Ports}}"},
		{"podman", "podman ps --filter name=^agent-bus-redis$ --format {{.Ports}}"},
	} {
		cfg := t.TempDir()
		env := map[string]string{"AGENT_BUS_BROKER_RUNTIME": c.runtime, "XDG_CONFIG_HOME": cfg, "REDIS_PASSWORD": "a-real-one"}
		getenv := func(k string) string { return env[k] }
		var ran string
		ports := "127.0.0.1:6380->6379/tcp"
		out := func(_ context.Context, name string, args ...string) (string, error) {
			ran = strings.Join(append([]string{name}, args...), " ")
			return ports + "\n", nil
		}
		check := redisPasswordCheck(getenv, out)
		if s, d, err := check(context.Background()); s != health.OK || err != nil || ran != c.want {
			t.Errorf("%q: %s %q %v, ran %q", c.runtime, s, d, err, ran)
		}
		ports = "0.0.0.0:6380->6379/tcp"
		if s, _, _ := check(context.Background()); s != health.Fail {
			t.Errorf("%q: beyond loopback read %s", c.runtime, s)
		}
		failing := func(context.Context, string, ...string) (string, error) { return "", errors.New("exit 125") }
		if s, d, err := redisPasswordCheck(getenv, failing)(context.Background()); s != health.Unknown || err == nil || !strings.Contains(d, "("+strings.Fields(c.want)[0]+")") {
			t.Errorf("%q: a failed read %s %q %v", c.runtime, s, d, err)
		}
	}
}
