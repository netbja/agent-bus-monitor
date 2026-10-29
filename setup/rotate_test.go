package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/redis/go-redis/v9"
)

// redis-password, rotation: the new password is written to the file, then set
// on the live broker, then proved by a fresh connection; if either step fails,
// the broker keeps (or gets back) the old one and the file is put back exactly
// as it was. Nothing is left half-written.

type fakeSetter struct {
	set  []string
	fail map[string]error // a password whose SetPassword fails
}

func (f *fakeSetter) SetPassword(_ context.Context, pw string) error {
	f.set = append(f.set, pw)
	return f.fail[pw]
}

// dirState is the directory's files and their contents.
func dirState(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		out[e.Name()] = string(b)
	}
	return out
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestRotatePasswordRollsBack(t *testing.T) {
	ctx := context.Background()
	ok := func(context.Context, string) error { return nil }
	for _, prior := range []bool{true, false} { // an existing file, or none (the historical default)
		dir := filepath.Join(t.TempDir(), "agentbus")
		path := filepath.Join(dir, "redis.env")
		old := bus.LegacyPassword
		if prior {
			old = "old-pw"
			if err := bus.WritePassword(path, old); err != nil {
				t.Fatal(err)
			}
		}
		before := dirState(t, dir)

		// The broker refuses the new password: nothing changed, on either side.
		live := &fakeSetter{fail: map[string]error{"new-pw": errors.New("ERR unknown command 'CONFIG'")}}
		if err := RotatePassword(ctx, live, path, old, "new-pw", ok); err == nil {
			t.Fatalf("prior=%v: a refused CONFIG SET reported success", prior)
		}
		if after := dirState(t, dir); !sameFiles(before, after) {
			t.Fatalf("prior=%v: the directory changed after a refused rotation:\nbefore %v\nafter  %v", prior, before, after)
		}

		// The new password does not log in: the broker is set back, the file put back.
		live = &fakeSetter{}
		verifyFails := func(context.Context, string) error { return errors.New("WRONGPASS") }
		if err := RotatePassword(ctx, live, path, old, "new-pw", verifyFails); err == nil {
			t.Fatalf("prior=%v: a failed proof reported success", prior)
		}
		if len(live.set) != 2 || live.set[0] != "new-pw" || live.set[1] != old {
			t.Fatalf("prior=%v: broker set %v, want the new one then the old one back", prior, live.set)
		}
		if after := dirState(t, dir); !sameFiles(before, after) {
			t.Fatalf("prior=%v: the directory changed after a failed proof:\nbefore %v\nafter  %v", prior, before, after)
		}

		// Success: the file holds the new one; the old file is kept beside it.
		live = &fakeSetter{}
		if err := RotatePassword(ctx, live, path, old, "new-pw", ok); err != nil {
			t.Fatalf("prior=%v: %v", prior, err)
		}
		if pw, err := bus.ReadPassword(path); err != nil || pw != "new-pw" {
			t.Fatalf("prior=%v: file %q, %v", prior, pw, err)
		}
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Fatalf("prior=%v: mode %v", prior, st.Mode().Perm())
		}
		prev, err := bus.ReadPassword(path + ".prev")
		if prior && (err != nil || prev != old) {
			t.Fatalf("the previous file was not kept: %q, %v", prev, err)
		}
		if !prior && err == nil {
			t.Fatal("a .prev was written for the historical default")
		}
	}
}

