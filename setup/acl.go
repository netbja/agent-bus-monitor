package setup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// ./setup.sh acl on|off (broker-hardening spec, step 4b part 2): the
// person's act. On writes one password file per user, puts users.acl (from
// bus.ACLFile, default off) and the healthcheck's ping.pass into the
// broker's data volume, sets broker.acl=on and recreates the broker, which
// then loads the file instead of requirepass (docker-compose.yml). Off
// removes both files, recreates the broker on requirepass and sets
// broker.acl=off; the default user's password file is never touched.

// LoginFunc proves a fresh login to the local broker as user ("" = the
// default user) with pw.
type LoginFunc func(ctx context.Context, user, pw string) error

// ACL is ./setup.sh acl on (on) or off. refuse is the agent refusal
// (AGENT_BUS_AGENT, a registered agent among the ancestors): it runs first,
// before any password file is read.
func (in *Installer) ACL(ctx context.Context, p *Prompter, on bool, refuse func(context.Context) string, login LoginFunc) int {
	o := in.O
	state := map[bool]string{true: "on", false: "off"}[on]
	if why := refuse(ctx); why != "" {
		o.Fatal(why)
		return 1
	}
	key, _ := config.Lookup("broker.acl")
	for _, k := range []string{"REDIS_URL", "REDIS_PASSWORD", key.Env} {
		if in.Getenv(k) != "" {
			o.Fatal(k + " is exported in this shell: it wins over the files this changes. Unset it (and remove it from your shell profile), then run this again.")
			return 1
		}
	}
	defPath, cfg := bus.PasswordFile(in.Getenv), config.Path(in.Getenv)
	if defPath == "" || cfg == "" {
		o.Fatal("no HOME: there is nowhere to keep the password files")
		return 1
	}
	// off is also the way back from a broker that does not start on its
	// users.acl (foureyes): it needs neither a running broker nor a login.
	if st := in.RedisState(ctx); on && st != "running" {
		o.Fatal("the Redis broker is " + st + ": start it first (./setup.sh install)")
		return 1
	}
	if ow, err := in.BrokerOwner(ctx); err != nil || !ow.Ours {
		where := "no checkout"
		if err != nil {
			where = "an owner that could not be read"
		} else if ow.Dir != "" {
			where = ow.Dir
		}
		o.Fatal("the broker belongs to " + where + ": switch its ACL from there")
		return 1
	}
	compose := ComposeCmd(ctx, in.R, in.runtime())
	if compose == "" {
		o.Fatal("no " + in.runtime().Compose() + " to recreate the broker with: install it first")
		return 1
	}
	def, err := bus.ReadPassword(defPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		def = bus.LegacyPassword
	case err != nil:
		o.Fatal(err.Error())
		return 1
	}
	// The current broker must log in, as it is now: the default user, or
	// admin when its ACL is on.
	if on && login(ctx, "", def) != nil {
		adm, aerr := bus.ReadPassword(bus.UserPasswordFile(in.Getenv, bus.ACLAdmin))
		if aerr != nil || login(ctx, bus.ACLAdmin, adm) != nil {
			o.Fatal("the broker's current password does not log in (neither the default user's nor admin's): nothing changed")
			return 1
		}
		// Its ACL is live: on would replace the password files its users
		// log in with (reviewer-codex). Remaking the users is off, then on.
		o.Fatal("the broker's ACL users are already on: nothing changed. To remake them, run ./setup.sh acl off, then ./setup.sh acl on")
		return 1
	}
	if in.Opt.DryRun {
		if on {
			o.Info("[dry-run] would write a password file per broker user (0600), put users.acl and the healthcheck's ping password in the broker's data volume, set broker.acl=on, recreate the broker from " + in.Repo + " and restart the running project services")
		} else {
			o.Info("[dry-run] would remove users.acl from the broker's data volume, recreate the broker on its default user's password, set broker.acl=off and restart the running project services")
		}
		return 0
	}
	if !p.Yes {
		yes, err := Ask(o, p, "turn the Redis broker's ACL users "+state+"? (the broker is recreated, busmon must be reopened)", false)
		if err != nil || !yes {
			o.Info("broker ACL: left as it is")
			return 0
		}
	}
	if on {
		return in.aclOn(ctx, compose, cfg, def, login)
	}
	return in.aclOff(ctx, compose, cfg, def, login)
}

