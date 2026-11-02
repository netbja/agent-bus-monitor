package peer

import (
	"crypto/sha256"
	"encoding/hex"
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

// The fingerprint is keyed by a host-local secret: one bus reader holding
// the snapshot cannot test a guessed URL offline (reviewer-codex: an unkeyed
// hash verified guesses at a private path). The same key gives the same
// value, so the health check on this host compares them.
func TestEndpointFPIsKeyed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agentbus")
	key, err := FPKey(dir)
	if err != nil || len(key) != 32 {
		t.Fatalf("FPKey = %d bytes, %v", len(key), err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "endpoint-fp.key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi, err)
	}
	again, err := FPKey(dir)
	if err != nil || string(again) != string(key) {
		t.Fatal("a second read gives another key")
	}
	url := "http://127.0.0.1:11434/tenant-7f3a/v1"
	fp := EndpointFP(key, url)
	if fp != EndpointFP(key, url) || fp == EndpointFP(key, "http://127.0.0.1:11434/tenant-7f3b/v1") {
		t.Fatal("not a function of the URL under one key")
	}
	other, _ := FPKey(filepath.Join(t.TempDir(), "agentbus"))
	sum := sha256.Sum256([]byte(url))
	for _, guess := range []string{EndpointFP(other, url), EndpointFP(nil, url), hex.EncodeToString(sum[:6]), hex.EncodeToString(sum[:8])} {
		if guess == fp {
			t.Fatalf("a guess without the key verifies the URL: %s", guess)
		}
	}
}
