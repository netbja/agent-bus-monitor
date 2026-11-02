package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/peer"
	"github.com/netbja/agent-bus-monitor/projlist"
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
	project := spec.Project
	if project == "" {
		project = getenv("AGENT_BUS_PROJECT")
	}
	cwd, err := os.Getwd()
	root := ""
	if err == nil {
		root, err = peerRoot(project, cwd, getenv)
	}
	if err != nil {
		fmt.Fprintln(errw, "peer: the project root:", err)
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
	home, _ := peerHome(getenv) // peerRoot has checked it
	deny, err := peer.NewDenyMatcher(denies, root, home)
	if err != nil {
		fmt.Fprintln(errw, "peer: the role's deny rules cannot be read:", err)
		return 1
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
	peerStartCheck(ctx, c, out)
	host, _ := os.Hostname()
	ident, note := peerIdent(statusIdent(ctx), settings, getenv)
	if note != "" {
		fmt.Fprintln(out, note)
	}
	r := &peer.Runner{Bus: b, Agent: agent, Consumer: fmt.Sprintf("%s:%d", host, os.Getpid()), Ident: ident,
		Turn: peer.Turn{Client: c, Prompt: spec.Prompt, MaxCalls: 8, Timeout: settings.TurnTimeout,
			Tools: peer.Tools{Root: root, Deny: deny, Thread: b.Thread, Agents: b.Agents}},
		RetryEvery: 30 * time.Second, LeaseWait: 5 * time.Second, Out: out}
	if err := r.Run(ctx); err != nil {
		fmt.Fprintln(errw, "peer:", err)
		return 1
	}
	return 0
}

// startCheck bounds the model check at start (R2): it runs before the
// receiver lease, and the client has no timeout of its own.
var startCheck = 5 * time.Second

// peerStartCheck says whether the endpoint serves the model yet; a fault is
// only said, the peer starts anyway and retries per entry.
func peerStartCheck(ctx context.Context, c peer.Client, out io.Writer) {
	ctx, cancel := context.WithTimeout(ctx, startCheck)
	defer cancel()
	if err := c.CheckModel(ctx); err != nil {
		fmt.Fprintln(out, "endpoint not ready yet:", err)
	}
}

// peerRoot is the folder the peer's tools read under (SEC-LLM-3): the
// project's path, its template's working_dir, else the Git top-level of the
// launch folder; never the launch folder itself, which agent-spawn takes from
// its caller. The filesystem root, the home directory and a folder holding
// it are refused: read_file and grep would reach every credential in it.
func peerRoot(project, cwd string, getenv func(string) string) (string, error) {
	home, err := peerHome(getenv)
	if err != nil {
		return "", err
	}
	root := ""
	if rows, err := projlist.Templates(rolecfg.ProjectsDir(getenv)); err == nil {
		for _, r := range rows {
			if r.Name == project && r.WorkingDir != "" {
				root = os.ExpandEnv(strings.Replace(r.WorkingDir, "~", getenv("HOME"), 1))
			}
		}
	}
	if root == "" {
		cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("no template for %s names its folder, and %s is not in a Git repository", project, cwd)
		}
		root = strings.TrimSpace(string(out))
	}
	// A relative folder would resolve against wherever the peer was launched,
	// and stay relative through EvalSymlinks, past the home checks below and
	// past every absolute deny pattern (reviewer-codex): refused. bootstrap
	// writes an absolute working_dir; git answers an absolute top-level.
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("the project's folder %q is not an absolute path: refused", root)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	switch {
	case root == "/":
		return "", fmt.Errorf("%s is the filesystem root: refused", root)
	case root == home:
		return "", fmt.Errorf("%s is the home directory: refused (launch the peer from the project's folder)", root)
	case strings.HasPrefix(home, root+string(filepath.Separator)):
		return "", fmt.Errorf("%s holds the home directory: refused", root)
	}
	return root, nil
}

// peerIdent adds to the peer's identity the endpoint it resolved, which
// health llm-endpoint probes for it (R4), and its URL's keyed fingerprint,
// the URL it keeps for its life (the bus can test no guess against it).
// Without the host key no fingerprint is recorded, and note says so.
func peerIdent(ident bus.AgentIdent, s peer.Settings, getenv func(string) string) (bus.AgentIdent, string) {
	ident.Endpoint = s.Endpoint
	key, note, err := endpointFPKey(getenv)
	if err != nil {
		return ident, "no endpoint fingerprint key (" + err.Error() + "): health cannot tell if [llm." + s.Endpoint + "]'s URL changes"
	}
	ident.EndpointFP = peer.EndpointFP(key, s.URL)
	return ident, note
}

// endpointFPKey is the host's endpoint fingerprint key, beside config.toml;
// with no configuration directory there is none (never one in the cwd).
func endpointFPKey(getenv func(string) string) ([]byte, string, error) {
	p := config.Path(getenv)
	if p == "" {
		return nil, "", fmt.Errorf("no configuration directory")
	}
	return peer.FPKey(filepath.Dir(p))
}

// userHome is the user's home from the passwd entry (os/user); a test's.
var userHome = func() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}

// peerHome is the home the root checks and the ~/ denies are anchored on:
// HOME, else (unset or relative) the user's passwd entry; with neither
// absolute the peer does not start (reviewer-codex, master: an empty home
// skipped the checks and anchored the ~/ denies nowhere).
func peerHome(getenv func(string) string) (string, error) {
	if h := getenv("HOME"); filepath.IsAbs(h) {
		return h, nil
	}
	if h, err := userHome(); err == nil && filepath.IsAbs(h) {
		return h, nil
	}
	return "", fmt.Errorf("no home directory: HOME is not an absolute path and the user has no home entry: refused")
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
