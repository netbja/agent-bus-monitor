package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func mustURL(t *testing.T, u string) *redis.Options {
	t.Helper()
	o, err := redis.ParseURL(u)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// Last used is per effective broker (project-switch note §2.5): -H vdr,
// REDIS_HOST=vdr and REDIS_URL=redis://vdr:6380 are one broker; another
// address, database or TLS is another; no username or password is written.
func TestLastUsedPerBroker(t *testing.T) {
	hostFlag := &redis.Options{Addr: "vdr:6380"} // as bus.Connect builds -H and REDIS_HOST
	url := mustURL(t, "redis://vdr:6380")
	if brokerKey(hostFlag) != brokerKey(url) || brokerKey(url) != "tcp vdr:6380 db0" {
		t.Fatalf("one broker, two keys: %q %q", brokerKey(hostFlag), brokerKey(url))
	}
	for _, other := range []string{"redis://vdr:6380/2", "rediss://vdr:6380", "redis://other:6380", "unix:///run/redis.sock"} {
		if brokerKey(mustURL(t, other)) == brokerKey(url) {
			t.Errorf("%s reads as the same broker", other)
		}
	}
	path := filepath.Join(t.TempDir(), "last-project")
	secret := mustURL(t, "redis://user:s3cret@vdr:6380/2")
	for broker, project := range map[*redis.Options]string{url: "alpha", hostFlag: "beta", secret: "gamma"} {
		if err := writeLastUsed(path, brokerKey(broker), project); err != nil {
			t.Fatal(err)
		}
	}
	if got := readLastUsed(path, brokerKey(url)); got != "beta" { // -H and the URL share a line
		t.Fatalf("last used %q", got)
	}
	if got := readLastUsed(path, brokerKey(secret)); got != "gamma" {
		t.Fatalf("db2's last used %q", got)
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "\n") != 2 || strings.Contains(string(b), "s3cret") || strings.Contains(string(b), "user") {
		t.Fatalf("file:\n%s", b)
	}
	if got := readLastUsed(filepath.Join(t.TempDir(), "none"), brokerKey(url)); got != "" {
		t.Fatalf("no file: %q", got)
	}
}

// The start order (project-switch note §2.5): --project, then
// AGENT_BUS_PROJECT, then last used; a last used with no keys on this broker
// any more (or whose keys cannot be read) is skipped.
func TestStartProject(t *testing.T) {
	keys := map[string]bool{"alpha": true}
	has := func(p string) (bool, error) {
		if p == "broken" {
			return false, os.ErrPermission
		}
		return keys[p], nil
	}
	for name, c := range map[string]struct{ flag, env, last, want string }{
		"flag first":           {"f", "e", "alpha", "f"},
		"env next":             {"", "e", "alpha", "e"},
		"last used":            {"", "", "alpha", "alpha"},
		"last used gone":       {"", "", "gone", ""},
		"last used unreadable": {"", "", "broken", ""},
		"nothing":              {"", "", "", ""},
	} {
		if got := startProject(c.flag, c.env, c.last, has); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
