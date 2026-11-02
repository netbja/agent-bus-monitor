package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/peer"
)

// llmProbeDeadline bounds one endpoint's GET /models (the spec's 2 s).
const llmProbeDeadline = 2 * time.Second

// llmCheckDeps is what llm-endpoint reads, injected: a test never reaches a
// real endpoint.
type llmCheckDeps struct {
	Agents func(context.Context) (map[string]bus.AgentSnapshot, error)
	Config func() *config.File    // config.toml, as the services read it
	FPKey  func() ([]byte, error) // the host's endpoint fingerprint key (peer.FPKey)
}

func realLLMCheckDeps(b *bus.Bus) llmCheckDeps {
	return llmCheckDeps{Agents: b.Agents, Config: func() *config.File { return config.Load(config.Path(os.Getenv), "config.toml") },
		FPKey: func() ([]byte, error) { return endpointFPKey(os.Getenv) }}
}

// llmEndpointCheck (local-llm-peer step 2) probes the endpoints the
// registered local-llm peers use: the one each peer resolved and recorded in
// its snapshot (Endpoint, R4), never another endpoint of the same model. A
// snapshot from before that field matches by model, as the peer's Resolve
// requires the two to be equal. Every
// probe is peer.Client.CheckModel, so the peer's own rules hold: no redirect,
// no proxy. Probes run side by side, each within llmProbeDeadline.
func llmEndpointCheck(d llmCheckDeps) func(context.Context) (health.Outcome, error) {
	return func(ctx context.Context) (health.Outcome, error) {
		var in health.LLMEndpointInput
		snaps, err := d.Agents(ctx)
		if err != nil {
			return health.Outcome{Status: health.Unknown, Detail: "agent registry unavailable"}, err
		}
		models := map[string][]string{} // model -> peers with no recorded endpoint
		var named []string              // peers with a recorded endpoint
		for name, s := range snaps {
			if s.Client != "local-llm" {
				continue
			}
			in.Peers = append(in.Peers, name)
			if s.Endpoint != "" {
				named = append(named, name)
			} else {
				models[s.Model] = append(models[s.Model], name)
			}
		}
		sort.Strings(named)
		if len(in.Peers) == 0 {
			return health.EvaluateLLMEndpoint(in), nil
		}
		cfg := d.Config()
		for _, w := range cfg.Warnings {
			if w.Key == "" && (w.Msg == "does not parse; ignored" || w.Msg == "cannot be read; ignored") {
				in.ConfigErr = "config.toml: " + w.Msg
				return health.EvaluateLLMEndpoint(in), nil
			}
		}
		var fpKey []byte // without it a recorded fingerprint cannot be compared
		if d.FPKey != nil {
			fpKey, _ = d.FPKey()
		}
		// Each probe is an endpoint and the model asked of it, once.
		var used []config.Endpoint
		seen := map[string]bool{}
		probe := func(ep config.Endpoint) {
			if k := ep.Name + "\x00" + ep.Model; !seen[k] {
				seen[k] = true
				used = append(used, ep)
			}
		}
		// A peer's own endpoint is probed for the model the peer runs, which
		// its launch checked against config.toml then: an endpoint edited since
		// is judged for what the peer still asks, and the edit is said.
		for _, p := range named {
			s := snaps[p]
			ep, ok := cfg.Endpoint(s.Endpoint)
			switch {
			case !ok:
				in.Unmatched = append(in.Unmatched, fmt.Sprintf("%s (endpoint %s)", p, s.Endpoint))
				continue
			case s.EndpointFP != "" && fpKey != nil && s.EndpointFP != peer.EndpointFP(fpKey, ep.URL):
				// The peer still calls the URL it started with; this probe
				// reaches the new one only.
				in.Unmatched = append(in.Unmatched, fmt.Sprintf("%s ([llm.%s]'s URL changed since its launch)", p, ep.Name))
			case s.Model != "" && s.Model != ep.Model:
				in.Unmatched = append(in.Unmatched, fmt.Sprintf("%s (runs %s; [llm.%s] now names %s)", p, s.Model, ep.Name, ep.Model))
				ep.Model = s.Model
			}
			probe(ep)
		}
		for _, ep := range cfg.Endpoints() {
			if len(models[ep.Model]) > 0 {
				probe(ep)
			}
		}
		for model, peers := range models {
			served := false
			for _, ep := range used {
				served = served || ep.Model == model
			}
			if !served {
				sort.Strings(peers)
				for _, p := range peers {
					in.Unmatched = append(in.Unmatched, fmt.Sprintf("%s (model %s)", p, model))
				}
			}
		}
		sort.Strings(in.Unmatched)
		in.Probes = make([]health.EndpointProbe, len(used))
		var wg sync.WaitGroup
		for i, ep := range used {
			wg.Add(1)
			go func() {
				defer wg.Done()
				in.Probes[i] = probeEndpoint(ctx, ep)
			}()
		}
		wg.Wait()
		return health.EvaluateLLMEndpoint(in), nil
	}
}

// probeEndpoint is one GET {url}/models, read through the peer's client.
// The probe keeps only a status and fixed words, never the server's own
// text: a body or a redirect may echo the URL's path, which no health detail
// shows (reviewer-codex).
func probeEndpoint(ctx context.Context, ep config.Endpoint) health.EndpointProbe {
	ctx, cancel := context.WithTimeout(ctx, llmProbeDeadline)
	defer cancel()
	p := health.EndpointProbe{Name: ep.Name, Host: endpointHost(ep.URL), Model: ep.Model}
	err := (peer.Client{URL: ep.URL, Model: ep.Model}).CheckModel(ctx)
	switch status := peer.StatusOf(err); {
	case err == nil:
		p.Reached, p.Status, p.Listed = true, 200, true
	case errors.Is(err, peer.ErrModelNotServed):
		p.Reached, p.Status = true, 200
	case status != 0:
		p.Reached, p.Status = true, status
	case errors.Is(err, peer.ErrBadResponse):
		p.Reached, p.Err = true, "the answer is not a model list"
	case errors.Is(err, context.DeadlineExceeded):
		p.Err = "none within " + llmProbeDeadline.String()
	case errors.Is(err, syscall.ECONNREFUSED):
		p.Err = "connection refused"
	default: // no answer, no reason worth naming
	}
	return p
}
