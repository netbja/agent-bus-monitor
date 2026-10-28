package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const inspectOwner = `docker inspect --format {{index .Config.Labels "com.docker.compose.project.working_dir"}} agent-bus-redis`

func redisWorld(t *testing.T, open ...bool) (*Installer, *bytes.Buffer, *bytes.Buffer, *fakeRunner) {
	t.Helper()
	f := &fakeRunner{t: t, paths: map[string]string{}, cmds: map[string]fakeCmd{}}
	var out, errw bytes.Buffer
	probes := 0
	in := &Installer{R: f, O: NewOutput(&out, &errw, false), Opt: Options{StartDocker: true}, Repo: t.TempDir(), Tmp: t.TempDir(),
		Getenv: func(string) string { return "" },
		Probe: func(host string, port int) bool {
			if host != "127.0.0.1" || port != 6380 {
				t.Fatalf("probe %s:%d", host, port)
			}
			probes++
			return probes <= len(open) && open[probes-1]
		},
		Sleep: func(time.Duration) {}}
	return in, &out, &errw, f
}

func TestRedis(t *testing.T) {
	ctx := context.Background()
	up := Docker{OK: true, Compose: "docker compose"}

	in, out, _, _ := redisWorld(t, true)
	in.Redis(ctx, up)
	if out.String() != "    ✓ Redis broker: active and listening on 127.0.0.1:6380\n" {
		t.Fatalf("%q", out.String())
	}
	in, out, _, _ = redisWorld(t)
	in.Opt.StartDocker = false
	in.Redis(ctx, up)
	if out.String() != "    • Redis broker not reachable on 127.0.0.1:6380 (--no-docker: not started).\n" {
		t.Fatalf("%q", out.String())
	}
	in, out, _, _ = redisWorld(t)
	in.Redis(ctx, Docker{OK: true})
	if out.String() != "    ! Redis broker not reachable on 127.0.0.1:6380 (run 'docker compose up -d' manually in "+in.Repo+").\n" {
		t.Fatalf("%q", out.String())
	}

	// No container yet: started from this checkout, then waited for.
	in, out, _, f := redisWorld(t, false, false, true)
	f.cmds[inspectOwner] = fakeCmd{err: errors.New("1"), out: "\nError: No such object: agent-bus-redis\n"}
	f.cmds["docker compose up -d"] = fakeCmd{do: func(c Cmd) {
		if c.Dir != in.Repo {
			t.Errorf("compose ran in %q", c.Dir)
		}
	}}
	in.Redis(ctx, up)
	if out.String() != "    • Redis not detected on :6380. Starting broker container...\n    ✓ Redis broker: started successfully on 127.0.0.1:6380\n" {
		t.Fatalf("%q", out.String())
	}
	// Ours (the label is this checkout), docker-compose, never ready.
	in, out, _, f = redisWorld(t)
	f.cmds[inspectOwner] = fakeCmd{out: in.Repo + "\n"}
	f.cmds["docker-compose up -d"] = fakeCmd{}
	in.Redis(ctx, Docker{OK: true, Compose: "docker-compose"})
	if !strings.HasSuffix(out.String(), "    ! Redis container started but port 6380 is not yet responsive.\n") {
		t.Fatalf("%q", out.String())
	}
	// compose fails: its log's tail, then the warning.
	in, out, errw, f := redisWorld(t)
	f.cmds[inspectOwner] = fakeCmd{out: in.Repo + "\n"}
	f.cmds["docker compose up -d"] = fakeCmd{err: errors.New("1"), out: "port is already allocated\n"}
	in.Redis(ctx, up)
	if errw.String() != "      port is already allocated\n" || !strings.HasSuffix(out.String(), "    ! Failed to launch Redis container via docker compose up -d\n") {
		t.Fatalf("%q %q", out.String(), errw.String())
	}
	// A dry run says what it would do.
	in, out, _, f = redisWorld(t)
	in.Opt.DryRun = true
	f.cmds[inspectOwner] = fakeCmd{out: in.Repo + "\n"}
	in.Redis(ctx, up)
	if out.String() != "    • [dry-run] would start Redis container via docker compose up -d\n" {
		t.Fatalf("%q", out.String())
	}
}

// The broker another checkout owns is never recreated from this one (spec:
// Safety, layer 2): a foreign label, no label, or an unreadable owner.
func TestRedisNeverStartsAnotherCheckoutsBroker(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		inspect fakeCmd
		want    string
	}{
		{fakeCmd{out: "/data/projects/agent-bus-monitor\n"}, "    ! Redis broker: the broker belongs to /data/projects/agent-bus-monitor: start or stop it from there.\n"},
		{fakeCmd{out: "\n"}, "    ! Redis broker: the broker belongs to no checkout (agent-bus-redis has no compose label): start or stop it by hand.\n"},
		{fakeCmd{err: errors.New("1"), out: "Cannot connect to the Docker daemon\n"}, "    ! Redis broker: could not read who owns agent-bus-redis (docker inspect failed): not started.\n"},
	} {
		for _, dry := range []bool{false, true} {
			in, out, _, f := redisWorld(t)
			in.Opt.DryRun = dry
			f.cmds[inspectOwner] = c.inspect // and no compose command: running one fails the test
			in.Redis(ctx, Docker{OK: true, Compose: "docker compose"})
			if out.String() != c.want {
				t.Fatalf("dry=%v %q", dry, out.String())
			}
		}
	}
}

func TestBrokerOwner(t *testing.T) {
	in, _, _, f := redisWorld(t)
	f.cmds[inspectOwner] = fakeCmd{err: errors.New("1"), out: "Error response from daemon: No such container: agent-bus-redis\n"}
	if o, err := in.BrokerOwner(context.Background()); err != nil || o != (Owner{}) {
		t.Fatalf("%+v %v", o, err)
	}
	f.cmds[inspectOwner] = fakeCmd{out: in.Repo + "/\n"}
	if o, err := in.BrokerOwner(context.Background()); err != nil || !o.Exists || !o.Ours || o.Dir != in.Repo+"/" {
		t.Fatalf("%+v %v", o, err)
	}
}
