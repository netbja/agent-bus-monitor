package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/dispatchsvc"
	"github.com/netbja/agent-bus-monitor/service"
)

// The CI loop's master is the services' escalation target (ci-loop §3-§5),
// through the service's own configuration: an owner-less PR's hand-off, a
// stop and a red base's directive all go to it, and a target changed after
// construction is read at the next pass. A fake gh in a scratch repository
// whose origin is a local bare one (delivery.forge github).
func TestServiceMergeQueueCIUsesTheEscalationTarget(t *testing.T) {
	b, rdb := dialMain(t)
	ctx := context.Background()
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run", "AGENT_BUS_DELIVERY_FORGE": "github",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	work := filepath.Join(root, "work")
	git(root, "init", "-q", "--bare", "origin.git")
	git(root, "init", "-q", "-b", "main", "work")
	git(work, "commit", "-q", "--allow-empty", "-m", "base")
	git(work, "remote", "add", "origin", filepath.Join(root, "origin.git"))
	git(work, "push", "-q", "origin", "main")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\ncase \"$1\" in pr) cat \""+bin+"/prs.json\" ;; api) cat \""+bin+"/base.json\" ;; esac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	put := func(name, s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	red := `{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://u","workflowName":"CI","completedAt":"2026-11-19T10:00:00Z"}`
	pr := func(n int, head string) string {
		return fmt.Sprintf(`{"number":%d,"title":"t","url":"u","headRefName":"b%d","headRefOid":%q,"baseRefName":"main","isDraft":false,"mergeable":"MERGEABLE","statusCheckRollup":[%s]}`, n, n, head, red)
	}
	base := func(nodes string) string {
		return `{"data":{"repository":{"ref":{"target":{"oid":"` + strings.Repeat("b", 40) + `","statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":false},"nodes":[` + nodes + `]}}}}}}}`
	}
	t.Chdir(work)
	paths := service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
	var cfg dispatch.Config
	if _, err := dispatchsvc.Config(serviceDispatchDeps(b, "", "lead", paths, io.Discard), &cfg); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.AcquireDispatch(ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	sent := func() map[string][]string {
		xs, err := rdb.XRange(ctx, bus.StreamKey(b.Project(), "cmd"), "-", "+").Result()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][]string{}
		for _, x := range xs {
			tg, _ := x.Values["target"].(string)
			cm, _ := x.Values["command"].(string)
			out[tg] = append(out[tg], cm)
		}
		return out
	}
	pass := func() {
		t.Helper()
		if err := cfg.MergeQueue.Pass(ctx, "tok"); err != nil {
			t.Fatal(err)
		}
		if rec, _ := b.MergeQueue(ctx); rec.Source != "gh" || rec.Reason != "" || rec.CI != "" {
			t.Fatalf("the pass: %+v", rec)
		}
	}
	// An owner-less red PR: the hand-off goes to the target.
	put("prs.json", "["+pr(7, strings.Repeat("e", 40))+"]")
	put("base.json", base(""))
	pass()
	if s := sent(); len(s["lead"]) != 1 || !strings.Contains(s["lead"][0], "CI failed on PR #7") || len(s["master"]) != 0 {
		t.Fatalf("the hand-off: %v", s)
	}
	// The target changed after construction, a new head at cap 0: the stop
	// goes to the new target.
	cfg.EscalateTo = "lead2"
	t.Setenv("AGENT_BUS_CI_MAX_HANDOFFS", "0")
	put("prs.json", "["+pr(7, strings.Repeat("f", 40))+"]")
	pass()
	if s := sent(); len(s["lead2"]) != 1 || !strings.Contains(s["lead2"][0], "CI loop stopped") {
		t.Fatalf("the stop: %v", s)
	}
	// A red base shares the PR's failure: its directive goes to the target.
	put("prs.json", "["+pr(7, strings.Repeat("f", 40))+","+pr(8, strings.Repeat("d", 40))+"]")
	put("base.json", base(red))
	pass()
	if s := sent(); len(s["lead2"]) != 2 || !strings.Contains(s["lead2"][1], "The base `main` is red") || len(s["master"]) != 0 {
		t.Fatalf("the base: %v", s)
	}
}
