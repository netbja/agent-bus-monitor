package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/caprun"
)

// run --cap is recognised on the raw argv, before any parser of agentbus's
// own reads a word of the command (agent-resource-limits §6): its -p, --help
// or --project belong to the command.
func TestCapRequestRaw(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"run", "--cap", "--", "go", "test", "-p", "2", "--help"}, true},
		{[]string{"run", "--cap", "--mem", "4G", "--", "true"}, true},
		{[]string{"run"}, false},
		{[]string{"run", "reset"}, false},
		{[]string{"-p", "demo", "run", "--cap", "--", "true"}, false},
		{[]string{"report", "coder", "run", "--cap"}, false},
	} {
		if got := isCapRun(c.args); got != c.want {
			t.Errorf("isCapRun(%q) = %v", c.args, got)
		}
	}
}

func limitsEnv(t *testing.T, file string) func(string) string {
	t.Helper()
	cfg := t.TempDir()
	if file != "" {
		if err := os.MkdirAll(filepath.Join(cfg, "agentbus"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg, "agentbus", "config.toml"), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return cfg
		}
		return ""
	}
}

// The defaults are the person's keys (Q4: 8G, 30m, no CPU cap); a flag may
// only narrow them, since an agent cannot raise its own cap, and a refusal
// names the key.
func TestCapLimits(t *testing.T) {
	for _, c := range []struct {
		file string
		args []string
		want caprun.Limits
		argv []string
		err  string
	}{
		{"", []string{"--", "go", "test", "./..."}, caprun.Limits{Mem: 8 << 30, MemText: "8G", Time: 30 * time.Minute}, []string{"go", "test", "./..."}, ""},
		{"", []string{"--mem", "4G", "--time", "5m", "--", "sh", "mutant.sh"}, caprun.Limits{Mem: 4 << 30, MemText: "4G", Time: 5 * time.Minute}, []string{"sh", "mutant.sh"}, ""},
		{"", []string{"--cpu", "200", "--", "true"}, caprun.Limits{Mem: 8 << 30, MemText: "8G", Time: 30 * time.Minute, CPU: 200}, []string{"true"}, ""},
		{"", []string{"--", "go", "test", "--mem", "16G"}, caprun.Limits{Mem: 8 << 30, MemText: "8G", Time: 30 * time.Minute}, []string{"go", "test", "--mem", "16G"}, ""},
		{"", []string{"--mem", "16G", "--", "true"}, caprun.Limits{}, nil, "above limits.command_memory (8G)"},
		{"", []string{"--mem", "off", "--", "true"}, caprun.Limits{}, nil, "limits.command_memory"},
		{"", []string{"--time", "1h", "--", "true"}, caprun.Limits{}, nil, "above limits.command_time (30m0s)"},
		{"", []string{"--time", "0", "--", "true"}, caprun.Limits{}, nil, "--time"},
		{"", []string{"--mem", "70%", "--", "true"}, caprun.Limits{}, nil, "--mem"},
		{"", []string{"--", "true", "x"}, caprun.Limits{Mem: 8 << 30, MemText: "8G", Time: 30 * time.Minute}, []string{"true", "x"}, ""},
		{"", []string{"true"}, caprun.Limits{}, nil, "usage"},
		{"", []string{"--"}, caprun.Limits{}, nil, "usage"},
		{"", []string{"--bogus", "--", "true"}, caprun.Limits{}, nil, "usage"},
		{"[limits]\ncommand_memory = \"off\"\ncommand_time = \"0\"\ncommand_cpu = 100\n", []string{"--", "true"}, caprun.Limits{CPU: 100}, []string{"true"}, ""},
		{"[limits]\ncommand_memory = \"off\"\n", []string{"--mem", "off", "--", "go", "test", "-race"}, caprun.Limits{Time: 30 * time.Minute}, []string{"go", "test", "-race"}, ""},
		{"[limits]\ncommand_memory = \"off\"\ncommand_time = \"0\"\n", []string{"--mem", "64G", "--time", "2h", "--", "true"}, caprun.Limits{Mem: 64 << 30, MemText: "64G", Time: 2 * time.Hour}, []string{"true"}, ""},
		{"[limits]\ncommand_cpu = 100\n", []string{"--cpu", "200", "--", "true"}, caprun.Limits{}, nil, "above limits.command_cpu (100)"},
	} {
		l, argv, err := capLimits(c.args, limitsEnv(t, c.file))
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.args, err, c.err)
			}
			continue
		}
		if err != nil || l != c.want || !slices.Equal(argv, c.argv) {
			t.Errorf("%q: %+v %q %v, want %+v %q", c.args, l, argv, err, c.want, c.argv)
		}
	}
}
