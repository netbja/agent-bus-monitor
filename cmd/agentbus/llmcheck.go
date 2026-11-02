package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
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
	Config func() *config.File // config.toml, as the services read it
}

func realLLMCheckDeps(b *bus.Bus) llmCheckDeps {
	return llmCheckDeps{Agents: b.Agents, Config: func() *config.File { return config.Load(config.Path(os.Getenv), "config.toml") }}
}

// llmEndpointCheck (local-llm-peer step 2) probes the endpoints the
// registered local-llm peers use: an endpoint serves a peer when its model
// is the peer's (the peer's own Resolve refuses to start otherwise). Every
// probe is peer.Client.CheckModel, so the peer's own rules hold: no redirect,
// no proxy. Probes run side by side, each within llmProbeDeadline.
func llmEndpointCheck(d llmCheckDeps) func(context.Context) (health.Outcome, error) {
	return func(ctx context.Context) (health.Outcome, error) {
		var in health.LLMEndpointInput
		snaps, err := d.Agents(ctx)
		if err != nil {
			return health.Outcome{Status: health.Unknown, Detail: "agent registry unavailable"}, err
		}
		models := map[string][]string{} // model -> peers
		for name, s := range snaps {
			if s.Client == "local-llm" {
				in.Peers = append(in.Peers, name)
				models[s.Model] = append(models[s.Model], name)
			}
		}
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
		var used []config.Endpoint
		for _, ep := range cfg.Endpoints() {
			if len(models[ep.Model]) > 0 {
				used = append(used, ep)
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
func probeEndpoint(ctx context.Context, ep config.Endpoint) health.EndpointProbe {
	ctx, cancel := context.WithTimeout(ctx, llmProbeDeadline)
	defer cancel()
	p := health.EndpointProbe{Name: ep.Name, Host: endpointHost(ep.URL), Model: ep.Model}
	err := (peer.Client{URL: ep.URL, Model: ep.Model}).CheckModel(ctx)
	var ef *peer.EndpointError
	switch {
	case err == nil:
		p.Reached, p.Status, p.Listed = true, 200, true
	case errors.Is(err, peer.ErrModelNotServed):
		p.Reached, p.Status = true, 200
	case errors.Is(err, context.DeadlineExceeded):
		p.Err = "no answer within " + llmProbeDeadline.String()
	case errors.Is(err, peer.ErrRequestRefused), errors.Is(err, peer.ErrBadResponse):
		p.Reached, p.Err = true, err.Error()
	case errors.As(err, &ef):
		// A server error answered (models: status 5xx/404/...); anything else gave no answer.
		p.Reached, p.Err = strings.HasPrefix(ef.Err.Error(), "models: status"), ef.Err.Error()
	default:
		p.Err = err.Error()
	}
	return p
}
