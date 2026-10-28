package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

func redisFixture() bus.RedisDetail {
	return bus.RedisDetail{
		Info:       bus.RedisInfo{Version: "8.0.2", UptimeSecs: 7200, UsedMemory: "1.5M", Keys: 42, PID: 1},
		PeakMemory: "2.0M", MaxMemory: "0B", MaxMemoryPolicy: "noeviction",
		RDBLastSave: capNow.Add(-5 * time.Minute).Unix(), RDBStatus: "ok", AOFEnabled: false, AOFStatus: "ok", Clients: 7,
		Projects: []bus.ProjectRedis{{Project: "demo", Keys: 30, Streams: map[string]int64{"status": 12, "report": 3, "notify": 1, "cmd": 40, "verdicts": 2}}},
	}
}

// busmon-redis-detail: the page states every fact, and an unknown reads ?.
func TestRedisPageBody(t *testing.T) {
	f := redisPage{detail: redisFixture(), bind: "127.0.0.1:6380", loopback: true, legacyPW: true,
		logs: []string{"1:M 28 Oct 2026 20:00:00.000 * Ready to accept connections tcp"}}
	body := plainOf(redisPageBody(f, capNow, 100))
	for _, want := range []string{"8.0.2", "up 2h", "1.5M", "peak 2.0M", "no limit", "noeviction", "last RDB save 5m ago (ok)", "AOF off",
		"7 clients", "42 keys", "demo", "30 keys", "status 12", "cmd 40", "verdicts 2",
		"127.0.0.1:6380", "loopback only", "default password", "Ready to accept connections"} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
	unknown := plainOf(redisPageBody(redisPage{err: "dial tcp: refused", bindErr: "no docker", logsErr: "no docker"}, capNow, 100))
	for _, want := range []string{"dial tcp: refused", "bind ?", "logs ?"} {
		if !strings.Contains(unknown, want) {
			t.Errorf("unknown page lacks %q:\n%s", want, unknown)
		}
	}
	if strings.Contains(unknown, " 0 keys") || strings.Contains(unknown, "loopback only") {
		t.Errorf("an unread page claims facts:\n%s", unknown)
	}
	exposed := plainOf(redisPageBody(redisPage{detail: redisFixture(), bind: "0.0.0.0:6380"}, capNow, 100))
	if !strings.Contains(exposed, "beyond loopback") || strings.Contains(exposed, "loopback only") {
		t.Errorf("an exposed port reads:\n%s", exposed)
	}
}

type detailReader struct {
	d   bus.RedisDetail
	err error
}

func (r detailReader) RedisDetail(context.Context) (bus.RedisDetail, error) { return r.d, r.err }

// The page reads docker only for its own container, bounded, and never
// writes: the port, then the log tail.
func TestGatherRedisPage(t *testing.T) {
	var calls []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch args[0] {
		case "port":
			return "127.0.0.1:6380", nil
		case "logs":
			return strings.Repeat("line\n", 50), nil
		}
		return "", errors.New("unexpected")
	}
	f := gatherRedisPage(context.Background(), detailReader{d: redisFixture()}, run, func(string) string { return "" }, "")
	if f.err != "" || !f.loopback || f.bind != "127.0.0.1:6380" || len(f.logs) > redisLogLines || len(f.logs) == 0 {
		t.Fatalf("facts %+v", f)
	}
	want := []string{"docker port agent-bus-redis 6379/tcp", "docker logs --tail 20 agent-bus-redis"}
	if strings.Join(calls, ";") != strings.Join(want, ";") {
		t.Fatalf("docker calls %q", calls)
	}
	f = gatherRedisPage(context.Background(), detailReader{err: errors.New("refused")}, nil, func(string) string { return "" }, "")
	if f.err != "refused" || f.bindErr == "" || f.logsErr == "" {
		t.Fatalf("no docker, no broker: %+v", f)
	}
}

// The Redis card is selectable; Enter on it opens the page.
func TestShellServicesRedisCardOpensItsPage(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	if m.cfg.docker != nil {
		t.Fatal("the test shell has a docker runner: it could read this host's container")
	}
	next, cmd := m.Update(keyMsg(tea.Key{Code: tea.KeyF3}))
	m = run(t, next.(Model), cmd)
	m = keys(t, m, kDown, kDown)
	if m.rd.top().sel != len(svcCardKinds) {
		t.Fatalf("down twice: card %d, want the Redis card", m.rd.top().sel)
	}
	next, cmd = m.Update(keyMsg(kEnter))
	m = run(t, next.(Model), cmd)
	if m.rd.top().kind != ovRedis || !strings.Contains(plainOf(m.rd.top().body), "redis") {
		t.Fatalf("enter on Redis opened %v: %s", m.rd.top().kind, plainOf(m.rd.top().body))
	}
	if !strings.Contains(plainOf(m.rd.top().body), "bind ?") {
		t.Errorf("with no docker runner the page claims a bind: %s", plainOf(m.rd.top().body))
	}
	if m = keys(t, m, kEsc); m.rd.top().kind != ovServices {
		t.Error("esc did not return to the cards")
	}
}

