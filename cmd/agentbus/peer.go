package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/peer"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// runPeer is agentbus peer (local-llm-peer): the local model as a test peer.
// --check <endpoint> asks config.toml's endpoint whether it serves its model
// (no project, no broker); --spec-file is what the adapter script passes
// (scripts/local-llm-peer): the launcher's resolved launch.json.
func runPeer(args []string, getenv func(string) string, out, errw io.Writer) int {
	rest, check := extractFlag(args, "--check")
	rest, specFile := extractFlag(rest, "--spec-file")
	cfg := config.Load(config.Path(getenv), "config.toml")
	switch {
	case check != "" && specFile == "" && len(rest) == 0:
		return peerCheck(check, cfg, out, errw)
	case specFile == "" || check != "" || len(rest) != 0:
		fmt.Fprintln(errw, "usage: agentbus peer --spec-file <launch.json> | agentbus peer --check <endpoint>")
		return 2
	}
	agent := getenv("AGENT_BUS_AGENT")
	if !bus.ValidName(agent) {
		fmt.Fprintln(errw, "peer: AGENT_BUS_AGENT is not set to an agent name: launch it with agent-launch <role> <project>")
		return 1
	}
	spec, err := peer.LoadSpec(specFile)
	if err != nil {
		fmt.Fprintln(errw, "peer:", err)
		return 1
	}
	settings, err := peer.Resolve(spec, cfg)
	if err != nil {
		fmt.Fprintln(errw, "peer:", err)
		return 1
	}
	// The role's effective Read denies, or no start at all (fail closed).
	manifest, err := rolecfg.Load(spec.Manifest)
	if err != nil {
		fmt.Fprintln(errw, "peer: the role's deny rules cannot be loaded:", err)
		return 1
	}
	denies, err := peer.ReadDenies(manifest, spec.Role)
	if err != nil {
		fmt.Fprintln(errw, "peer: the role's deny rules cannot be read:", err)
		return 1
	}
	root, err := os.Getwd()
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		fmt.Fprintln(errw, "peer: the project root:", err)
		return 1
	}
	deny, err := peer.NewDenyMatcher(denies, root, getenv("HOME"))
	if err != nil {
		fmt.Fprintln(errw, "peer: the role's deny rules cannot be read:", err)
		return 1
	}
	project := spec.Project
	if project == "" {
		project = getenv("AGENT_BUS_PROJECT")
	}
	client, err := bus.Connect("")
	if err != nil {
		fmt.Fprintln(errw, "peer: Redis connection failed:", err)
		return 1
	}
	b, err := bus.Open(client, project)
	if err != nil {
		fmt.Fprintln(errw, "peer:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := peer.Client{URL: settings.URL, Model: settings.Model, MaxTokens: settings.MaxTokens}
	fmt.Fprintf(out, "local-llm peer %s on %s, model %s, root %s\n", agent, endpointHost(settings.URL), settings.Model, root)
	if err := c.CheckModel(ctx); err != nil {
		fmt.Fprintln(out, "endpoint not ready yet:", err)
	}
	host, _ := os.Hostname()
	r := &peer.Runner{Bus: b, Agent: agent, Consumer: fmt.Sprintf("%s:%d", host, os.Getpid()), Ident: statusIdent(ctx),
		Turn: peer.Turn{Client: c, Prompt: spec.Prompt, MaxCalls: 8, Timeout: settings.TurnTimeout,
			Tools: peer.Tools{Root: root, Deny: deny, Thread: b.Thread, Agents: b.Agents}},
		RetryEvery: 30 * time.Second, LeaseWait: 5 * time.Second, Out: out}
	if err := r.Run(ctx); err != nil {
		fmt.Fprintln(errw, "peer:", err)
		return 1
	}
	return 0
}

// peerCheck asks one endpoint for its models: 0 when it serves its model.
func peerCheck(name string, cfg *config.File, out, errw io.Writer) int {
	ep, ok := cfg.Endpoint(name)
	if !ok {
		fmt.Fprintf(errw, "peer: config.toml has no valid [llm.%s] (see agentbus config)\n", name)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := (peer.Client{URL: ep.URL, Model: ep.Model}).CheckModel(ctx); err != nil {
		fmt.Fprintf(errw, "peer: [llm.%s] at %s: %v\n", name, endpointHost(ep.URL), err)
		return 1
	}
	fmt.Fprintf(out, "[llm.%s] at %s serves %s\n", name, endpointHost(ep.URL), ep.Model)
	return 0
}

// endpointHost is an endpoint's host and port, never its path: what agentbus
// config shows of it.
func endpointHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "?"
	}
	return u.Host
}
