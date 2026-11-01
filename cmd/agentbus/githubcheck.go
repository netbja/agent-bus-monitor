package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
)

// probeRun is one command's result as the github check reads it: ran is
// false when the command could not start, timedOut when the check's deadline
// stopped it.
type probeRun struct {
	ran, timedOut bool
	exit          int
	out           string
}

// githubCheck is the github health check (busmon pages, section 6): from
// where the project's services run, can they reach GitHub by SSH and by
// HTTPS? It keeps in memory since when both have failed, so a restarted
// dispatcher starts the hour over (a later warn, never an earlier one).
type githubCheck struct {
	run func(ctx context.Context, env []string, name string, args ...string) probeRun
	now func() time.Time

	mu      sync.Mutex
	neither time.Time // when both transports began failing; zero otherwise
}

func newGitHubCheck() *githubCheck { return &githubCheck{run: runProbe, now: time.Now} }

// outcome runs both probes in parallel within ctx, when the project's remote
// is on github.com.
func (c *githubCheck) outcome(ctx context.Context) health.Outcome {
	repo := c.repo(ctx)
	if repo == "" {
		c.mu.Lock()
		c.neither = time.Time{}
		c.mu.Unlock()
		return health.EvaluateGitHub("", health.Probe{}, health.Probe{}, 0)
	}
	var ssh, https health.Probe
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r := c.run(ctx, nil, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-T", "git@github.com")
		ssh = health.ClassifySSH(r.ran, r.timedOut, r.exit, r.out)
	}()
	go func() {
		defer wg.Done()
		// No credential helper and no prompt: a reachability probe only.
		r := c.run(ctx, []string{"GIT_TERMINAL_PROMPT=0"}, "git", "-c", "credential.helper=", "ls-remote", "https://github.com/"+repo)
		https = health.ClassifyHTTPS(r.ran, r.timedOut, r.exit, r.out)
	}()
	wg.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	var neitherFor time.Duration
	if health.GitHubNeither(ssh, https) {
		now := c.now()
		if c.neither.IsZero() {
			c.neither = now
		}
		neitherFor = now.Sub(c.neither)
	} else {
		c.neither = time.Time{}
	}
	return health.EvaluateGitHub(repo, ssh, https, neitherFor)
}

// repo is the owner/repo of the project's GitHub remote: origin's when it is
// on github.com, else the first remote that is, else "".
func (c *githubCheck) repo(ctx context.Context) string {
	r := c.run(ctx, nil, "git", "config", "--get-regexp", `^remote\..*\.url$`)
	if !r.ran {
		return ""
	}
	first := ""
	for _, line := range strings.Split(r.out, "\n") {
		key, url, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		repo, ok := health.GitHubRepo(url)
		if !ok {
			continue
		}
		if key == "remote.origin.url" {
			return repo
		}
		if first == "" {
			first = repo
		}
	}
	return first
}

// probeOutputMax bounds what a probe keeps of its output: enough for an
// error line, never a large repository's whole ref list.
const probeOutputMax = 16 << 10

type cappedBuffer struct {
	bytes.Buffer
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := probeOutputMax - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// runProbe runs one probe in the service's own environment plus env, with no
// stdin, in the project directory (the service's working directory).
func runProbe(ctx context.Context, env []string, name string, args ...string) probeRun {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = time.Second
	var out cappedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	r := probeRun{ran: true, out: out.String(), timedOut: ctx.Err() != nil}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		r.exit = exit.ExitCode()
	case r.timedOut:
		r.exit = -1
	default: // not found, not executable: the probe could not run
		return probeRun{}
	}
	return r
}
