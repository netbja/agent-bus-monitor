package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
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

const inspectVolume = "docker volume inspect --format {{.Name}} agent-bus_redis-data"

// redis-password: install writes a random password file for a fresh broker
// only (no file, no container, no data volume, none exported), before compose
// starts it; an existing broker keeps its password; a dry run says which.
func TestRedisPasswordForAFreshBroker(t *testing.T) {
	ctx := context.Background()
	up := Docker{OK: true, Compose: "docker compose"}
	noContainer := fakeCmd{err: errors.New("1"), out: "\nError: No such object: agent-bus-redis\n"}
	noVolume := fakeCmd{err: errors.New("1"), out: "Error response from daemon: get agent-bus_redis-data: no such volume\n"}
	world := func(env map[string]string, owner, volume fakeCmd) (*Installer, *bytes.Buffer, string) {
		in, out, _, f := redisWorld(t, false, true)
		if owner.out == "ours" { // the broker's container exists and belongs to this checkout
			owner.out = in.Repo + "\n"
		}
		cfg := t.TempDir()
		env["XDG_CONFIG_HOME"] = cfg
		in.Getenv = func(k string) string { return env[k] }
		f.cmds[inspectOwner] = owner
		f.cmds[inspectVolume] = volume
		f.cmds["docker compose up -d"] = fakeCmd{}
		return in, out, filepath.Join(cfg, "agentbus", "redis.env")
	}

	// Fresh: a password written 0600 before compose up, and said so (never its value).
	in, out, path := world(map[string]string{}, noContainer, noVolume)
	in.Redis(ctx, up)
	pw, err := bus.ReadPassword(path)
	if err != nil || len(pw) < 40 {
		t.Fatalf("fresh broker: password %d chars, %v", len(pw), err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	if !strings.Contains(out.String(), "Redis password: a random one written to "+path+" (0600)") || strings.Contains(out.String(), pw) {
		t.Fatalf("%q", out.String())
	}
	if i, j := strings.Index(out.String(), "Redis password"), strings.Index(out.String(), "Starting broker"); i < 0 || j < 0 || i > j {
		t.Fatalf("the password is not written before compose starts the broker: %q", out.String())
	}

	// A second install keeps the file as it is.
	in.Redis(ctx, up)
	if again, _ := bus.ReadPassword(path); again != pw {
		t.Fatal("a second install replaced the password")
	}

	// An existing broker (its container, or only its data volume) keeps its password.
	for name, c := range map[string][2]fakeCmd{
		"its stopped container": {{out: "ours"}, noVolume},
		"its data volume":       {noContainer, {out: "agent-bus_redis-data\n"}},
	} {
		in, out, path := world(map[string]string{}, c[0], c[1])
		in.Redis(ctx, up)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: a password was written for an existing broker", name)
		}
		if !strings.Contains(out.String(), "an existing broker keeps its password") {
			t.Fatalf("%s: %q", name, out.String())
		}
	}

	// An exported REDIS_PASSWORD is what compose uses: no file.
	in, out, path = world(map[string]string{"REDIS_PASSWORD": "mine"}, noContainer, noVolume)
	in.Redis(ctx, up)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || !strings.Contains(out.String(), "REDIS_PASSWORD is exported") {
		t.Fatalf("exported: file %v, %q", err, out.String())
	}

	// reviewer-codex: a REDIS_PASSWORD in the checkout's .env is what compose
	// would start the broker with: no file that the broker would not use.
	in, out, path = world(map[string]string{}, noContainer, noVolume)
	_ = os.WriteFile(filepath.Join(in.Repo, ".env"), []byte("REDIS_PASSWORD=custom\n"), 0o600)
	in.Redis(ctx, up)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || !strings.Contains(out.String(), ".env sets REDIS_PASSWORD") {
		t.Fatalf(".env: file %v, %q", err, out.String())
	}

	// A dry run writes nothing and says what it would do.
	in, out, path = world(map[string]string{}, noContainer, noVolume)
	in.Opt.DryRun = true
	in.Redis(ctx, up)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || !strings.Contains(out.String(), "[dry-run] would write a random Redis password to "+path) {
		t.Fatalf("dry run: file %v, %q", err, out.String())
	}

	// An unreadable volume answer is not taken for a fresh broker.
	in, _, path = world(map[string]string{}, noContainer, fakeCmd{err: errors.New("1"), out: "Cannot connect to the Docker daemon\n"})
	in.Redis(ctx, up)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a password was written when the volume could not be read")
	}
}
