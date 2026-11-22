package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// wipCmdWorld: a project on the test broker, config.toml with the limit
// given (machine line), the request command's env and stderr captured.
func wipCmdWorld(t *testing.T, cfg string) (*bus.Bus, *strings.Builder) {
	t.Helper()
	b, _ := dialMain(t)
	return wipCmdWorldOn(t, b, cfg)
}

func wipSend(t *testing.T, b *bus.Bus, task, target string) {
	t.Helper()
	var out strings.Builder
	if err := runRequest(context.Background(), b, "master", []string{"send", task, target, "do", task}, &out); err != nil {
		t.Fatal(err)
	}
}

func wipRun(b *bus.Bus, self string, args ...string) error {
	var out strings.Builder
	return runRequest(context.Background(), b, self, args, &out)
}

// wip-limit step 2: request accept applies the project's board.wip_limit
// (default 2): a first accept at the limit is refused with the bus's text;
// request review takes one out of flight, and the accept then passes.
func TestRequestAcceptAppliesTheWIPLimit(t *testing.T) {
	b, _ := wipCmdWorld(t, "")
	for _, task := range []string{"a", "b", "c"} {
		wipSend(t, b, task, "coder")
	}
	for _, task := range []string{"a", "b"} {
		if err := wipRun(b, "coder", "accept", task); err != nil {
			t.Fatal(err)
		}
	}
	err := wipRun(b, "coder", "accept", "c")
	if !errors.Is(err, bus.ErrRequestRefused) || !strings.Contains(err.Error(), "wip limit reached for coder (2 of 2 accepted: a, b)") {
		t.Fatalf("accept at the default limit: %v", err)
	}
	if err := wipRun(b, "coder", "review", "a", "--branch", "feat/a", "step", "1", "in", "review"); err != nil {
		t.Fatal(err)
	}
	board, _ := b.Board(context.Background())
	if e := board["a"]; e.State != "review" || e.Request.Branch != "feat/a" || e.Request.ReviewNote != "step 1 in review" {
		t.Fatalf("a: %s %+v", e.State, e.Request)
	}
	if err := wipRun(b, "coder", "accept", "c"); err != nil {
		t.Fatalf("under the limit after review: %v", err)
	}
	if err := wipRun(b, "coder", "review", "a", "--branch", "not a branch"); err == nil {
		t.Fatal("an invalid branch passed")
	}
}

// The project's own value ([project.<p>.board]) applies; 0 is no limit.
func TestRequestAcceptReadsTheProjectsLimit(t *testing.T) {
	b, _ := dialMain(t)
	cfg := "[board]\nwip_limit = 1\n[project." + b.Project() + ".board]\nwip_limit = 0\n"
	b, _ = wipCmdWorldOn(t, b, cfg)
	for _, task := range []string{"a", "b", "c"} {
		wipSend(t, b, task, "coder")
		if err := wipRun(b, "coder", "accept", task); err != nil {
			t.Fatalf("limit 0, accept %s: %v", task, err)
		}
	}
}

// wipCmdWorldOn is wipCmdWorld on an existing bus.
func wipCmdWorldOn(t *testing.T, b *bus.Bus, cfg string) (*bus.Bus, *strings.Builder) {
	t.Helper()
	dir := t.TempDir()
	if cfg != "" {
		_ = os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700)
		_ = os.WriteFile(filepath.Join(dir, "agentbus", "config.toml"), []byte(cfg), 0o600)
	}
	env := map[string]string{"XDG_CONFIG_HOME": dir}
	old, oldErr := requestGetenv, requestStderr
	var errw strings.Builder
	requestGetenv, requestStderr = func(k string) string { return env[k] }, &errw
	t.Cleanup(func() { requestGetenv, requestStderr = old, oldErr })
	return b, &errw
}

// request send to an agent at its limit is queued, never refused (Q5), with
// one line on stderr naming what it holds and the agents under their limit
// that hold a request of this project; under the limit, nothing is said.
func TestRequestSendToAnAgentAtItsLimit(t *testing.T) {
	b, errw := wipCmdWorld(t, "")
	wipSend(t, b, "a", "coder")
	wipSend(t, b, "b", "coder")
	wipSend(t, b, "x", "coder2")
	wipSend(t, b, "y", "coder3") // at its limit too: not offered as a route
	wipSend(t, b, "z", "coder3")
	for _, at := range [][2]string{{"coder", "a"}, {"coder", "b"}, {"coder3", "y"}, {"coder3", "z"}} {
		if err := wipRun(b, at[0], "accept", at[1]); err != nil {
			t.Fatal(err)
		}
	}
	if errw.Len() != 0 {
		t.Fatalf("said under the limit: %q", errw.String())
	}
	wipSend(t, b, "c", "coder")
	if got := errw.String(); got != "coder is at its wip limit (2/2: a, b): queued; coder2 0/2\n" {
		t.Fatalf("stderr %q", got)
	}
	board, _ := b.Board(context.Background())
	if board["c"].State != "requested" {
		t.Fatalf("c: %s", board["c"].State)
	}
}
