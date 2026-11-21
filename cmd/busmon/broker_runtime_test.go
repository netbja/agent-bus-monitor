package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/brokerrt"
)

// broker-hardening step 3: with broker.runtime=podman the Redis page asks
// podman for the container's port and log tail, and says podman when it
// has no runner; the Services card inspects it with podman and names it.
func TestBusmonBrokerUnderPodman(t *testing.T) {
	cfg := t.TempDir()
	env := map[string]string{"REDIS_HOST": "127.0.0.1", "AGENT_BUS_BROKER_RUNTIME": "podman", "XDG_CONFIG_HOME": cfg}
	getenv := func(k string) string { return env[k] }
	var calls []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch args[0] {
		case "port":
			return "127.0.0.1:6380", nil
		case "logs":
			return "line\n", nil
		case "inspect":
			return "docker.io/library/redis:8-alpine|running", nil
		}
		return "", errors.New("unexpected")
	}
	f := gatherRedisPage(context.Background(), detailReader{d: redisFixture()}, run, getenv, "")
	if f.err != "" || !f.loopback || strings.Join(calls, ";") != "podman port agent-bus-redis 6379/tcp;podman logs --tail 20 agent-bus-redis" {
		t.Fatalf("facts %+v, calls %q", f, calls)
	}
	if f := gatherRedisPage(context.Background(), detailReader{d: redisFixture()}, nil, getenv, ""); f.bindErr != "no podman" {
		t.Fatalf("no runner: %q", f.bindErr)
	}
	calls = nil
	recs := map[string]bus.ServiceRecord{}
	r := &fakeRedisInfo{fakeReader: &fakeReader{calls: map[string]int{}}, info: bus.RedisInfo{Version: "8.2.1"}}
	s := gatherServices(context.Background(), r, "p", nil, recs, "here", run, nil, brokerrt.Podman)
	if s.redis.where != "podman agent-bus-redis (docker.io/library/redis:8-alpine), running" || len(calls) != 1 || !strings.HasPrefix(calls[0], "podman inspect") {
		t.Fatalf("card %q, calls %q", s.redis.where, calls)
	}
}
