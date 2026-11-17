package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/redis/go-redis/v9"
)

// human-not-an-agent: ask and status name the agent that asks or declares.
// From an agent's terminal, the person's recorded name there wrote the
// person into {p}:agents under that agent's terminal (agentbus ask <person>
// in master's); it is refused, and an agent of the person's name, or the
// person's own shell, still writes.
func TestPersonGuard(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[identity]\nas = \"charlie\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(agent string) func(string) string {
		return func(k string) string {
			switch k {
			case "XDG_CONFIG_HOME", "HOME":
				return dir
			case "AGENT_BUS_AGENT":
				return agent
			}
			return ""
		}
	}
	for _, c := range []struct {
		agent, cmd string
		rest       []string
		refused    bool
	}{
		{"master", "ask", []string{"charlie", "--file", "q.json"}, true},
		{"master", "ask", []string{"--file", "q.json", "charlie"}, true},
		{"master", "status", []string{"charlie", "waiting", "x"}, true},
		{"master", "ask", []string{"master", "--file", "q.json"}, false},
		{"master", "status", []string{"master", "idle"}, false},
		{"master", "ask", []string{"charlie", "--withdraw", "1-0"}, false}, // closing a stray ask stays possible
		{"charlie", "status", []string{"charlie", "idle"}, false},          // an agent of the person's name
		{"", "status", []string{"charlie", "idle"}, false},                 // the person's own shell
		{"master", "report", []string{"charlie", "x"}, false},              // not a snapshot write
	} {
		err := personGuard(env(c.agent), c.cmd, c.rest)
		if (err != nil) != c.refused {
			t.Errorf("%s %s %v as %q: %v", c.cmd, c.rest, c.rest, c.agent, err)
		}
		if err != nil && (!strings.Contains(err.Error(), "the person") || !strings.Contains(err.Error(), "agentbus ask master")) {
			t.Errorf("%s: unclear refusal %q", c.cmd, err)
		}
	}
}

// The guard runs in main, before any broker call: the built binary refuses
// with no broker to reach.
func TestPersonGuardInMain(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agentbus")
	if out, err := runOut("go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[identity]\nas = \"charlie\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-p", "demo", "status", "charlie", "waiting", "x")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "AGENT_BUS_AGENT=master", "REDIS_HOST=127.0.0.1", "REDIS_PORT=1"}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 || !strings.Contains(stderr.String(), `"charlie" is the person`) {
		t.Fatalf("status charlie from master: %v, stderr %q", err, stderr.String())
	}
}

// reviewer-codex on 6cd737c6: the dispatcher knows the person from
// identity.as, not only from the capped cmd stream; the service's config
// and the foreground one both start from dispatchBaseConfig.
func TestDispatchConfigKnowsThePerson(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	cfg := filepath.Join(root, "cfg", "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[identity]\nas = \"charlie\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dispatchBaseConfig().Person; got != "charlie" {
		t.Fatalf("the base config's person %q", got)
	}
	b, err := bus.Open(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "demo") // never dialled
	if err != nil {
		t.Fatal(err)
	}
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	sc, _, err := serviceDispatchConfig(b, "demo", "", paths, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Person != "charlie" {
		t.Fatalf("the service's person %q", sc.Person)
	}
}
