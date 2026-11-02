package health

import (
	"fmt"
	"strings"
)

// EndpointProbe is one GET {url}/models of an endpoint a local-llm peer uses
// (local-llm-peer step 2). Host is its host and port: the URL's path is never
// shown.
type EndpointProbe struct {
	Name, Host, Model string
	Reached           bool   // an HTTP answer came back
	Status            int    // its status
	Listed            bool   // the answer lists Model
	Err               string // no answer, or an answer that is no model list: why
}

// LLMEndpointInput is everything llm-endpoint judges from.
type LLMEndpointInput struct {
	Peers     []string        // registered agents with client local-llm
	ConfigErr string          // config.toml unreadable: why
	Probes    []EndpointProbe // one per endpoint those peers use
	Unmatched []string        // peers no [llm.*] endpoint serves: "name (model m)"
}

// EvaluateLLMEndpoint: fail when an endpoint gives no answer or an error
// status, warn when one answers without its model or a peer has no endpoint,
// ok when every endpoint lists its model; unknown with no peer, no readable
// config or no endpoint at all. The worst decides; the detail names each.
func EvaluateLLMEndpoint(in LLMEndpointInput) Outcome {
	switch {
	case len(in.Peers) == 0:
		return Outcome{Status: Unknown, Detail: "no local-llm agent registered"}
	case in.ConfigErr != "":
		return Outcome{Status: Unknown, Detail: truncateDetail(in.ConfigErr)}
	case len(in.Probes) == 0:
		return Outcome{Status: Unknown, Detail: truncateDetail("no [llm.*] endpoint in config.toml serves " + strings.Join(in.Unmatched, ", "))}
	}
	status := OK
	var bad, good []string
	for _, p := range in.Probes {
		switch {
		case !p.Reached || p.Err != "":
			status = Fail
			why := p.Err
			if why == "" {
				why = "no answer"
			}
			if p.Reached {
				bad = append(bad, fmt.Sprintf("%s %s %s", p.Name, p.Host, why))
			} else {
				bad = append(bad, fmt.Sprintf("%s %s no answer: %s", p.Name, p.Host, why))
			}
		case p.Status != 200:
			status = Fail
			bad = append(bad, fmt.Sprintf("%s %s status %d", p.Name, p.Host, p.Status))
		case !p.Listed:
			if status == OK {
				status = Warn
			}
			bad = append(bad, fmt.Sprintf("%s %s answers without %s", p.Name, p.Host, p.Model))
		default:
			good = append(good, fmt.Sprintf("%s %s serves %s", p.Name, p.Host, p.Model))
		}
	}
	if len(in.Unmatched) > 0 {
		if status == OK {
			status = Warn
		}
		bad = append(bad, "no [llm.*] endpoint for "+strings.Join(in.Unmatched, ", "))
	}
	return Outcome{Status: status, Detail: truncateDetail(strings.Join(append(bad, good...), "; "))}
}
