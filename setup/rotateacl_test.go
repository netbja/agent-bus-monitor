package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// dial is the usersBroker as the rotation's admin connection.
func (b *usersBroker) dial(ctx context.Context, user, pw string) (ACLUsers, func(), error) {
	if err := b.login(ctx, user, pw); err != nil {
		return nil, nil, err
	}
	return b, func() {}, nil
}

// noDefault fails the test if the rotation dials the default user.
func noDefault(t *testing.T) Dialer {
	return func(context.Context, string) (PasswordSetter, func(), error) {
		t.Fatal("the default user was dialled with broker.acl on")
		return nil, nil, nil
	}
}

// rotateACLWorld: rotateWorld with broker.acl=on, a password file per user
// that the broker holds, and ping.pass's copy into the volume recorded.
func rotateACLWorld(t *testing.T, env map[string]string) (*Installer, *bytes.Buffer, *fakeRunner, *usersBroker, *[]byte) {
	t.Helper()
	in, out, f, _, _ := rotateWorld(t, env)
	in.Tmp = t.TempDir()
	if err := config.Set(config.Path(in.Getenv), "broker.acl", "on"); err != nil {
		t.Fatal(err)
	}
	b := &usersBroker{live: map[string]string{}, saved: map[string]string{}}
	for _, u := range bus.ACLRoles {
		if err := bus.WritePassword(bus.UserPasswordFile(in.Getenv, u), "old-"+u); err != nil {
			t.Fatal(err)
		}
		b.live[u], b.saved[u] = "old-"+u, "old-"+u
	}
	var ping []byte
	f.cmds[sprintf(aclCpPing, in.Tmp)] = fakeCmd{do: func(Cmd) {
		data, err := os.ReadFile(filepath.Join(in.Tmp, "ping.pass"))
		if err != nil {
			t.Fatalf("cp of a missing ping.pass: %v", err)
		}
		ping = data
	}}
	return in, out, f, b, &ping
}

// broker-hardening step 4b part 3: with broker.acl on, rotate-redis-password
// rotates each ACL user as admin (admin last, ACL SAVE), writes ping's new
// password into the volume for the healthcheck, never touches the default
// user, needs no recreate (users.acl is in the volume), and restarts the
// running services.
func TestRotateRedisPasswordWithACLOn(t *testing.T) {
	in, out, f, b, ping := rotateACLWorld(t, map[string]string{})
	if code := in.RotateRedisPassword(context.Background(), &Prompter{Yes: true}, noDefault(t), b.dial); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, u := range bus.ACLRoles {
		pw := fileOf(t, in.Getenv, u)
		if pw == "old-"+u || b.live[u] != pw || b.saved[u] != pw {
			t.Errorf("%s: file %q, live %q, saved %q", u, pw, b.live[u], b.saved[u])
		}
		if strings.Contains(out.String(), pw) {
			t.Fatalf("a password printed: %s", out)
		}
	}
	if b.order[len(b.order)-1] != bus.ACLAdmin {
		t.Errorf("order %v", b.order)
	}
	if string(*ping) != b.live[bus.ACLPing] {
		t.Errorf("ping.pass %q, ping's password %q", *ping, b.live[bus.ACLPing])
	}
	for _, r := range f.ran {
		if strings.Contains(r, "force-recreate") {
			t.Errorf("recreated: %q", f.ran)
		}
	}
	if !strings.Contains(out.String(), "restarted demo dispatch") {
		t.Fatalf("%s", out)
	}
	if _, err := os.Stat(bus.PasswordFile(in.Getenv)); err == nil {
		t.Error("the default user's password file was written")
	}
}

// Refusals and the dry run change nothing: no user rotated, no copy.
func TestRotateRedisPasswordWithACLOnChangesNothing(t *testing.T) {
	key, _ := config.Lookup("broker.acl")
	for _, c := range []struct {
		name string
		env  map[string]string
		set  func(in *Installer, b *usersBroker)
		code int
		want string
	}{
		{"admin refused", map[string]string{}, func(_ *Installer, b *usersBroker) { b.live[bus.ACLAdmin] = "other" }, 1, "admin's password does not log in"},
		{"admin file missing", map[string]string{}, func(in *Installer, _ *usersBroker) {
			_ = os.Remove(bus.UserPasswordFile(in.Getenv, bus.ACLAdmin))
		}, 1, "admin's password file"},
		{"broker.acl exported", map[string]string{key.Env: "on"}, nil, 1, key.Env + " is exported"},
		{"dry run", map[string]string{}, func(in *Installer, _ *usersBroker) { in.Opt.DryRun = true }, 0, "[dry-run] would write a new random password for each broker user"},
		{"declined", map[string]string{}, nil, 0, "left as they are"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in, out, _, b, ping := rotateACLWorld(t, c.env)
			if c.set != nil {
				c.set(in, b)
			}
			p := &Prompter{Yes: c.name != "declined", TTY: &bytes.Buffer{}}
			if code := in.RotateRedisPassword(context.Background(), p, noDefault(t), b.dial); code != c.code || !strings.Contains(out.String(), c.want) {
				t.Fatalf("exit %d: %s", code, out)
			}
			if len(b.order) != 0 || *ping != nil {
				t.Fatalf("changed: %v, ping %q", b.order, *ping)
			}
		})
	}
}