// The container's facts are the broker's only when busmon reads that
// container: another endpoint (a remote host, another port, a unix socket)
// leaves bind and logs unknown, and the logs are not even read.
func TestGatherRedisPageOnlyForTheBrokerItReads(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"remote url":  {"REDIS_URL": "redis://:pw@far.example:6380/0"},
		"other port":  {"REDIS_PORT": "6390"},
		"unix socket": {"REDIS_URL": "unix:///tmp/x/redis.sock"},
		"remote host": {"REDIS_HOST": "10.0.0.5"},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			run := func(_ context.Context, name string, args ...string) (string, error) {
				calls = append(calls, args[0])
				return "127.0.0.1:6380", nil
			}
			f := gatherRedisPage(context.Background(), detailReader{d: redisFixture()}, run, func(k string) string { return env[k] }, "")
			if f.loopback || f.bind != "" || !strings.Contains(f.bindErr, "not this host's") || f.logsErr == "" {
				t.Fatalf("facts %+v", f)
			}
			if strings.Join(calls, ",") != "port" {
				t.Fatalf("docker calls %q: the logs of another broker were read", calls)
			}
		})
	}
	// The address and its family must be the one published, not just the port.
	for name, c := range map[string]struct {
		env  map[string]string
		pub  string
		want bool
	}{
		"ipv4 endpoint, ipv6-only publish":        {map[string]string{"REDIS_URL": "redis://127.0.0.1:6380/0"}, "[::1]:6380", false},
		"ipv6 endpoint, ipv4-only publish":        {map[string]string{"REDIS_URL": "redis://[::1]:6380/0"}, "127.0.0.1:6380", false},
		"loopback endpoint, an interface publish": {map[string]string{"REDIS_HOST": "127.0.0.1"}, "192.168.1.5:6380", false},
		"an interface endpoint":                   {map[string]string{"REDIS_HOST": "192.168.1.5"}, "192.168.1.5:6380", false},
		"ipv4 endpoint, ipv4 wildcard":            {map[string]string{"REDIS_HOST": "127.0.0.1"}, "0.0.0.0:6380", true},
		"ipv6 endpoint, ipv6 loopback":            {map[string]string{"REDIS_URL": "redis://[::1]:6380/0"}, "[::1]:6380", true},
		"localhost, ipv4 loopback (default)":      {nil, "127.0.0.1:6380", true},
	} {
		run := func(context.Context, string, ...string) (string, error) { return c.pub, nil }
		f := gatherRedisPage(context.Background(), detailReader{d: redisFixture()}, run, func(k string) string { return c.env[k] }, "")
		if got := f.bindErr == ""; got != c.want {
			t.Errorf("%s: established %v, want %v (%+v)", name, got, c.want, f)
		}
		if !c.want && (f.loopback || f.logs != nil) {
			t.Errorf("%s: claims %+v", name, f)
		}
	}
	// --host overrides REDIS_HOST, as bus.Connect does.
	run := func(context.Context, string, ...string) (string, error) { return "127.0.0.1:6380", nil }
	if f := gatherRedisPage(context.Background(), detailReader{d: redisFixture()}, run, func(k string) string { return map[string]string{"REDIS_HOST": "far"}[k] }, "localhost"); !f.loopback {
		t.Fatalf("--host localhost: %+v", f)
	}
}

// A docker read keeps only the end of its output, whatever it prints.
func TestDockerReadIsBounded(t *testing.T) {
	tb := &tailBuffer{max: 8}
	for _, w := range []string{"abc", "defghij", "klmnopqrstuvwxyz", "12"} {
		tb.Write([]byte(w))
	}
	if string(tb.buf) != "uvwxyz12" {
		t.Fatalf("tail %q", tb.buf)
	}
	out, err := realSvcRunner(context.Background(), "sh", "-c", "i=0; while [ $i -lt 2000 ]; do printf 'line %04d of a long log\\n' $i; i=$((i+1)); done")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > svcOutMax || !strings.HasSuffix(out, "line 1999 of a long log") {
		t.Fatalf("%d bytes kept (max %d), ends %q", len(out), svcOutMax, out[max(0, len(out)-30):])
	}
}
