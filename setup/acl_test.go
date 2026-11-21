package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// aclBroker is the broker as ./setup.sh acl sees it: the files put in its
// data volume, loaded when it is recreated; a login by user and password.
type aclBroker struct {
	defaultPW    string
	volume       map[string][]byte // /data/<name>
	live         []byte            // users.acl loaded at the last recreate; nil = requirepass
	failRecreate bool
	logins       int
}

func (b *aclBroker) login(_ context.Context, user, pw string) error {
	b.logins++
	if b.live == nil {
		if user == "" && pw == b.defaultPW {
			return nil
		}
		return errors.New("WRONGPASS")
	}
	sum := sha256.Sum256([]byte(pw))
	for _, l := range strings.Split(string(b.live), "\n") {
		if strings.HasPrefix(l, "user "+user+" ") && strings.Contains(l, " on #"+hex.EncodeToString(sum[:])+" ") {
			return nil
		}
	}
	return errors.New("WRONGPASS")
}

const (
	aclCpUsers = "docker cp %s/users.acl agent-bus-redis:/data/users.acl"
	aclCpPing  = "docker cp %s/ping.pass agent-bus-redis:/data/ping.pass"
	aclRm      = "docker exec agent-bus-redis rm -f /data/users.acl /data/ping.pass"
	aclUp      = "docker compose up -d --force-recreate redis"
)

// aclWorld: this checkout's broker running on the historical default
// password, its volume and recreate modelled by b.
func aclWorld(t *testing.T, env map[string]string) (*Installer, *bytes.Buffer, *fakeRunner, *aclBroker) {
	t.Helper()
	in, out, f, _, _ := rotateWorld(t, env)
	in.Opt.Sub = "acl"
	in.Tmp = t.TempDir()
	in.Sleep = func(time.Duration) {}
	b := &aclBroker{defaultPW: bus.LegacyPassword, volume: map[string][]byte{}}
	cp := func(name string) fakeCmd {
		return fakeCmd{do: func(Cmd) {
			data, err := os.ReadFile(filepath.Join(in.Tmp, name))
			if err != nil {
				t.Fatalf("cp of a missing %s: %v", name, err)
			}
			if st, _ := os.Stat(filepath.Join(in.Tmp, name)); st.Mode().Perm() != 0o600 {
				t.Errorf("%s copied with mode %v", name, st.Mode().Perm())
			}
			b.volume[name] = data
		}}
	}
	f.cmds[sprintf(aclCpUsers, in.Tmp)] = cp("users.acl")
	f.cmds[sprintf(aclCpPing, in.Tmp)] = cp("ping.pass")
	f.cmds[aclRm] = fakeCmd{do: func(Cmd) { delete(b.volume, "users.acl"); delete(b.volume, "ping.pass") }}
	f.cmds[aclUp] = fakeCmd{do: func(Cmd) {
		if b.failRecreate {
			return
		}
		b.live = b.volume["users.acl"]
	}}
	return in, out, f, b
}

func sprintf(format, s string) string { return strings.Replace(format, "%s", s, 1) }

func aclSetting(t *testing.T, in *Installer) string {
	t.Helper()
	return config.Machine(in.Getenv).String("broker.acl")
}

func noRefusal(context.Context) string { return "" }

func TestACLOnThenOff(t *testing.T) {
	ctx := context.Background()
	yes := &Prompter{Yes: true}
	in, out, f, b := aclWorld(t, map[string]string{})
	if code := in.ACL(ctx, yes, true, noRefusal, b.login); code != 0 {
		t.Fatalf("on: exit %d: %s", code, out)
	}
	// one password file per user, 0600, each logging in as its user
	for _, role := range bus.ACLRoles {
		path := bus.UserPasswordFile(in.Getenv, role)
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", role, err, st)
		}
		pw, err := bus.ReadPassword(path)
		if err != nil || b.login(ctx, role, pw) != nil {
			t.Errorf("%s does not log in with its file (%v)", role, err)
		}
		if strings.Contains(out.String(), pw) {
			t.Errorf("%s's password printed", role)
		}
	}
	ping, _ := bus.ReadPassword(bus.UserPasswordFile(in.Getenv, bus.ACLPing))
	if string(b.volume["ping.pass"]) != ping {
		t.Error("the healthcheck's ping.pass is not ping's password")
	}
	if !strings.HasSuffix(string(b.live), "user default off\n") || b.login(ctx, "", bus.LegacyPassword) == nil {
		t.Error("the default user still logs in")
	}
	if got := aclSetting(t, in); got != "on" {
		t.Fatalf("broker.acl = %q", got)
	}
	if _, err := os.Stat(filepath.Join(in.Tmp, "users.acl")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the staged users.acl was left behind")
	}
	recreate, restart := -1, -1
	for i, r := range f.ran {
		switch {
		case r == aclUp:
			recreate = i
		case strings.HasSuffix(r, "service restart dispatch"):
			restart = i
		}
	}
	if recreate < 0 || restart < recreate {
		t.Fatalf("recreate at %d, service restart at %d: %q", recreate, restart, f.ran)
	}

	// off gives back a working default user, its file kept.
	out.Reset()
	if code := in.ACL(ctx, yes, false, noRefusal, b.login); code != 0 {
		t.Fatalf("off: exit %d: %s", code, out)
	}
	if b.live != nil || len(b.volume) != 0 || b.login(ctx, "", bus.LegacyPassword) != nil {
		t.Fatalf("off: the broker is still on its ACL file (%d files in the volume)", len(b.volume))
	}
	if got := aclSetting(t, in); got != "off" {
		t.Fatalf("broker.acl = %q after off", got)
	}
}

