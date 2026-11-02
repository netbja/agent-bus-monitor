package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/peer"
)

func llmConfig(t *testing.T, body string) *config.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(p, "config.toml")
}

// testFPKey is the host key the check's tests fingerprint with.
var testFPKey = []byte("0123456789abcdef0123456789abcdef")

func llmDeps(snaps map[string]bus.AgentSnapshot, cfg *config.File) llmCheckDeps {
	return llmCheckDeps{
		Agents: func(context.Context) (map[string]bus.AgentSnapshot, error) { return snaps, nil },
		Config: func() *config.File { return cfg },
		FPKey:  func() ([]byte, error) { return testFPKey, nil },
	}
}

// The check probes only the endpoints the registered local-llm peers use (by
// their model, as the peer's own Resolve requires), each GET /models with a
// 2 s deadline; only httptest endpoints, never a real one.
func TestLLMEndpointCheck(t *testing.T) {
	up := modelsServer(t, "qwen")
	other := modelsServer(t, "phi4")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(3 * time.Second) }))
	t.Cleanup(slow.Close)
	conf := "[llm.local]\nurl = \"" + up.URL + "/v1\"\nmodel = \"qwen\"\n" +
		"[llm.wrong]\nurl = \"" + other.URL + "/v1\"\nmodel = \"llama3\"\n" +
		"[llm.slow]\nurl = \"" + slow.URL + "/v1\"\nmodel = \"slowm\"\n" +
		"[llm.unused]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"nobody\"\n"
	cfg := llmConfig(t, conf)
	peer := func(model string) bus.AgentSnapshot { return bus.AgentSnapshot{Client: "local-llm", Model: model} }
	for name, c := range map[string]struct {
		snaps  map[string]bus.AgentSnapshot
		want   health.Status
		detail []string
		not    []string
	}{
		"no peer": {map[string]bus.AgentSnapshot{"coder": {Client: "claude-code", Model: "opus"}}, health.Unknown, []string{"no local-llm agent"}, nil},
		"ok":      {map[string]bus.AgentSnapshot{"local": peer("qwen")}, health.OK, []string{"local", "serves qwen"}, []string{"unused", "/v1"}},
		"model not served": {map[string]bus.AgentSnapshot{"local": peer("qwen"), "l2": peer("llama3")}, health.Warn,
			[]string{"wrong", "answers without llama3"}, []string{"unused"}},
		"slow is no answer":  {map[string]bus.AgentSnapshot{"s": peer("slowm")}, health.Fail, []string{"slow", "no answer"}, nil},
		"no endpoint for it": {map[string]bus.AgentSnapshot{"x": peer("ghost")}, health.Unknown, []string{"x (model ghost)"}, nil},
	} {
		start := time.Now()
		out, err := llmEndpointCheck(llmDeps(c.snaps, cfg))(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out.Status != c.want {
			t.Errorf("%s: %s (%s), want %s", name, out.Status, out.Detail, c.want)
		}
		for _, d := range c.detail {
			if !strings.Contains(out.Detail, d) {
				t.Errorf("%s: detail %q lacks %q", name, out.Detail, d)
			}
		}
		for _, d := range c.not {
			if strings.Contains(out.Detail, d) {
				t.Errorf("%s: detail %q holds %q", name, out.Detail, d)
			}
		}
		if time.Since(start) > 2500*time.Millisecond {
			t.Errorf("%s took %s, past the 2 s probe deadline", name, time.Since(start))
		}
	}
	// A registry read that fails, and a config file with problems.
	d := llmDeps(nil, cfg)
	d.Agents = func(context.Context) (map[string]bus.AgentSnapshot, error) { return nil, errors.New("down") }
	if out, _ := llmEndpointCheck(d)(context.Background()); out.Status != health.Unknown {
		t.Errorf("registry down = %+v", out)
	}
	bad := llmConfig(t, "this is not toml [")
	if out, _ := llmEndpointCheck(llmDeps(map[string]bus.AgentSnapshot{"local": peer("qwen")}, bad))(context.Background()); out.Status != health.Unknown || !strings.Contains(out.Detail, "config.toml") {
		t.Errorf("unreadable config = %+v", out)
	}
}

