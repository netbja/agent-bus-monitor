package peer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const specJSON = `{"model": "qwen2.5-coder:3b", "client": "local-llm", "provider": "local",
 "options": {"endpoint": "local", "turn_timeout": "2m", "max_tokens": 512},
 "profile": "local", "role": "local", "manifest": "/m/roles.toml", "project": "llmtest",
 "prompt": "You are local.", "prompt_path": "/m/local.md"}`

func TestLoadSpecAndSettings(t *testing.T) {
	dir := t.TempDir()
	spec, err := LoadSpec(writeFile(t, dir, "launch.json", specJSON))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Role != "local" || spec.Project != "llmtest" || spec.Manifest != "/m/roles.toml" || spec.Prompt != "You are local." {
		t.Fatalf("spec = %+v", spec)
	}
	cfg := config.Load(writeFile(t, dir, "config.toml", "[llm.local]\nurl = \"http://127.0.0.1:11434/v1\"\nmodel = \"qwen2.5-coder:3b\"\n"), "config.toml")
	s, err := Resolve(spec, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "http://127.0.0.1:11434/v1" || s.Model != "qwen2.5-coder:3b" || s.TurnTimeout != 2*time.Minute || s.MaxTokens != 512 {
		t.Fatalf("settings = %+v", s)
	}
}

func TestResolveDefaultsAndRefusals(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Load(writeFile(t, dir, "config.toml", "[llm.local]\nurl = \"http://127.0.0.1:11434/v1\"\nmodel = \"qwen2.5-coder:3b\"\n"), "config.toml")
	base := Spec{Model: "qwen2.5-coder:3b", Role: "local", Project: "llmtest", Options: map[string]any{"endpoint": "local"}}
	s, err := Resolve(base, cfg)
	if err != nil || s.TurnTimeout != 10*time.Minute || s.MaxTokens != 1024 {
		t.Fatalf("defaults = %+v %v, want 10m and 1024", s, err)
	}
	for name, c := range map[string]struct {
		spec Spec
		want string
	}{
		"no endpoint option": {Spec{Model: "qwen2.5-coder:3b", Options: map[string]any{}}, "options.endpoint"},
		"unknown endpoint":   {Spec{Model: "qwen2.5-coder:3b", Options: map[string]any{"endpoint": "gpu"}}, "[llm.gpu]"},
		"model differs":      {Spec{Model: "llama3:8b", Options: map[string]any{"endpoint": "local"}}, "llama3:8b"},
		"bad timeout":        {Spec{Model: "qwen2.5-coder:3b", Options: map[string]any{"endpoint": "local", "turn_timeout": "soon"}}, "turn_timeout"},
		"zero tokens":        {Spec{Model: "qwen2.5-coder:3b", Options: map[string]any{"endpoint": "local", "max_tokens": int64(0)}}, "max_tokens"},
	} {
		if _, err := Resolve(c.spec, cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, c.want)
		}
	}
	// The model mismatch names both, never the URL.
	_, err = Resolve(Spec{Model: "llama3:8b", Options: map[string]any{"endpoint": "local"}}, cfg)
	if err == nil || !strings.Contains(err.Error(), "qwen2.5-coder:3b") || strings.Contains(err.Error(), "11434") {
		t.Fatalf("mismatch = %v", err)
	}
}