// Refusals come before anything is read or changed: no password file, no
// login, no command.
func TestACLRefusals(t *testing.T) {
	ctx := context.Background()
	yes := &Prompter{Yes: true}
	key, _ := config.Lookup("broker.acl")
	for _, c := range []struct {
		name   string
		env    map[string]string
		refuse func(context.Context) string
		prep   func(in *Installer, f *fakeRunner, b *aclBroker)
		want   string
	}{
		{"an agent's process tree", map[string]string{}, func(context.Context) string { return "this runs under the agent p/coder" }, nil, "p/coder"},
		{"broker.acl exported", map[string]string{key.Env: "off"}, noRefusal, nil, key.Env + " is exported"},
		{"exported password", map[string]string{"REDIS_PASSWORD": "x"}, noRefusal, nil, "REDIS_PASSWORD is exported"},
		{"stopped broker", map[string]string{}, noRefusal, func(_ *Installer, f *fakeRunner, _ *aclBroker) { f.cmds[psRedisState] = fakeCmd{out: "exited\n"} }, "start it first"},
		{"another checkout's broker", map[string]string{}, noRefusal, func(_ *Installer, f *fakeRunner, _ *aclBroker) { f.cmds[inspectOwner] = fakeCmd{out: "/elsewhere\n"} }, "belongs to /elsewhere"},
		{"a wrong current password", map[string]string{}, noRefusal, func(_ *Installer, _ *fakeRunner, b *aclBroker) { b.defaultPW = "other" }, "does not log in"},
	} {
		in, out, f, b := aclWorld(t, c.env)
		if c.prep != nil {
			c.prep(in, f, b)
		}
		ran := len(f.ran)
		if code := in.ACL(ctx, yes, true, c.refuse, b.login); code != 1 || !strings.Contains(out.String(), c.want) {
			t.Errorf("%s: exit %d, %q", c.name, code, out)
		}
		for _, role := range bus.ACLRoles {
			if _, err := os.Stat(bus.UserPasswordFile(in.Getenv, role)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s: %s's password file written", c.name, role)
			}
		}
		if len(b.volume) != 0 || aclSetting(t, in) != "off" {
			t.Errorf("%s: the broker or the setting changed", c.name)
		}
		if c.name == "an agent's process tree" && (len(f.ran) != ran || b.logins != 0) {
			t.Errorf("%s: ran %q, %d logins before refusing", c.name, f.ran[ran:], b.logins)
		}
	}
}

// The recreated broker does not take the users: everything is put back,
// the setting off, the volume emptied, the broker recreated on requirepass.
func TestACLOnRollsBack(t *testing.T) {
	ctx := context.Background()
	in, out, f, b := aclWorld(t, map[string]string{})
	b.failRecreate = true
	if code := in.ACL(ctx, &Prompter{Yes: true}, true, noRefusal, b.login); code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	b.failRecreate = false
	if aclSetting(t, in) != "off" || len(b.volume) != 0 {
		t.Fatalf("not put back: setting %q, volume %d files", aclSetting(t, in), len(b.volume))
	}
	n := 0
	for _, r := range f.ran {
		if r == aclUp {
			n++
		}
		if strings.Contains(r, "service restart") {
			t.Error("services restarted after a failed switch")
		}
	}
	if n != 2 || !strings.Contains(out.String(), "put back") {
		t.Fatalf("%d recreates: %s", n, out)
	}
}

// A dry run and a declined question change nothing.
func TestACLDryRunAndDecline(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		dry bool
		p   *Prompter
	}{{true, &Prompter{Yes: true}}, {false, &Prompter{TTY: fakeTTY{strings.NewReader("n\n"), &bytes.Buffer{}}}}} {
		in, out, f, b := aclWorld(t, map[string]string{})
		in.Opt.DryRun = c.dry
		if code := in.ACL(ctx, c.p, true, noRefusal, b.login); code != 0 {
			t.Fatalf("dry=%v: exit %d: %s", c.dry, code, out)
		}
		for _, r := range f.ran {
			if strings.Contains(r, " cp ") || r == aclUp {
				t.Errorf("dry=%v: ran %s", c.dry, r)
			}
		}
		if _, err := os.Stat(bus.UserPasswordFile(in.Getenv, bus.ACLAdmin)); !errors.Is(err, fs.ErrNotExist) || aclSetting(t, in) != "off" {
			t.Errorf("dry=%v: something changed", c.dry)
		}
	}
}
