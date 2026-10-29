package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStatuslineNeverFailsAndPassesStdinThrough(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	in := `{"model":{"display_name":"Opus"},"rate_limits":{"five_hour":{"used_percentage":12,"resets_at":1792116530}}}`
	var out bytes.Buffer
	if code := runStatusline(nil, strings.NewReader(in), &out); code != 0 || !strings.Contains(out.String(), "Opus · 5h 12%") {
		t.Fatalf("built-in: %d %q", code, out.String())
	}
	out.Reset()
	if code := runStatusline([]string{"--then", "cat"}, strings.NewReader(in), &out); code != 0 || out.String() != in {
		t.Fatalf("--then got %q", out.String())
	}
	big := strings.Repeat(" ", 70<<10) + in
	out.Reset()
	if code := runStatusline([]string{"--then", "wc -c"}, strings.NewReader(big), &out); code != 0 || strings.TrimSpace(out.String()) != strconv.Itoa(len(big)) {
		t.Fatalf("over-cap stdin not replayed: %q", out.String())
	}
	out.Reset()
	if code := runStatusline(nil, strings.NewReader("garbage"), &out); code != 0 {
		t.Fatal("bad input failed the status line")
	}
}

func TestBuiltinStatuslineNeedsNoOtherTool(t *testing.T) {
	in := `{"model":{"display_name":"Opus 5.5"},"workspace":{"current_dir":"/x/myapp"},"context_window":{"used_percentage":41},
		"rate_limits":{"five_hour":{"used_percentage":12,"resets_at":4102444800},"seven_day":{"used_percentage":54,"resets_at":4102444800}}}`
	got := builtinStatusline([]byte(in), time.Unix(4102444000, 0))
	for _, want := range []string{"Opus 5.5", "myapp", "ctx 41%", "5h 12% (", "7d 54%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("line %q lacks %q", got, want)
		}
	}
	var out bytes.Buffer
	statuslineInstall("", &out)
	if strings.Contains(out.String(), `"command": "`) && strings.Contains(strings.SplitN(out.String(), "\n}", 2)[0], "--then") {
		t.Fatalf("the default install chains another tool:\n%s", out.String())
	}
}

// The service's tokens pass reads Claude Code's projects directory, from
// CLAUDE_CONFIG_DIR first; AGENT_BUS_TOKENS=0 turns it off.
func TestServiceTokens(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	home := func() (string, error) { return "/home/user", nil }
	if c := serviceTokens(env(nil), home); c.Root != "/home/user/.claude/projects" || c.Every != 5*time.Minute || c.Budget.Files != 64 {
		t.Errorf("default %+v", c)
	}
	if c := serviceTokens(env(map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}), home); c.Root != "/cfg/projects" {
		t.Errorf("CLAUDE_CONFIG_DIR: %+v", c)
	}
	if c := serviceTokens(env(map[string]string{"AGENT_BUS_TOKENS": "0"}), home); c.Root != "" {
		t.Errorf("off: %+v", c)
	}
}