// A redirect to another server is never followed by the check either.
func TestLLMEndpointCheckFollowsNoRedirect(t *testing.T) {
	target := modelsServer(t, "qwen")
	hops := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(hops.Close)
	cfg := llmConfig(t, "[llm.local]\nurl = \""+hops.URL+"/v1\"\nmodel = \"qwen\"\n")
	out, _ := llmEndpointCheck(llmDeps(map[string]bus.AgentSnapshot{"local": {Client: "local-llm", Model: "qwen"}}, cfg))(context.Background())
	if out.Status != health.Fail || !strings.Contains(out.Detail, "307") {
		t.Fatalf("redirect = %+v, want fail naming 307", out)
	}
}

// A server error is an answer: fail naming its status, never "no answer".
func TestLLMEndpointCheckServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "loading", 503) }))
	t.Cleanup(srv.Close)
	cfg := llmConfig(t, "[llm.local]\nurl = \""+srv.URL+"/v1\"\nmodel = \"qwen\"\n")
	out, _ := llmEndpointCheck(llmDeps(map[string]bus.AgentSnapshot{"local": {Client: "local-llm", Model: "qwen"}}, cfg))(context.Background())
	if out.Status != health.Fail || !strings.Contains(out.Detail, "status 503") || strings.Contains(out.Detail, "no answer") {
		t.Fatalf("503 = %+v", out)
	}
}

// The detail is built from fixed words and a status, never from what the
// server answered: a body or a redirect echoing the URL's path stays out of
// the stored health detail (reviewer-codex).
func TestLLMEndpointDetailNeverEchoesTheServer(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"503 body": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "failed at /tenant/private-prefix/v1/models", 503)
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:1/tenant/private-prefix/v1/models", http.StatusTemporaryRedirect)
		},
		"401 body": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no access to /tenant/private-prefix/v1", 401)
		},
		"bad list": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("/tenant/private-prefix/v1 {")) },
	} {
		srv := httptest.NewServer(h)
		cfg := llmConfig(t, "[llm.local]\nurl = \""+srv.URL+"/tenant/private-prefix/v1\"\nmodel = \"qwen\"\n")
		out, _ := llmEndpointCheck(llmDeps(map[string]bus.AgentSnapshot{"local": {Client: "local-llm", Model: "qwen"}}, cfg))(context.Background())
		srv.Close()
		if out.Status != health.Fail || strings.Contains(out.Detail, "private-prefix") || strings.Contains(out.Detail, "/v1") {
			t.Errorf("%s = %+v, want fail with no path", name, out)
		}
	}
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	cfg := llmConfig(t, "[llm.local]\nurl = \""+url+"/tenant/private-prefix/v1\"\nmodel = \"qwen\"\n")
	out, _ := llmEndpointCheck(llmDeps(map[string]bus.AgentSnapshot{"local": {Client: "local-llm", Model: "qwen"}}, cfg))(context.Background())
	if out.Status != health.Fail || !strings.Contains(out.Detail, "connection refused") || strings.Contains(out.Detail, "private-prefix") {
		t.Errorf("down = %+v", out)
	}
}

// The check probes the endpoint each peer resolved (options.endpoint, kept in
// its snapshot), not every endpoint serving its model: an unused one of the
// same model being down is no failure (R4). A peer naming an endpoint
// config.toml no longer has is said; a snapshot written before the field
// existed still matches by model.
func TestLLMEndpointCheckProbesTheSelectedEndpoint(t *testing.T) {
	up := modelsServer(t, "qwen")
	conf := "[llm.used]\nurl = \"" + up.URL + "/v1\"\nmodel = \"qwen\"\n" +
		"[llm.spare]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"qwen\"\n"
	cfg := llmConfig(t, conf)
	sel := func(ep string) bus.AgentSnapshot {
		return bus.AgentSnapshot{Client: "local-llm", Model: "qwen", Endpoint: ep}
	}
	out, err := llmEndpointCheck(llmDeps(map[string]bus.AgentSnapshot{"local": sel("used")}, cfg))(context.Background())
	if err != nil || out.Status != health.OK || strings.Contains(out.Detail, "spare") {
		t.Fatalf("selected endpoint up, a spare down = %s %q %v, want ok without spare", out.Status, out.Detail, err)
	}
	out, _ = llmEndpointCheck(llmDeps(map[string]bus.AgentSnapshot{"local": sel("spare")}, cfg))(context.Background())
	if out.Status != health.Fail || !strings.Contains(out.Detail, "spare") {
		t.Fatalf("selected endpoint down = %s %q, want fail naming spare", out.Status, out.Detail)
	}
	out, _ = llmEndpointCheck(llmDeps(map[string]bus.AgentSnapshot{"local": sel("gone")}, cfg))(context.Background())
	if !strings.Contains(out.Detail, "local (endpoint gone)") {
		t.Fatalf("an endpoint config.toml lacks = %s %q", out.Status, out.Detail)
	}
}