// The same rollback against real brokers: private redis-server processes on
// Unix sockets in a temporary directory, started and killed by the test (a
// CONFIG SET changes a whole server, so the shared test broker is never
// used). Opt-in like every Redis test: AGENTBUS_TEST_REDIS_URL must be set.
func TestRotatePasswordOnADisposableRedis(t *testing.T) {
	if os.Getenv("AGENTBUS_TEST_REDIS_URL") == "" {
		t.Skip("Redis integration test: set AGENTBUS_TEST_REDIS_URL (this one starts its own disposable redis-server)")
	}
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("no redis-server on PATH")
	}
	ctx := context.Background()
	start := func(t *testing.T, extra ...string) string {
		dir := t.TempDir()
		sock := filepath.Join(dir, "r.sock")
		args := append([]string{"--port", "0", "--unixsocket", sock, "--unixsocketperm", "700", "--save", "", "--appendonly", "no", "--requirepass", "old-pw"}, extra...)
		cmd := exec.Command(server, args...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		for range 50 {
			if _, err := os.Stat(sock); err == nil {
				return sock
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("redis-server did not open its socket")
		return ""
	}
	login := func(sock, pw string) error {
		c := redis.NewClient(&redis.Options{Network: "unix", Addr: sock, Password: pw})
		defer c.Close()
		return c.Ping(ctx).Err()
	}
	dir := filepath.Join(t.TempDir(), "agentbus")
	path := filepath.Join(dir, "redis.env")
	if err := bus.WritePassword(path, "old-pw"); err != nil {
		t.Fatal(err)
	}
	before := dirState(t, dir)

	// CONFIG renamed away: CONFIG SET fails for real; the old password still logs in.
	sock := start(t, "--rename-command", "CONFIG", "")
	c := redis.NewClient(&redis.Options{Network: "unix", Addr: sock, Password: "old-pw"})
	defer c.Close()
	verify := func(ctx context.Context, pw string) error { return login(sock, pw) }
	if err := RotatePassword(ctx, RedisSetter{C: c}, path, "old-pw", "new-pw", verify); err == nil {
		t.Fatal("a broker without CONFIG reported a rotation")
	}
	if !sameFiles(before, dirState(t, dir)) || login(sock, "old-pw") != nil || login(sock, "new-pw") == nil {
		t.Fatal("after a refused CONFIG SET: the file or the broker changed")
	}

	// A broker that takes it: the new password logs in, the old one no longer.
	sock = start(t)
	c2 := redis.NewClient(&redis.Options{Network: "unix", Addr: sock, Password: "old-pw"})
	defer c2.Close()
	verify = func(ctx context.Context, pw string) error { return login(sock, pw) }
	if err := RotatePassword(ctx, RedisSetter{C: c2}, path, "old-pw", "new-pw", verify); err != nil {
		t.Fatal(err)
	}
	if login(sock, "new-pw") != nil || login(sock, "old-pw") == nil {
		t.Fatal("the broker does not hold the new password alone")
	}
	if pw, _ := bus.ReadPassword(path); pw != "new-pw" {
		t.Fatalf("file holds %q", pw)
	}

	// A proof that fails: the live broker is set back to the old password.
	sock = start(t)
	c3 := redis.NewClient(&redis.Options{Network: "unix", Addr: sock, Password: "old-pw"})
	defer c3.Close()
	if err := bus.WritePassword(path, "old-pw"); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path + ".prev")
	before = dirState(t, dir)
	if err := RotatePassword(ctx, RedisSetter{C: c3}, path, "old-pw", "new-pw", func(context.Context, string) error { return errors.New("proof refused") }); err == nil {
		t.Fatal("a failed proof reported success")
	}
	if !sameFiles(before, dirState(t, dir)) || login(sock, "old-pw") != nil {
		t.Fatal("after a failed proof: the file or the broker was not put back")
	}
}

// fakeBroker answers the dialer as a broker holding one password.
type fakeBroker struct {
	pw     string
	refuse bool // CONFIG SET fails
	dials  int
}

func (b *fakeBroker) dial(_ context.Context, pw string) (PasswordSetter, func(), error) {
	b.dials++
	if pw != b.pw {
		return nil, nil, errors.New("WRONGPASS")
	}
	return brokerSetter{b}, func() {}, nil
}

type brokerSetter struct{ b *fakeBroker }

func (s brokerSetter) SetPassword(_ context.Context, pw string) error {
	if s.b.refuse {
		return errors.New("ERR unknown command 'CONFIG'")
	}
	s.b.pw = pw
	return nil
}

const psRedisState = "docker ps -a --filter name=^agent-bus-redis$ --format {{.State}}"

// rotateWorld: this checkout's broker running, a HOME with the config dir,
// the services listed by the installed agentbus.
func rotateWorld(t *testing.T, env map[string]string) (*Installer, *bytes.Buffer, *fakeRunner, string, string) {
	t.Helper()
	home := t.TempDir()
	env["HOME"] = home
	f := &fakeRunner{t: t, paths: map[string]string{"docker": "/usr/bin/docker"}, cmds: map[string]fakeCmd{}}
	var out bytes.Buffer
	in := &Installer{R: f, O: NewOutput(&out, &out, false), Repo: t.TempDir(), Opt: Options{Sub: "rotate-redis-password", BinDir: filepath.Join(home, "bin")},
		Getenv: func(k string) string { return env[k] }}
	_ = os.MkdirAll(in.Opt.BinDir, 0o755)
	ab := filepath.Join(in.Opt.BinDir, "agentbus")
	_ = os.WriteFile(ab, []byte("#!/bin/sh\n"), 0o755)
	f.cmds[psRedisState] = fakeCmd{out: "running\n"}
	f.cmds[inspectOwner] = fakeCmd{out: in.Repo + "\n"}
	f.cmds[ab+" service list"] = fakeCmd{out: "demo                     dispatch  active (running), pid 7\ndemo                     missions  inactive (dead)\n"}
	f.cmds[ab+" --project demo service restart dispatch"] = fakeCmd{}
	f.cmds["docker compose version"] = fakeCmd{}
	f.cmds["docker compose up -d --force-recreate redis"] = fakeCmd{do: func(c Cmd) {
		if c.Dir != in.Repo {
			t.Errorf("compose ran in %q, want the checkout", c.Dir)
		}
	}}
	return in, &out, f, filepath.Join(home, ".config", "agentbus", "redis.env"), ab
}

func TestRotateRedisPasswordCommand(t *testing.T) {
	ctx := context.Background()
	yes := &Prompter{Yes: true}

	// Success from the historical default: a new file, the broker on it, the
	// running service restarted (the stopped one left alone), nothing printed of it.
	in, out, f, path, ab := rotateWorld(t, map[string]string{})
	b := &fakeBroker{pw: bus.LegacyPassword}
	if code := in.RotateRedisPassword(ctx, yes, b.dial); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	pw, err := bus.ReadPassword(path)
	if err != nil || pw != b.pw || pw == bus.LegacyPassword {
		t.Fatalf("file %q (%v), broker %q", pw, err, b.pw)
	}
	if strings.Contains(out.String(), pw) || !strings.Contains(out.String(), "restarted demo dispatch") {
		t.Fatalf("%s", out)
	}
	for _, r := range f.ran {
		if strings.Contains(r, "missions") && strings.Contains(r, "restart") {
			t.Fatal("a stopped service was restarted (started)")
		}
	}
	// foureyes: the container keeps the env_file it was created with, so the
	// broker is recreated once the new password is proved, and its services
	// restart after it: a reboot comes back on the new password.
	recreate, restart := -1, -1
	for i, r := range f.ran {
		switch {
		case r == "docker compose up -d --force-recreate redis":
			recreate = i
		case strings.HasSuffix(r, "service restart dispatch"):
			restart = i
		}
	}
	if recreate < 0 || restart < recreate {
		t.Fatalf("recreate at %d, service restart at %d: %q", recreate, restart, f.ran)
	}
	_ = ab

	// The recreate fails: the file and the running broker hold the new
	// password; the error gives the way back to a running container.
	in, out, f, path, _ = rotateWorld(t, map[string]string{})
	f.cmds["docker compose up -d --force-recreate redis"] = fakeCmd{err: errors.New("1"), out: "port is already allocated\n"}
	b = &fakeBroker{pw: bus.LegacyPassword}
	if code := in.RotateRedisPassword(ctx, yes, b.dial); code != 1 || !strings.Contains(out.String(), "docker compose up -d") || !strings.Contains(out.String(), in.Repo) {
		t.Fatalf("recreate failure: exit %d, %q", code, out)
	}
	if pw, _ := bus.ReadPassword(path); pw != b.pw {
		t.Fatal("after a failed recreate the file and the broker disagree")
	}

	// From a file: the previous one is kept beside it.
	in, out, _, path, _ = rotateWorld(t, map[string]string{})
	if err := bus.WritePassword(path, "first"); err != nil {
		t.Fatal(err)
	}
	b = &fakeBroker{pw: "first"}
	if code := in.RotateRedisPassword(ctx, yes, b.dial); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if prev, _ := bus.ReadPassword(path + ".prev"); prev != "first" || !strings.Contains(out.String(), ".prev") {
		t.Fatalf("prev %q: %s", prev, out)
	}

	// Refusals, each before anything changes.
	for _, c := range []struct {
		name string
		env  map[string]string
		prep func(in *Installer, f *fakeRunner, path string)
		b    *fakeBroker
		want string
	}{
		{"exported password", map[string]string{"REDIS_PASSWORD": "x"}, nil, &fakeBroker{pw: bus.LegacyPassword}, "REDIS_PASSWORD is exported"},
		{"exported url", map[string]string{"REDIS_URL": "redis://h"}, nil, &fakeBroker{pw: bus.LegacyPassword}, "REDIS_URL is exported"},
		{"stopped broker", map[string]string{}, func(_ *Installer, f *fakeRunner, _ string) { f.cmds[psRedisState] = fakeCmd{out: "exited\n"} }, &fakeBroker{pw: bus.LegacyPassword}, "start it first"},
		{"another checkout's broker", map[string]string{}, func(_ *Installer, f *fakeRunner, _ string) { f.cmds[inspectOwner] = fakeCmd{out: "/elsewhere\n"} }, &fakeBroker{pw: bus.LegacyPassword}, "belongs to /elsewhere"},
		{"a services env file with a password", map[string]string{}, func(in *Installer, _ *fakeRunner, _ string) {
			dir := filepath.Join(in.Getenv("HOME"), ".config", "agentbus", "services")
			_ = os.MkdirAll(dir, 0o700)
			_ = os.WriteFile(filepath.Join(dir, "shop.env"), []byte("REDIS_PASSWORD=\"x\"\n"), 0o600)
		}, &fakeBroker{pw: bus.LegacyPassword}, "shop"},
		{"a REDIS_PASSWORD in the checkout's .env", map[string]string{}, func(in *Installer, _ *fakeRunner, _ string) {
			_ = os.WriteFile(filepath.Join(in.Repo, ".env"), []byte("REDIS_PASSWORD=x\n"), 0o600)
		}, &fakeBroker{pw: bus.LegacyPassword}, ".env sets REDIS_PASSWORD"},
		{"a wrong current password", map[string]string{}, nil, &fakeBroker{pw: "something-else"}, "does not log in"},
		{"no docker compose", map[string]string{}, func(_ *Installer, f *fakeRunner, _ string) {
			f.cmds["docker compose version"] = fakeCmd{err: errors.New("1")}
		}, &fakeBroker{pw: bus.LegacyPassword}, "no docker compose"},
		{"a broker that refuses CONFIG SET", map[string]string{}, nil, &fakeBroker{pw: bus.LegacyPassword, refuse: true}, "nothing changed"},
	} {
		in, out, f, path, _ := rotateWorld(t, c.env)
		if c.prep != nil {
			c.prep(in, f, path)
		}
		before := c.b.pw
		if code := in.RotateRedisPassword(ctx, yes, c.b.dial); code != 1 || !strings.Contains(out.String(), c.want) {
			t.Errorf("%s: exit %d, %q", c.name, code, out)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || c.b.pw != before {
			t.Errorf("%s: something changed (file %v, broker %q)", c.name, err, c.b.pw)
		}
		for _, r := range f.ran {
			if strings.Contains(r, "restart") {
				t.Errorf("%s: a service was restarted", c.name)
			}
		}
	}

	// A dry run and a declined question change nothing.
	for _, c := range []struct {
		dry bool
		p   *Prompter
	}{{true, yes}, {false, &Prompter{TTY: fakeTTY{strings.NewReader("n\n"), &bytes.Buffer{}}}}} {
		in, out, _, path, _ := rotateWorld(t, map[string]string{})
		in.Opt.DryRun = c.dry
		b := &fakeBroker{pw: bus.LegacyPassword}
		if code := in.RotateRedisPassword(ctx, c.p, b.dial); code != 0 {
			t.Fatalf("dry=%v: exit %d: %s", c.dry, code, out)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || b.pw != bus.LegacyPassword {
			t.Fatalf("dry=%v: something changed: %s", c.dry, out)
		}
	}
}

func TestSetsKey(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		body string
		want bool
	}{
		{"REDIS_PASSWORD=x\n", true},
		{"REDIS_PASSWORD=\"x\"\n", true},
		{"export REDIS_PASSWORD='x'\n", true},
		{"# REDIS_PASSWORD=x\n", false}, // a comment
		{"REDIS_PASSWORD=\n", false},
		{"REDIS_PASSWORD=\"\"\n", false},
		{"OTHER=1\n", false},
	} {
		p := filepath.Join(dir, "e")
		_ = os.WriteFile(p, []byte(c.body), 0o644)
		if got := setsKey(p, "REDIS_PASSWORD"); got != c.want {
			t.Errorf("%q: %v", c.body, got)
		}
	}
	if setsKey(filepath.Join(dir, "absent"), "REDIS_PASSWORD") {
		t.Error("a missing file sets nothing")
	}
	unreadable := filepath.Join(dir, "unreadable")
	_ = os.WriteFile(unreadable, []byte("OTHER=1\n"), 0o000)
	if os.Geteuid() != 0 && !setsKey(unreadable, "REDIS_PASSWORD") {
		t.Error("an unreadable file must count as setting it (refuse rather than guess)")
	}
}

// reviewer-codex: the new password fails its proof and the broker cannot be
// set back: the broker may hold the new one, so the new file stays (with the
// previous one beside it) and the error says so; nothing is restored.
func TestRotatePasswordKeepsTheNewFileWhenTheBrokerCannotBeSetBack(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "agentbus")
	path := filepath.Join(dir, "redis.env")
	if err := bus.WritePassword(path, "old-pw"); err != nil {
		t.Fatal(err)
	}
	live := &fakeSetter{fail: map[string]error{"old-pw": errors.New("connection reset")}}
	err := RotatePassword(ctx, live, path, "old-pw", "new-pw", func(context.Context, string) error { return errors.New("WRONGPASS") })
	if err == nil {
		t.Fatal("reported success")
	}
	if pw, _ := bus.ReadPassword(path); pw != "new-pw" {
		t.Fatalf("the file holds %q: the broker may hold new-pw, so must the file", pw)
	}
	if prev, _ := bus.ReadPassword(path + ".prev"); prev != "old-pw" {
		t.Fatalf("the previous password is not kept beside it: %q", prev)
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), ".prev") {
		t.Fatalf("the error does not say where each password is: %v", err)
	}
}
