package holder_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// holder_restarted against a real holder (herdr exit 6d2): an agent proved
// in a terminal of the current build's holder (its status as agentbus status
// writes it); the holder dies, every terminal with it, and a new one of the
// same build comes back empty. The evidence is what agentbus health reads:
// the socket's age, the live terminals, the agents' proofs on Redis's clock.
func TestHolderRestartedAgainstARealHolder(t *testing.T) {
	testredis.Configure(t)
	dir, err := os.MkdirTemp("", "hr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Chmod(dir, 0o700)
	getenv := func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return dir
		}
		return ""
	}
	r, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	project := "r" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(r, project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = bus.DeleteProject(context.Background(), r, project)
		r.Close()
	})
	ctx := context.Background()
	const build = "v9"
	key := service.BuildKey(build)
	sock, err := holder.SocketPath(getenv, project, key)
	if err != nil {
		t.Fatal(err)
	}
	serve := func() (stop func()) {
		s, err := holder.NewServer(holder.Config{Project: project, Build: build, Socket: sock, Grace: time.Second,
			Launch: func(holder.Start) (holder.Command, error) {
				return holder.Command{Argv: []string{"/bin/sh", "-c", "exec sleep 60"}}, nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Listen(); err != nil {
			t.Fatal(err)
		}
		sctx, cancel := context.WithCancel(context.Background())
		go s.Serve(sctx)
		once := false
		stop = func() {
			if !once {
				once = true
				cancel()
				s.Shutdown()
			}
		}
		t.Cleanup(stop)
		return stop
	}
	stop := serve()
	h, err := holder.NewHost(project, build, getenv)
	if err != nil {
		t.Fatal(err)
	}
	term, err := h.Start(ctx, termhost.Spec{Project: project, Name: "coder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Status(ctx, "coder", "idle", "", bus.AgentIdent{Pane: string(term.ID), PID: term.PID, PIDStart: term.PIDStart}); err != nil {
		t.Fatal(err)
	}
	judge := func() health.Outcome {
		t.Helper()
		info, err := os.Stat(sock)
		if err != nil {
			t.Fatal(err)
		}
		terms, err := h.List(ctx, project)
		if err != nil {
			t.Fatal(err)
		}
		cur := health.HolderBuild{Key: key, Raw: build, Current: true, Unit: true, Active: true, Answers: true,
			Terms: len(terms), Since: time.Since(info.ModTime())}
		for _, tm := range terms {
			if tm.Alive {
				_, id, _ := strings.Cut(string(tm.ID), "/")
				cur.Live = append(cur.Live, id)
			}
		}
		snaps, err := b.Agents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var proofs []health.HolderProof
		for n, s := range snaps {
			proofs = append(proofs, health.HolderProof{Name: n, Pane: s.Pane, PID: s.PID, IdentTS: s.IdentTS})
		}
		now, err := b.ServerTime(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return health.EvaluateHolder(health.WithHolderAgents(health.HolderEvidence{Known: true, Builds: []health.HolderBuild{cur}}, proofs, now))
	}
	if got := judge(); got.Status != health.OK {
		t.Fatalf("before the restart: %+v", got)
	}
	time.Sleep(1100 * time.Millisecond) // the new socket is younger than the proof
	stop()
	serve()
	got := judge()
	if got.Status != health.Warn || !strings.Contains(got.Detail, "restarted") || !strings.Contains(got.Detail, "1 agent(s) lost: coder") {
		t.Fatalf("after the restart: %+v", got)
	}
}