// ping's copy cannot be written: the rotation says the healthcheck shows the
// broker unhealthy until it is.
func TestRotateRedisPasswordWithACLOnPingCopyFails(t *testing.T) {
	in, out, f, b, _ := rotateACLWorld(t, map[string]string{})
	f.cmds[sprintf(aclCpPing, in.Tmp)] = fakeCmd{err: errors.New("1"), out: "no such container\n"}
	if code := in.RotateRedisPassword(context.Background(), &Prompter{Yes: true}, noDefault(t), b.dial); code != 1 || !strings.Contains(out.String(), "healthcheck") {
		t.Fatalf("exit %d: %s", code, out)
	}
}

const aclProbe = "docker exec agent-bus-redis sh -c if [ -f /data/users.acl ]; then echo yes; else echo no; fi"

// setup status: broker.acl and whether the volume holds users.acl, with a
// warning when they disagree (each way locks the clients out).
func TestStatusACLLine(t *testing.T) {
	for _, c := range []struct {
		setting, state string
		probe          fakeCmd
		want           string
	}{
		{"on", "running", fakeCmd{out: "yes\n"}, "✓ broker ACL: on (users.acl in the broker's data volume"},
		{"off", "running", fakeCmd{out: "no\n"}, "• broker ACL: off"},
		{"on", "running", fakeCmd{out: "no\n"}, "! broker ACL: broker.acl is on, but the broker's data volume holds no users.acl"},
		{"off", "running", fakeCmd{out: "yes\n"}, "! broker ACL: broker.acl is off, but the broker's data volume holds users.acl"},
		{"on", "running", fakeCmd{err: errors.New("1")}, "! broker ACL: broker.acl is on; users.acl in the broker's data volume could not be checked"},
		{"on", "stopped", fakeCmd{}, "• broker ACL: broker.acl is on (the broker is stopped: its users.acl is not checked)"},
	} {
		in, out, f, _, _ := rotateWorld(t, map[string]string{})
		if err := config.Set(config.Path(in.Getenv), "broker.acl", c.setting); err != nil {
			t.Fatal(err)
		}
		f.cmds[aclProbe] = c.probe
		in.aclLine(context.Background(), c.state == "running")
		if got := strings.TrimSpace(out.String()); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s %s %+v: %q, want %q…", c.setting, c.state, c.probe, got, c.want)
		}
		if c.state != "running" && len(f.ran) != 0 {
			t.Errorf("probed a stopped broker: %q", f.ran)
		}
	}
}

// uninstall --purge removes the volume and its users.acl: broker.acl is set
// back to off, or the next broker's clients would log in as users it does
// not have. A plain stop keeps both.
func TestUninstallPurgeSetsTheACLOff(t *testing.T) {
	for _, purge := range []bool{true, false} {
		in, out, f, _ := uninstallWorld(t)
		in.Opt.Purge = purge
		f.cmds["docker compose down -v"] = fakeCmd{}
		f.cmds["docker compose stop"] = fakeCmd{}
		if err := config.Set(config.Path(in.Getenv), "broker.acl", "on"); err != nil {
			t.Fatal(err)
		}
		in.Uninstall(context.Background(), &Prompter{Yes: true})
		got := config.Machine(in.Getenv).String("broker.acl")
		if purge && (got != "off" || !strings.Contains(out.String(), "broker.acl: set back to off")) {
			t.Fatalf("purge: broker.acl %q, %s", got, out)
		}
		if !purge && got != "on" {
			t.Fatalf("stop: broker.acl %q", got)
		}
	}
	// The purge fails: the setting is kept.
	in, _, f, _ := uninstallWorld(t)
	in.Opt.Purge = true
	f.cmds["docker compose down -v"] = fakeCmd{err: errors.New("1")}
	_ = config.Set(config.Path(in.Getenv), "broker.acl", "on")
	in.Uninstall(context.Background(), &Prompter{Yes: true})
	if got := config.Machine(in.Getenv).String("broker.acl"); got != "on" {
		t.Fatalf("failed purge: broker.acl %q", got)
	}
}