// The endpoint is judged for the model the running peer asks for, not the
// one config.toml names now: an endpoint changed to another model after the
// launch, serving that one only, is no ok (reviewer-codex); the change is said.
func TestLLMEndpointCheckJudgesThePeersRunningModel(t *testing.T) {
	srv := modelsServer(t, "phi")
	cfg := llmConfig(t, "[llm.used]\nurl = \""+srv.URL+"/v1\"\nmodel = \"phi\"\n")
	snaps := map[string]bus.AgentSnapshot{"local": {Client: "local-llm", Model: "qwen", Endpoint: "used"}}
	out, err := llmEndpointCheck(llmDeps(snaps, cfg))(context.Background())
	if err != nil || out.Status != health.Warn || !strings.Contains(out.Detail, "answers without qwen") || !strings.Contains(out.Detail, "local (runs qwen; [llm.used] now names phi)") {
		t.Fatalf("config changed under a running peer = %s %q %v", out.Status, out.Detail, err)
	}
}

// The peer keeps the URL it resolved for its whole life: an endpoint whose
// URL config.toml changed since is said, even when the new server answers
// (reviewer-codex), by a fingerprint, never the URL.
func TestLLMEndpointCheckSaysAURLChangedSinceTheLaunch(t *testing.T) {
	srv := modelsServer(t, "qwen")
	cfg := llmConfig(t, "[llm.used]\nurl = \""+srv.URL+"/v1\"\nmodel = \"qwen\"\n")
	snap := func(fp string) map[string]bus.AgentSnapshot {
		return map[string]bus.AgentSnapshot{"local": {Client: "local-llm", Model: "qwen", Endpoint: "used", EndpointFP: fp}}
	}
	if out, _ := llmEndpointCheck(llmDeps(snap(peer.EndpointFP(testFPKey, srv.URL+"/v1")), cfg))(context.Background()); out.Status != health.OK {
		t.Fatalf("same URL = %s %q", out.Status, out.Detail)
	}
	out, _ := llmEndpointCheck(llmDeps(snap(peer.EndpointFP(testFPKey, "http://127.0.0.1:1/v1")), cfg))(context.Background())
	if out.Status != health.Warn || !strings.Contains(out.Detail, "local ([llm.used]'s URL changed since its launch)") || strings.Contains(out.Detail, "127.0.0.1:1") {
		t.Fatalf("URL changed = %s %q", out.Status, out.Detail)
	}
}

// A peer that recorded a fingerprint, checked on a host whose key cannot be
// read, is unknown, never ok: the URL could not be compared, and that is said
// (reviewer-codex, master).
func TestLLMEndpointCheckSaysAnUnreadableKey(t *testing.T) {
	srv := modelsServer(t, "qwen")
	cfg := llmConfig(t, "[llm.used]\nurl = \""+srv.URL+"/v1\"\nmodel = \"qwen\"\n")
	d := llmDeps(map[string]bus.AgentSnapshot{"local": {Client: "local-llm", Model: "qwen", Endpoint: "used", EndpointFP: "00112233aabbccdd"}}, cfg)
	d.FPKey = func() ([]byte, error) { return nil, errors.New("permission denied") }
	out, _ := llmEndpointCheck(d)(context.Background())
	if out.Status != health.Unknown || !strings.Contains(out.Detail, "URL not compared for local") {
		t.Fatalf("unreadable key = %s %q", out.Status, out.Detail)
	}
}
