package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/peer"
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

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
}

// The read root is the project's path (its template's working_dir), else the
// Git top-level of the launch folder, never the folder itself; / , $HOME and
// an ancestor of $HOME are refused (SEC-LLM-3: read_file and grep would cover
// redis.env, ~/.claude.json, ~/.codex/auth.json).
func TestPeerRoot(t *testing.T) {
	home := t.TempDir()
	projects := t.TempDir()
	repo := filepath.Join(home, "src", "shop")
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	tpl := func(project, dir string) {
		if err := os.WriteFile(filepath.Join(projects, project+".toml"), []byte("name = \""+project+"\"\nworking_dir = \""+dir+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tpl("shop", "~/src/shop")
	tpl("slash", "/")
	tpl("above", filepath.Dir(home))
	tpl("relup", "../..") // from repo/sub: the home's parent, by a relative path (reviewer-codex)
	tpl("reldot", ".")    // from $HOME: $HOME itself
	tpl("tilde", "~")
	env := map[string]string{"HOME": home, "AGENTBUS_PROJECTS_DIR": projects}
	getenv := func(k string) string { return env[k] }
	real := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }
	for _, c := range []struct {
		name, project, cwd, want, refused string
	}{
		{"the template's folder, wherever the launch", "shop", home, real(repo), ""},
		{"no template: the Git top-level", "other", filepath.Join(repo, "sub"), real(repo), ""},
		{"no template, no Git", "other", t.TempDir(), "", "not in a Git repository"},
		{"$HOME", "other", home, "", "the home directory"},
		{"/", "slash", repo, "", "the filesystem root"},
		{"an ancestor of $HOME", "above", repo, "", "holds the home directory"},
		{"a relative working_dir", "relup", filepath.Join(repo, "sub"), "", "not an absolute path"},
		{"a relative alias of $HOME", "reldot", home, "", "not an absolute path"},
		{"~ alone", "tilde", repo, "", "the home directory"},
	} {
		if c.name == "$HOME" {
			gitInit(t, home) // a dotfiles repository: its top-level is $HOME
		}
		got, err := peerRoot(c.project, c.cwd, getenv)
		switch {
		case c.refused == "" && (err != nil || got != c.want):
			t.Errorf("%s: root = %q, %v; want %q", c.name, got, err, c.want)
		case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
			t.Errorf("%s: root = %q, %v; want a refusal naming %q", c.name, got, err, c.refused)
		}
	}
}

// Launched from $HOME, the peer exits 1 with the reason, before the bus.
func TestPeerRefusesHomeAsRoot(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "launch.json")
	if err := os.WriteFile(spec, []byte(`{"model":"m","options":{"endpoint":"local"},"role":"local","project":"llmtest","manifest":"`+filepath.Join(dir, "missing.toml")+`","prompt":"p"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := peerEnv(t, "[llm.local]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\"\n")
	home := env("HOME")
	gitInit(t, home)
	t.Chdir(home)
	getenv := func(k string) string {
		switch k {
		case "AGENT_BUS_AGENT":
			return "local"
		case "AGENTBUS_PROJECTS_DIR":
			return filepath.Join(dir, "projects")
		}
		return env(k)
	}
	var out, errw bytes.Buffer
	if code := runPeer([]string{"--spec-file", spec}, getenv, &out, &errw); code != 1 || !strings.Contains(errw.String(), "the home directory") {
		t.Fatalf("from $HOME = %d %q", code, errw.String())
	}
}

// The start's model check is bounded: an endpoint that accepts and never
// answers delays the peer by startCheck at most, then it says so and goes on
// to take its receiver lease (R2: it used to hang there, unarmed).
func TestPeerStartCheckIsBounded(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-hang }))
	t.Cleanup(func() { close(hang); srv.Close() })
	defer func(d time.Duration) { startCheck = d }(startCheck)
	startCheck = 200 * time.Millisecond
	var out bytes.Buffer
	begin := time.Now()
	peerStartCheck(context.Background(), peer.Client{URL: srv.URL + "/v1", Model: "m"}, &out)
	if took := time.Since(begin); took > 2*time.Second {
		t.Fatalf("the start check took %s", took)
	}
	if !strings.Contains(out.String(), "endpoint not ready yet") {
		t.Fatalf("not said: %q", out.String())
	}
}

// With HOME unset or relative the home checks cannot run: the peer refuses,
// never reads a repository rooted at the real home (reviewer-codex).
func TestPeerRootNeedsAnAbsoluteHome(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	for _, home := range []string{"", "relative/home"} {
		env := map[string]string{"HOME": home, "AGENTBUS_PROJECTS_DIR": t.TempDir()}
		if root, err := peerRoot("other", repo, func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "HOME") {
			t.Errorf("HOME=%q: root = %q, %v; want a refusal naming HOME", home, root, err)
		}
	}
}
