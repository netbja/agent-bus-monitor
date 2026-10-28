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
	f := gatherRedisPage(context.Background(), detailReader{d: redisFixture()}, run, func(string) string { return "" })
	if f.err != "" || !f.loopback || f.bind != "127.0.0.1:6380" || len(f.logs) > redisLogLines || len(f.logs) == 0 {
		t.Fatalf("facts %+v", f)
	}
	want := []string{"docker port agent-bus-redis 6379/tcp", "docker logs --tail 20 agent-bus-redis"}
	if strings.Join(calls, ";") != strings.Join(want, ";") {
		t.Fatalf("docker calls %q", calls)
	}
	f = gatherRedisPage(context.Background(), detailReader{err: errors.New("refused")}, nil, func(string) string { return "" })
	if f.err != "refused" || f.bindErr == "" || f.logsErr == "" {
		t.Fatalf("no docker, no broker: %+v", f)
	}
}

// The Redis card is selectable; Enter on it opens the page.
func TestShellServicesRedisCardOpensItsPage(t *testing.T) {
	m := fixtureShell(t, 140, 40)
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
	if m = keys(t, m, kEsc); m.rd.top().kind != ovServices {
		t.Error("esc did not return to the cards")
	}
}
