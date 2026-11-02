package config

import (
	"strings"
	"testing"
)

// The local LLM peer's endpoints (spec docs/superpowers/specs/2026-10-31-local-llm-peer-design.md,
// "The endpoint: config.toml"): one [llm.<name>] table each, url and model
// required, remote optional; loopback only unless remote = true.
func TestEndpoints(t *testing.T) {
	f := Load(writeFile(t, "config.toml", `[display]
theme = "dark"

[llm.local]
url   = "http://127.0.0.1:11434/v1"  # Ollama
model = "qwen2.5-coder:3b"

[llm.box]
url    = "https://gpu.lan:8080/v1"
model  = "qwen2.5-coder:7b"
remote = true

[llm.ipv6]
url   = "http://[::1]:8080/v1"
model = "m"

[llm.named]
url   = "http://localhost:8080"
model = "m"
`), "config.toml")
	if len(f.Warnings) != 0 {
		t.Fatalf("warnings: %v", f.Warnings)
	}
	got := f.Endpoints()
	want := []Endpoint{
		{Name: "box", URL: "https://gpu.lan:8080/v1", Model: "qwen2.5-coder:7b", Remote: true, Line: 8},
		{Name: "ipv6", URL: "http://[::1]:8080/v1", Model: "m", Line: 13},
		{Name: "local", URL: "http://127.0.0.1:11434/v1", Model: "qwen2.5-coder:3b", Line: 4},
		{Name: "named", URL: "http://localhost:8080", Model: "m", Line: 17},
	}
	if len(got) != len(want) {
		t.Fatalf("endpoints %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("endpoint %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if e, ok := f.Endpoint("local"); !ok || e.Model != "qwen2.5-coder:3b" {
		t.Fatalf("Endpoint(local) %+v %v", e, ok)
	}
	if _, ok := f.Endpoint("absent"); ok {
		t.Fatal("an absent endpoint was found")
	}
	if !f.Has("display.theme") {
		t.Fatal("a registry key beside the endpoints was lost")
	}
}

// Every refusal drops the one endpoint, says why at its line, and never quotes
// a value: a secret, an unknown key, a URL that is not loopback without
// remote, not http(s), with user info, a query or a fragment, a model that is
// empty or more than one line, a wrong type, a name that is not a valid name,
// a key directly under [llm].
func TestEndpointRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		body, key, why string
	}{
		"a key":               {"[llm.a]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\"\nkey = \"sk-SECRET\"\n", "llm.a.key", "a secret does not belong in this file"},
		"a token":             {"[llm.a]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\"\ntoken = \"SECRET\"\n", "llm.a.token", "a secret does not belong in this file"},
		"a password":          {"[llm.a]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\"\npassword = \"SECRET\"\n", "llm.a.password", "a secret does not belong in this file"},
		"an unknown key":      {"[llm.a]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\"\ntemperature = 0.2\n", "llm.a.temperature", "unknown key"},
		"no url":              {"[llm.a]\nmodel = \"m\"\n", "llm.a", "no url"},
		"no model":            {"[llm.a]\nurl = \"http://127.0.0.1:1/v1\"\n", "llm.a", "no model"},
		"not loopback":        {"[llm.a]\nurl = \"http://10.0.0.5:1/v1\"\nmodel = \"m\"\n", "llm.a.url", "not loopback: set remote = true to send repository text there"},
		"another loopback ip": {"[llm.a]\nurl = \"http://127.0.0.2:1/v1\"\nmodel = \"m\"\n", "llm.a.url", "not loopback: set remote = true to send repository text there"},
		"remote false":        {"[llm.a]\nurl = \"http://gpu.lan:1/v1\"\nmodel = \"m\"\nremote = false\n", "llm.a.url", "not loopback: set remote = true to send repository text there"},
		"a scheme":            {"[llm.a]\nurl = \"ftp://127.0.0.1/v1\"\nmodel = \"m\"\n", "llm.a.url", "want an http or https URL"},
		"no host":             {"[llm.a]\nurl = \"http:///v1\"\nmodel = \"m\"\n", "llm.a.url", "want an http or https URL"},
		"user info":           {"[llm.a]\nurl = \"http://me:SECRET@127.0.0.1:1/v1\"\nmodel = \"m\"\n", "llm.a.url", "user info in a URL is a secret: it does not belong in this file"},
		"a query":             {"[llm.a]\nurl = \"http://127.0.0.1:1/v1?SECRET=1\"\nmodel = \"m\"\n", "llm.a.url", "want a URL without a query or fragment"},
		"a fragment":          {"[llm.a]\nurl = \"http://127.0.0.1:1/v1#SECRET\"\nmodel = \"m\"\n", "llm.a.url", "want a URL without a query or fragment"},
		"not a string url":    {"[llm.a]\nurl = 8080\nmodel = \"m\"\n", "llm.a.url", "want a string"},
		"an empty model":      {"[llm.a]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"\"\n", "llm.a.model", "want a one-line name"},
		"a two-line model":    {"[llm.a]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\\nSECRET\"\n", "llm.a.model", "want a one-line name"},
		"remote not a bool":   {"[llm.a]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\"\nremote = \"yes\"\n", "llm.a.remote", "want true or false"},
		"a bad name":          {"[llm.Box]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\"\n", "llm.Box", "not a valid endpoint name"},
		"directly under llm":  {"[llm]\nurl = \"http://127.0.0.1:1/v1\"\n", "llm.url", "an endpoint is a table: [llm.<name>]"},
	} {
		f := Load(writeFile(t, "config.toml", c.body+"[llm.ok]\nurl = \"http://127.0.0.1:2/v1\"\nmodel = \"ok\"\n"), "config.toml")
		var found bool
		for _, w := range f.Warnings {
			if strings.Contains(w.String(), "SECRET") || strings.Contains(w.String(), "127.0.0") {
				t.Errorf("%s: a warning quotes the file: %s", name, w)
			}
			if w.Key == c.key && strings.Contains(w.Msg, c.why) && strings.Contains(w.Msg, "endpoint ignored") {
				found = true
				if w.Line == 0 {
					t.Errorf("%s: no line in %s", name, w)
				}
			}
		}
		if !found {
			t.Errorf("%s: warnings %v, want %s: %s; endpoint ignored", name, f.Warnings, c.key, c.why)
		}
		if eps := f.Endpoints(); len(eps) != 1 || eps[0].Name != "ok" {
			t.Errorf("%s: endpoints %+v, want only ok", name, eps)
		}
	}
}

