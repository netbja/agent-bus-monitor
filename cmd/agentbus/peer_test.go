package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modelsServer(t *testing.T, models ...string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		var data []map[string]string
		for _, m := range models {
			data = append(data, map[string]string{"id": m})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func peerEnv(t *testing.T, configBody string) func(string) string {
	t.Helper()
	xdg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "agentbus", "config.toml"), []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"XDG_CONFIG_HOME": xdg, "HOME": t.TempDir()}
	return func(k string) string { return env[k] }
}

// peer --check asks the endpoint for its models: exit 0 when it serves the
// configured one, 1 otherwise, never printing the URL's path.
func TestPeerCheck(t *testing.T) {
	srv := modelsServer(t, "qwen2.5-coder:3b")
	conf := "[llm.local]\nurl = \"" + srv.URL + "/v1\"\nmodel = \"qwen2.5-coder:3b\"\n[llm.other]\nurl = \"" + srv.URL + "/v1\"\nmodel = \"phi4\"\n"
	var out, errw bytes.Buffer
	if code := runPeer([]string{"--check", "local"}, peerEnv(t, conf), &out, &errw); code != 0 || !strings.Contains(out.String(), "serves qwen2.5-coder:3b") || strings.Contains(out.String(), "/v1") {
		t.Fatalf("check = %d %q %q", code, out.String(), errw.String())
	}
	out.Reset()
	errw.Reset()
	if code := runPeer([]string{"--check", "other"}, peerEnv(t, conf), &out, &errw); code != 1 || !strings.Contains(errw.String(), "phi4") {
		t.Fatalf("missing model = %d %q", code, errw.String())
	}
	errw.Reset()
	if code := runPeer([]string{"--check", "nope"}, peerEnv(t, conf), &out, &errw); code != 1 || !strings.Contains(errw.String(), "[llm.nope]") {
		t.Fatalf("unknown endpoint = %d %q", code, errw.String())
	}
}

// The peer refuses to start, before any bus or endpoint call, without a
// spec, an agent name, a resolvable endpoint or readable denies.
func TestPeerRefusesToStart(t *testing.T) {
	var out, errw bytes.Buffer
	if code := runPeer(nil, peerEnv(t, ""), &out, &errw); code != 2 {
		t.Fatalf("no args = %d", code)
	}
	dir := t.TempDir()
	spec := filepath.Join(dir, "launch.json")
	if err := os.WriteFile(spec, []byte(`{"model":"m","options":{"endpoint":"local"},"role":"local","project":"llmtest","manifest":"`+filepath.Join(dir, "missing.toml")+`","prompt":"p"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := peerEnv(t, "[llm.local]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\"\n")
	withAgent := func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "local"
		}
		return env(k)
	}
	for name, c := range map[string]struct {
		getenv func(string) string
		want   string
	}{
		"no agent":          {env, "AGENT_BUS_AGENT"},
		"denies unreadable": {withAgent, "deny rules"},
	} {
		errw.Reset()
		if code := runPeer([]string{"--spec-file", spec}, c.getenv, &out, &errw); code != 1 || !strings.Contains(errw.String(), c.want) {
			t.Errorf("%s = %d %q, want %q", name, code, errw.String(), c.want)
		}
	}
}
