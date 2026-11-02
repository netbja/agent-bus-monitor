package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
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

// With HOME unset or relative, the home is the user's passwd entry; the home
// checks run against it, so a repository rooted there is still refused. No
// home at all refuses the start (reviewer-codex, master).
func TestPeerRootFindsTheHomeWithoutHOME(t *testing.T) {
	home := t.TempDir()
	gitInit(t, home)
	defer func(f func() (string, error)) { userHome = f }(userHome)
	for _, h := range []string{"", "relative/home"} {
		env := map[string]string{"HOME": h, "AGENTBUS_PROJECTS_DIR": t.TempDir()}
		getenv := func(k string) string { return env[k] }
		userHome = func() (string, error) { return home, nil }
		if root, err := peerRoot("other", home, getenv); err == nil || !strings.Contains(err.Error(), "the home directory") {
			t.Errorf("HOME=%q, passwd home a repository: root = %q, %v; want refused as the home", h, root, err)
		}
		if got, err := peerHome(getenv); err != nil || got != home {
			t.Errorf("HOME=%q: peerHome = %q, %v; want the passwd entry", h, got, err)
		}
		userHome = func() (string, error) { return "", errors.New("no passwd entry") }
		if root, err := peerRoot("other", home, getenv); err == nil || !strings.Contains(err.Error(), "no home directory") {
			t.Errorf("HOME=%q, no passwd home: root = %q, %v; want a refusal", h, root, err)
		}
	}
}

// The fingerprint key lives beside config.toml; with no configuration
// directory there is no key, and none is written in the current folder.
func TestEndpointFPKeyNeedsTheConfigDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	if _, _, err := endpointFPKey(func(string) string { return "" }); err == nil {
		t.Fatal("a key with no configuration directory")
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Fatalf("written in the cwd: %v", entries)
	}
	xdg := t.TempDir()
	if key, _, err := endpointFPKey(func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }); err != nil || len(key) != 32 {
		t.Fatalf("key = %d bytes, %v", len(key), err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "agentbus", "endpoint-fp.key")); err != nil {
		t.Fatal(err)
	}
}

// The peer's identity carries the endpoint it resolved and, with the host
// key, its URL's keyed fingerprint: R4's wiring, without which llm-endpoint
// falls back to matching by model and never sees a changed URL (foureyes).
func TestPeerIdentRecordsItsEndpoint(t *testing.T) {
	xdg := t.TempDir()
	getenv := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }
	s := peer.Settings{Endpoint: "used", URL: "http://127.0.0.1:11434/v1", Model: "qwen"}
	id, note := peerIdent(bus.AgentIdent{Client: "local-llm"}, s, getenv)
	key, _, err := endpointFPKey(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if id.Client != "local-llm" || id.Endpoint != "used" || id.EndpointFP != peer.EndpointFP(key, s.URL) || note != "" {
		t.Fatalf("ident = %+v, note %q", id, note)
	}
	// No key: the endpoint still, no fingerprint, and it is said.
	id, note = peerIdent(bus.AgentIdent{}, s, func(string) string { return "" })
	if id.Endpoint != "used" || id.EndpointFP != "" || !strings.Contains(note, "fingerprint") {
		t.Fatalf("no key: ident = %+v, note %q", id, note)
	}
}

// A template's ~ is the home the checks use: with HOME unset, the passwd
// entry's (foureyes).
func TestPeerRootExpandsTildeWithTheSameHome(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "src", "shop")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	projects := t.TempDir()
	if err := os.WriteFile(filepath.Join(projects, "shop.toml"), []byte("name = \"shop\"\nworking_dir = \"~/src/shop\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func(f func() (string, error)) { userHome = f }(userHome)
	userHome = func() (string, error) { return home, nil }
	env := map[string]string{"HOME": "", "AGENTBUS_PROJECTS_DIR": projects}
	want, _ := filepath.EvalSymlinks(repo)
	if root, err := peerRoot("shop", t.TempDir(), func(k string) string { return env[k] }); err != nil || root != want {
		t.Fatalf("root = %q, %v; want %q", root, err, want)
	}
}

// Residual c of the PR 259 security re-check: a Git repository at or under
// the configuration base ($XDG_CONFIG_HOME, else ~/.config: a dotfiles
// repository) or one holding agentbus's configuration directory (where
// redis.env and endpoint-fp.key live) is never the peer's root.
func TestPeerRootRefusesTheConfigurationDirectory(t *testing.T) {
	home := t.TempDir()
	dotconfig := filepath.Join(home, ".config")
	nvim := filepath.Join(dotconfig, "nvim")
	if err := os.MkdirAll(nvim, 0o700); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dotconfig)
	outside := t.TempDir() // XDG_CONFIG_HOME outside the home, inside a repository
	xdg := filepath.Join(outside, "cfg")
	if err := os.MkdirAll(filepath.Join(xdg, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitInit(t, outside)
	emacs := filepath.Join(dotconfig, "emacs") // its own repository, under the base
	tool := filepath.Join(xdg, "tool")         // its own repository, under an XDG base outside the home
	for _, d := range []string{emacs, tool} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		gitInit(t, d)
	}
	plain := filepath.Join(home, "src", "shop")
	if err := os.MkdirAll(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	gitInit(t, plain)
	projects := t.TempDir()
	for _, c := range []struct {
		name, cwd, xdg, refused string
	}{
		{"~/.config, a dotfiles repository", dotconfig, "", "configuration directory"},
		{"a folder under ~/.config", nvim, "", "configuration directory"},
		{"a repository of its own under ~/.config", emacs, "", "configuration directory"},
		{"a repository under $XDG_CONFIG_HOME", tool, xdg, "configuration directory"},
		{"a repository holding $XDG_CONFIG_HOME/agentbus", outside, xdg, "configuration directory"},
		{"a project's own repository", plain, "", ""},
		{"a project's own repository, XDG elsewhere", plain, xdg, ""},
	} {
		env := map[string]string{"HOME": home, "AGENTBUS_PROJECTS_DIR": projects, "XDG_CONFIG_HOME": c.xdg}
		got, err := peerRoot("other", c.cwd, func(k string) string { return env[k] })
		switch {
		case c.refused == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
			t.Errorf("%s: root = %q, %v; want a refusal naming %q", c.name, got, err, c.refused)
		}
	}
}
