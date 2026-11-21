package setup

import (
	"os"
	"strings"
	"testing"
)

// broker-hardening step 1: the broker's config on stdin states its
// durability, AOF fsynced every second and the RDB schedule, rather than
// leaning on Redis's defaults.
func TestComposeStatesDurability(t *testing.T) {
	raw, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	start := strings.Index(s, "<<EOF\n")
	end := strings.Index(s[max(start, 0):], "\n        EOF")
	if start < 0 || end < 0 {
		t.Fatalf("no config on stdin in the compose file:\n%s", s)
	}
	conf := map[string]bool{}
	for _, l := range strings.Split(s[start+len("<<EOF\n"):start+end], "\n") {
		conf[strings.TrimSpace(l)] = true
	}
	for _, want := range []string{"appendonly yes", "appendfsync everysec", "save 3600 1 300 100 60 10000"} {
		if !conf[want] {
			t.Errorf("the config on stdin lacks %q: %v", want, conf)
		}
	}
	if strings.Contains(s, "--appendonly") {
		t.Error("appendonly is still on the argv: one place states the persistence")
	}
}
