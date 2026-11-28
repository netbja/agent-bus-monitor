package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/redis/go-redis/v9"
)

// foureyes on observer-role: with broker.acl on, every read the observer
// guard allows must run as the observer user. Each read the CLI does, by its
// own code, against a disposable redis-server loaded with ACLFile's users,
// connected as observer: a category gap (TIME, a read script) fails here.
func TestObserverReadsUnderTheACL(t *testing.T) {
	if os.Getenv("AGENTBUS_TEST_REDIS_URL") == "" {
		t.Skip("Redis integration test: set AGENTBUS_TEST_REDIS_URL (this one starts its own disposable redis-server)")
	}
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("no redis-server on PATH")
	}
	pw := map[string]string{}
	for _, r := range bus.ACLRoles {
		pw[r] = "pw-of-" + r
	}
	data, err := bus.ACLFile(pw)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	aclfile := filepath.Join(dir, "users.acl")
	if err := os.WriteFile(aclfile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "r.sock")
	cmd := exec.Command(server, "--port", "0", "--unixsocket", sock, "--unixsocketperm", "700", "--save", "", "--appendonly", "no", "--aclfile", aclfile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx := context.Background()
	as := func(user string) *bus.Bus {
		c := redis.NewClient(&redis.Options{Network: "unix", Addr: sock, Username: user, Password: pw[user]})
		t.Cleanup(func() { c.Close() })
		b, err := bus.Open(c, "demo")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// Something to read, written as master.
	m := as(bus.ACLMaster)
	if _, err := m.Status(ctx, "coder", "working", "task 3", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Request(ctx, "t1", "master", "coder", "", "build it", 0); err != nil {
		t.Fatal(err)
	}
	rep, err := m.Report(ctx, "coder", "report", "done")
	if err != nil {
		t.Fatal(err)
	}
	o := as(bus.ACLObserver)
	var out, errw bytes.Buffer
	for name, read := range map[string]func() error{
		"board":   func() error { _, err := o.Board(ctx); return err },
		"request": func() error { return runRequest(ctx, o, "observer", nil, &out) },
		"agents": func() error {
			if _, err := o.Agents(ctx); err != nil {
				return err
			}
			_, err := o.ArmedAgents(ctx)
			return err
		},
		"reports":      func() error { _, err := o.Reports(ctx, 25); return err },
		"reports <id>": func() error { _, err := o.ReportByID(ctx, rep); return err },
		"reports --unread": func() error {
			evs, err := o.Reports(ctx, 25)
			if err != nil {
				return err
			}
			_, seenErr, err := readNoticeStates(ctx, o, evs)
			if err == nil {
				err = seenErr
			}
			return err
		},
		"thread":   func() error { _, err := o.Thread(ctx, rep); return err },
		"verdicts": func() error { _, err := o.Verdicts(ctx, ""); return err },
		"asks":     func() error { return exitErr(runAsks(ctx, o, nil, &out, &errw)) },
		"swaps":    func() error { return exitErr(runSwaps(ctx, o, nil, &out, &errw)) },
		"usage":    func() error { _, err := o.Usage(ctx); return err },
		"budget":   func() error { _, err := o.Budgets(ctx); return err },
		"merge-queue": func() error { // its exit 2 says "no record yet": the reads themselves
			if _, err := o.MergeQueue(ctx); err != nil {
				return err
			}
			_, err := o.DroppedHeads(ctx)
			return err
		},
		"summary": func() error { // status-summary §3.1: read only, as observer
			return exitErr(runSummary(ctx, "demo", []string{"--json"}, summaryDeps{Now: time.Now,
				Open: func(string) (*bus.Bus, error) { return o, nil }}, &out, &errw))
		},
		"health":       func() error { return exitErr(runHealth(ctx, o, "observer", nil, &out, &errw)) },
		"health warns": func() error { return exitErr(runHealth(ctx, o, "observer", []string{"warns"}, &out, &errw)) },
		"health log":   func() error { return exitErr(runHealth(ctx, o, "observer", []string{"log"}, &out, &errw)) },
	} {
		errw.Reset()
		if err := read(); err != nil {
			t.Errorf("%s as observer: %v %s", name, err, errw.String())
		}
	}
	// And the user writes nothing, through the bus's own writers.
	if _, err := o.Status(ctx, "observer", "idle", "", bus.AgentIdent{}); err == nil {
		t.Error("observer wrote a status")
	}
	if _, err := o.Report(ctx, "observer", "report", "x"); err == nil {
		t.Error("observer wrote a report")
	}
}

// exitErr is a run function's exit code as an error (0: none).
func exitErr(code int) error {
	if code != 0 {
		return fmt.Errorf("exit %d", code)
	}
	return nil
}