// busmon.toml never held an endpoint: there it stays an unknown key, and
// llm.* never reaches the registry's keys.
func TestEndpointsOnlyInConfigToml(t *testing.T) {
	f := LoadLegacy(writeFile(t, "busmon.toml", "[llm.a]\nurl = \"http://127.0.0.1:1/v1\"\nmodel = \"m\"\n"), "busmon.toml")
	if len(f.Endpoints()) != 0 || len(f.Warnings) != 2 {
		t.Fatalf("legacy: %+v %v", f.Endpoints(), f.Warnings)
	}
	for _, k := range Keys {
		if strings.HasPrefix(k.Name, "llm.") {
			t.Fatalf("registry key %s collides with the endpoints' tables", k.Name)
		}
	}
	if (*File)(nil).Endpoints() != nil {
		t.Fatal("a nil file has endpoints")
	}
}

// One warning per problem (reviewer-codex): an endpoint missing both url and
// model says both, and a refused url is said even when the model is missing.
func TestEndpointWarnsEveryProblem(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		want []string
	}{
		"neither":                  {"[llm.a]\nremote = true\n", []string{"llm.a: no url", "llm.a: no model"}},
		"a bad url, no model":      {"[llm.a]\nurl = \"http://10.0.0.5/v1\"\n", []string{"llm.a.url: not loopback", "llm.a: no model"}},
		"an unknown key, no model": {"[llm.a]\nurl = \"http://127.0.0.1/v1\"\nctx = 4096\n", []string{"llm.a.ctx: unknown key", "llm.a: no model"}},
		// remote unread: the url is not judged against a remote = false nobody wrote
		"a bad remote, a remote url": {"[llm.a]\nurl = \"http://gpu.lan/v1\"\nmodel = \"m\"\nremote = \"yes\"\n", []string{"llm.a.remote: want true or false"}},
	} {
		f := Load(writeFile(t, "config.toml", c.body), "config.toml")
		got := ""
		for _, w := range f.Warnings {
			got += w.String() + "\n"
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in:\n%s", name, w, got)
			}
		}
		if len(f.Warnings) != len(c.want) || len(f.Endpoints()) != 0 {
			t.Errorf("%s: %d warnings, endpoints %+v:\n%s", name, len(f.Warnings), f.Endpoints(), got)
		}
	}
}
