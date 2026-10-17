package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

const approvalManifest = `[roles.master]
model = "sonnet"
permission = "acceptEdits"
tier = "boot"
pops = true
[roles.deploy]
model = "sonnet"
permission = "bypassPermissions"
tier = "pop"
approval = "person"
[roles.coder]
model = "opus"
permission = "bypassPermissions"
tier = "boot"
`

func approvalTestDeps(t *testing.T, env map[string]string) approvalDeps {
	path := filepath.Join(t.TempDir(), "roles.toml")
	if err := os.WriteFile(path, []byte(approvalManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return approvalDeps{
		Manifest: func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) },
		Getenv:   func(k string) string { return env[k] },
	}
}

func TestApprovalArgs(t *testing.T) {
	d := approvalTestDeps(t, map[string]string{"AGENT_BUS_APPROVAL_TTL": "soon"})
	ok := approvalTestDeps(t, nil)
	for _, c := range []struct {
		d    approvalDeps
		args []string
	}{
		{ok, []string{"ask", "nobody", "why"}},
		{ok, []string{"ask", "coder", "why"}},
		{ok, []string{"ask", "deploy"}},
		{ok, []string{"use", "deploy", "--check", "--for-swap"}},
		{ok, []string{"use"}},
		{ok, []string{"other"}},
		{d, []string{"use", "deploy"}},
	} {
		var out, errw bytes.Buffer
		if code := runApproval(context.Background(), nil, c.d, "master", c.args, &out, &errw); code != 2 {
			t.Fatalf("%v: exit %d (%s)", c.args, code, errw.String())
		}
	}
}

func TestApprovalAskAndUse(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.AsksKey(b.Project()), bus.AgentsKey(b.Project())) })
	d := approvalTestDeps(t, nil)
	var out, errw bytes.Buffer
	if code := runApproval(ctx, b, d, "master", []string{"ask", "deploy", "move", "/tmp"}, &out, &errw); code != 0 {
		t.Fatalf("ask: %d %s", code, errw.String())
	}
	if !strings.HasPrefix(out.String(), "approval ask ") || !strings.Contains(out.String(), " for deploy: answer it in busmon") {
		t.Fatalf("ask out = %q", out.String())
	}
	id := strings.Fields(out.String())[2]
	out.Reset()
	errw.Reset()
	if code := runApproval(ctx, b, d, "master", []string{"use", "deploy"}, &out, &errw); code != 3 ||
		!strings.Contains(errw.String(), "agentbus approval ask deploy <reason>") {
		t.Fatalf("use before the answer: %d %q", code, errw.String())
	}
	if _, _, err := b.Answer(ctx, id, bus.ApprovalQuestion("deploy"), "bernard", bus.ApprovalAnswer, ""); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runApproval(ctx, b, d, "master", []string{"use", "deploy"}, &out, &errw); code != 0 || strings.TrimSpace(out.String()) != id {
		t.Fatalf("use: %d %q %s", code, out.String(), errw.String())
	}
	if code := runApproval(ctx, b, d, "master", []string{"use", "deploy"}, &out, &errw); code != 3 {
		t.Fatalf("second use: %d", code)
	}
}
