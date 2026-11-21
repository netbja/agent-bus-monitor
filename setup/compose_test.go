package setup

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

// composeShell is one of the compose file's shell scripts, runnable here:
// compose's $$ unescaped and /data moved to data.
func composeShell(t *testing.T, script, data string) string {
	t.Helper()
	return strings.ReplaceAll(strings.ReplaceAll(script, "$$", "$"), "/data/", data+"/")
}

// composeScripts reads the broker's command script and its healthcheck.
func composeScripts(t *testing.T) (command, health string) {
	t.Helper()
	raw, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	_, rest, ok := strings.Cut(s, "      - |\n")
	body, _, ok2 := strings.Cut(rest, "    ports:")
	if !ok || !ok2 {
		t.Fatal("no command script in the compose file")
	}
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		lines = append(lines, strings.TrimPrefix(l, "        "))
	}
	_, hc, ok := strings.Cut(s, "      test: ")
	hc, _, _ = strings.Cut(hc, "\n")
	var test []string
	if !ok || json.Unmarshal([]byte(hc), &test) != nil || len(test) != 2 || test[0] != "CMD-SHELL" {
		t.Fatalf("healthcheck %q", hc)
	}
	return strings.Join(lines, "\n"), test[1]
}

// broker.acl (step 4b, part 2): users.acl in the data volume replaces
// requirepass in the config on stdin, and the healthcheck logs in as ping
// with the password beside it; without the file, both are as before.
func TestComposeACLFromTheDataVolume(t *testing.T) {
	command, health := composeScripts(t)
	bin := t.TempDir()
	// fakes: redis-server prints its stdin config, redis-cli its argv and auth
	_ = os.WriteFile(filepath.Join(bin, "redis-server"), []byte("#!/bin/sh\ncat\n"), 0o755)
	_ = os.WriteFile(filepath.Join(bin, "redis-cli"), []byte("#!/bin/sh\necho \"PONG $REDISCLI_AUTH $*\"\n"), 0o755)
	run := func(script, data string) string {
		cmd := exec.Command("sh", "-c", composeShell(t, script, data))
		cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "REDIS_PASSWORD=file-pw"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return string(out)
	}
	off := t.TempDir()
	conf := run(command, off)
	if !strings.Contains(conf, `requirepass "file-pw"`) || strings.Contains(conf, "aclfile") {
		t.Errorf("off: %s", conf)
	}
	if got := run(strings.Replace(health, "grep -q PONG", "cat", 1), off); !strings.Contains(got, "PONG file-pw ping") || strings.Contains(got, "--user") {
		t.Errorf("off healthcheck: %s", got)
	}
	on := t.TempDir()
	_ = os.WriteFile(filepath.Join(on, "users.acl"), []byte("user default off\n"), 0o600)
	_ = os.WriteFile(filepath.Join(on, "ping.pass"), []byte("ping-pw"), 0o600)
	conf = run(command, on)
	if !strings.Contains(conf, "aclfile "+on+"/users.acl") || strings.Contains(conf, "requirepass") || !strings.Contains(conf, "appendonly yes") {
		t.Errorf("on: %s", conf)
	}
	if got := run(strings.Replace(health, "grep -q PONG", "cat", 1), on); !strings.Contains(got, "PONG ping-pw --user ping ping") {
		t.Errorf("on healthcheck: %s", got)
	}
}