func (in *Installer) aclOn(ctx context.Context, compose, cfg, def string, login LoginFunc) int {
	o := in.O
	pw := map[string]string{}
	for _, role := range bus.ACLRoles {
		p, err := bus.NewPassword()
		if err == nil {
			err = bus.WritePassword(bus.UserPasswordFile(in.Getenv, role), p)
		}
		if err != nil {
			o.Fatal("broker ACL: " + role + "'s password file: " + err.Error())
			return 1
		}
		pw[role] = p
	}
	acl, err := bus.ACLFile(pw)
	if err != nil {
		o.Fatal("broker ACL: " + err.Error())
		return 1
	}
	// ping.pass first: users.acl is the switch the compose file reads.
	if err := in.toVolume(ctx, "ping.pass", []byte(pw[bus.ACLPing])); err != nil {
		in.aclUndo(ctx, compose)
		o.Fatal("broker ACL: " + err.Error() + "; nothing changed")
		return 1
	}
	if err := in.toVolume(ctx, "users.acl", acl); err != nil {
		in.aclUndo(ctx, compose)
		o.Fatal("broker ACL: " + err.Error() + "; nothing changed")
		return 1
	}
	// The proof, then the setting (foureyes): admin logs in, the default user
	// is refused; broker.acl=on only once the broker holds its users.
	why := ""
	switch {
	case in.recreate(ctx, compose) != nil:
		why = "the broker could not be recreated"
	case !in.waitUser(ctx, login, bus.ACLAdmin, pw[bus.ACLAdmin]):
		why = "admin does not log in to the recreated broker (did it start on users.acl?)"
	case login(ctx, "", def) == nil:
		why = "the recreated broker still lets the default user in"
	default:
		if err := config.Set(cfg, "broker.acl", "on"); err != nil {
			why = cfg + " could not be set (" + err.Error() + ")"
		}
	}
	if why != "" {
		// Put back: the volume emptied without the broker's container (it
		// may be restarting on a refused file), the broker recreated.
		if in.aclUndo(ctx, compose) && in.recreate(ctx, compose) == nil && in.waitUser(ctx, login, "", def) {
			o.Fatal("broker ACL: " + why + "; everything is put back (broker.acl off, the broker on its default user)")
		} else {
			o.Fatal("broker ACL: " + why + ", and putting it back failed: run ./setup.sh acl off (it works on a broker that is down), then check it with " + compose + " ps in " + in.Repo)
		}
		return 1
	}
	o.OK("broker ACL: on; a password file per user beside " + bus.PasswordFile(in.Getenv) + " (0600), the default user off")
	code := in.restartRunningServices(ctx)
	o.Info("reopen busmon; a client on another machine (SSH tunnel) needs its user's password file")
	return code
}

func (in *Installer) aclOff(ctx context.Context, compose, cfg, def string, login LoginFunc) int {
	o := in.O
	if !in.aclUndo(ctx, compose) {
		o.Fatal("broker ACL: users.acl could not be removed from the broker's data volume; nothing changed")
		return 1
	}
	if in.recreate(ctx, compose) != nil || !in.waitUser(ctx, login, "", def) {
		o.Fatal("broker ACL: the broker recreated without users.acl does not log in with " + bus.PasswordFile(in.Getenv) + "; check it with " + compose + " ps in " + in.Repo)
		return 1
	}
	if err := config.Set(cfg, "broker.acl", "off"); err != nil {
		o.Fatal("broker ACL: the broker is off its users, but " + cfg + " could not be set (" + err.Error() + "): set broker.acl = \"off\" there")
		return 1
	}
	o.OK("broker ACL: off; the broker is back on its default user's password (" + bus.PasswordFile(in.Getenv) + ")")
	code := in.restartRunningServices(ctx)
	o.Info("reopen busmon")
	return code
}

// toVolume copies data into the broker's data volume as /data/name, 0600,
// through a staged file removed after the copy.
func (in *Installer) toVolume(ctx context.Context, name string, data []byte) error {
	dir := in.Tmp
	if dir == "" {
		d, err := os.MkdirTemp("", "agentbus-acl-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(d)
		dir = d
	}
	src := filepath.Join(dir, name)
	if err := writeSecret(src, data); err != nil {
		return err
	}
	defer os.Remove(src)
	rt := in.runtime().Bin()
	if out, err := in.R.Run(ctx, Cmd{Name: rt, Args: []string{"cp", src, RedisContainer + ":/data/" + name}}); err != nil {
		showTail(in.O.err, out)
		return errors.New("could not copy " + name + " into the broker: " + err.Error())
	}
	return nil
}

// aclUndo removes users.acl and ping.pass from the data volume, through a
// one-off container on the same volume: it works with the broker down or
// restarting on a file it refuses (foureyes), where an exec cannot.
func (in *Installer) aclUndo(ctx context.Context, compose string) bool {
	name, args := composeArgs(compose, "run", "--rm", "--no-deps", "--entrypoint", "rm", "redis", "-f", "/data/users.acl", "/data/ping.pass")
	out, err := in.R.Run(ctx, Cmd{Name: name, Args: args, Dir: in.Repo, Env: in.composeEnv()})
	if err != nil {
		showTail(in.O.err, out)
	}
	return err == nil
}

// recreate recreates the broker's container from this checkout, with the
// same image (never a Valkey broker recreated as Redis); a failure's output
// tail is shown.
func (in *Installer) recreate(ctx context.Context, compose string) error {
	name, args := composeArgs(compose, "up", "-d", "--force-recreate", "redis")
	out, err := in.R.Run(ctx, Cmd{Name: name, Args: args, Dir: in.Repo, Env: in.composeEnv()})
	if err != nil {
		showTail(in.O.err, out)
	}
	return err
}

// waitUser waits up to 30 s for a login as user with pw.
func (in *Installer) waitUser(ctx context.Context, login LoginFunc, user, pw string) bool {
	for range 30 {
		if login(ctx, user, pw) == nil {
			return true
		}
		in.sleep(time.Second)
	}
	return false
}
