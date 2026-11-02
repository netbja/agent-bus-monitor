package health

import (
	"strings"
	"testing"
)

// llm-endpoint (local-llm-peer step 2): ok when every endpoint the peers use
// lists its model, warn when one answers without it, fail when one gives no
// answer or an error status; unknown with no peer, no readable config or no
// endpoint for a peer. The worst endpoint decides; the detail names host and
// port, never the URL's path.
func TestEvaluateLLMEndpoint(t *testing.T) {
	ok := EndpointProbe{Name: "local", Host: "127.0.0.1:11434", Model: "qwen", Reached: true, Status: 200, Listed: true}
	missing := EndpointProbe{Name: "gpu", Host: "gpu.lan:8080", Model: "big", Reached: true, Status: 200}
	down := EndpointProbe{Name: "box", Host: "127.0.0.1:8080", Model: "m", Err: "connection refused"}
	silent := EndpointProbe{Name: "quiet", Host: "127.0.0.1:7000", Model: "m"}
	errStatus := EndpointProbe{Name: "srv", Host: "127.0.0.1:9000", Model: "m", Reached: true, Status: 500}
	for name, c := range map[string]struct {
		in     LLMEndpointInput
		want   Status
		detail []string
	}{
		"no peer":          {LLMEndpointInput{}, Unknown, []string{"no local-llm agent"}},
		"config unread":    {LLMEndpointInput{Peers: []string{"local"}, ConfigErr: "config.toml cannot be read"}, Unknown, []string{"config.toml cannot be read"}},
		"no endpoint":      {LLMEndpointInput{Peers: []string{"local"}, Unmatched: []string{"local (model qwen)"}}, Unknown, []string{"no [llm.*] endpoint", "local (model qwen)"}},
		"ok":               {LLMEndpointInput{Peers: []string{"local"}, Probes: []EndpointProbe{ok}}, OK, []string{"local 127.0.0.1:11434 serves qwen"}},
		"model missing":    {LLMEndpointInput{Peers: []string{"local"}, Probes: []EndpointProbe{ok, missing}}, Warn, []string{"gpu gpu.lan:8080 answers without big"}},
		"down":             {LLMEndpointInput{Peers: []string{"local"}, Probes: []EndpointProbe{missing, down}}, Fail, []string{"box 127.0.0.1:8080 no answer: connection refused", "gpu"}},
		"no reason":        {LLMEndpointInput{Peers: []string{"local"}, Probes: []EndpointProbe{silent}}, Fail, []string{"quiet 127.0.0.1:7000 no answer"}},
		"error status":     {LLMEndpointInput{Peers: []string{"local"}, Probes: []EndpointProbe{errStatus}}, Fail, []string{"srv 127.0.0.1:9000 status 500"}},
		"a peer unmatched": {LLMEndpointInput{Peers: []string{"local", "other"}, Probes: []EndpointProbe{ok}, Unmatched: []string{"other (model phi)"}}, Warn, []string{"no [llm.*] endpoint for other (model phi)"}},
		// The endpoint a running peer resolved has changed in config.toml
		// since its launch: a warning in its own words (foureyes).
		"endpoint changed": {LLMEndpointInput{Peers: []string{"local"}, Probes: []EndpointProbe{ok}, Changed: []string{"local ([llm.local]'s URL changed since its launch)"}}, Warn, []string{"endpoint changed for local ([llm.local]'s URL changed since its launch)"}},
		// A URL that could not be compared is not known: never ok (master),
		// and a worse finding still decides.
		"not compared":          {LLMEndpointInput{Peers: []string{"local"}, Probes: []EndpointProbe{ok}, Unverified: []string{"local"}}, Unknown, []string{"URL not compared for local", "serves qwen"}},
		"not compared and down": {LLMEndpointInput{Peers: []string{"local"}, Probes: []EndpointProbe{down}, Unverified: []string{"local"}}, Fail, []string{"no answer", "URL not compared for local"}},
	} {
		out := EvaluateLLMEndpoint(c.in)
		if out.Status != c.want {
			t.Errorf("%s: %s (%s), want %s", name, out.Status, out.Detail, c.want)
		}
		for _, d := range c.detail {
			if !strings.Contains(out.Detail, d) {
				t.Errorf("%s: detail %q lacks %q", name, out.Detail, d)
			}
		}
		if strings.Contains(out.Detail, "/v1") || len([]rune(out.Detail)) > detailMax {
			t.Errorf("%s: detail %q", name, out.Detail)
		}
	}
	if d, ok := ByID("llm-endpoint"); !ok || d.Place(Fail) != PlaceForYou || d.Place(Warn) != PlaceForYou || d.Command == "" {
		t.Fatalf("definition = %+v %v, want for you on warn and fail with a command", d, ok)
	}
}
