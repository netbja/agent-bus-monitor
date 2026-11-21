package setup

import (
	"context"
	"fmt"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/redis/go-redis/v9"
)

// The rotation with the broker's ACL users on (broker-hardening spec, step
// 4b part 3): each user's password replaced with ACL SETUSER <user>
// resetpass ><new>, its file through RotatePassword (the previous one kept
// as .prev, put back on a refusal or a failed proof), admin last, since the
// connection that rotates them is admin's and stays logged in; ACL SAVE
// after each user writes users.acl, so a restart keeps them.

// ACLUsers changes the live broker's users.
type ACLUsers interface {
	SetUserPassword(ctx context.Context, user, pw string) error
	SaveACL(ctx context.Context) error
}

// RedisUsers changes them on an admin connection.
type RedisUsers struct{ C *redis.Client }

func (r RedisUsers) SetUserPassword(ctx context.Context, user, pw string) error {
	return r.C.Do(ctx, "ACL", "SETUSER", user, "resetpass", ">"+pw).Err()
}

func (r RedisUsers) SaveACL(ctx context.Context) error { return r.C.Do(ctx, "ACL", "SAVE").Err() }

// userSetter is one user of ACLUsers as RotatePassword's PasswordSetter.
type userSetter struct {
	b    ACLUsers
	user string
}

func (s userSetter) SetPassword(ctx context.Context, pw string) error {
	return s.b.SetUserPassword(ctx, s.user, pw)
}

// RotateUsers rotates every user of bus.ACLRoles, admin last, with ACL SAVE
// after each. Every file is read before anything changes. A user that fails
// stops the rotation: it and the users after it keep their passwords, the
// ones before stay rotated and are saved; a failed save stops it too. login proves a fresh login as user; after
// receives ping's new password (the healthcheck's copy in the broker's
// data volume) once the broker holds it.
func RotateUsers(ctx context.Context, b ACLUsers, getenv func(string) string, login func(ctx context.Context, user, pw string) error, after func(user, pw string) error) error {
	order := make([]string, 0, len(bus.ACLRoles))
	for _, u := range bus.ACLRoles {
		if u != bus.ACLAdmin {
			order = append(order, u)
		}
	}
	order = append(order, bus.ACLAdmin)
	old := map[string]string{}
	for _, u := range order {
		pw, err := bus.ReadPassword(bus.UserPasswordFile(getenv, u))
		if err != nil {
			return fmt.Errorf("%s's password file: %v (nothing changed; ./setup.sh acl off, then on, remakes them)", u, err)
		}
		old[u] = pw
	}
	var afterErr error
	for i, u := range order {
		pw, err := bus.NewPassword()
		if err == nil {
			err = RotatePassword(ctx, userSetter{b, u}, bus.UserPasswordFile(getenv, u), old[u], pw,
				func(ctx context.Context, p string) error { return login(ctx, u, p) })
		}
		if err != nil {
			saved := "the users before it are rotated and saved"
			if i == 0 {
				saved = "no user was rotated"
			}
			return fmt.Errorf("%s: %v; %s, it and the ones after keep their passwords", u, err, saved)
		}
		// Saved after each user (spec §3 step 4; reviewer-codex): an
		// interruption leaves no rotated file that users.acl does not hold.
		if err := b.SaveACL(ctx); err != nil {
			return fmt.Errorf("ACL SAVE after %s failed (%v): %s holds its new password on the broker and in its file, but users.acl its old one, so a restart would bring that back; the users before it are saved, the ones after untouched. Run the rotation again now", u, err, u)
		}
		if u == bus.ACLPing {
			afterErr = after(u, pw)
		}
	}
	if afterErr != nil {
		return fmt.Errorf("ping is rotated, but the healthcheck's copy could not be written (%v): the broker shows unhealthy until it is", afterErr)
	}
	return nil
}

// UsersDialer opens a connection to the local broker as user with pw; the
// caller closes it.
type UsersDialer func(ctx context.Context, user, pw string) (ACLUsers, func(), error)

// rotateACLUsers is rotate-redis-password with broker.acl on: admin's file
// logs in (nothing changes otherwise), the person confirms, RotateUsers runs
// on that connection with a fresh login as each user for its proof, ping's
// new password goes into the data volume for the healthcheck, then the
// running project services are restarted. The default user is left alone:
// it is off.
func (in *Installer) rotateACLUsers(ctx context.Context, p *Prompter, users UsersDialer) int {
	o := in.O
	adm, err := bus.ReadPassword(bus.UserPasswordFile(in.Getenv, bus.ACLAdmin))
	if err != nil {
		o.Fatal("broker.acl is on, but admin's password file cannot be read (" + err.Error() + "): nothing changed. ./setup.sh status shows whether the broker holds its users")
		return 1
	}
	live, closeLive, err := users(ctx, bus.ACLAdmin, adm)
	if err != nil {
		o.Fatal("broker.acl is on, but admin's password does not log in to the broker (" + err.Error() + "): nothing changed. ./setup.sh status shows whether the broker holds its users")
		return 1
	}
	defer closeLive()
	if in.Opt.DryRun {
		o.Info("[dry-run] would write a new random password for each broker user beside " + bus.PasswordFile(in.Getenv) + " (0600, the previous one kept as .prev), set it on the running broker as admin (admin last), prove each, save users.acl, put ping's in the broker's data volume for the healthcheck, then restart the running project services")
		return 0
	}
	if !p.Yes {
		yes, err := Ask(o, p, "replace the passwords of the Redis broker's ACL users? (busmon and any client on another machine must reload them)", false)
		if err != nil || !yes {
			o.Info("broker ACL users: passwords left as they are")
			return 0
		}
	}
	login := func(ctx context.Context, user, pw string) error {
		_, closeNew, err := users(ctx, user, pw)
		if err == nil {
			closeNew()
		}
		return err
	}
	ping := func(_, pw string) error { return in.toVolume(ctx, "ping.pass", []byte(pw)) }
	if err := RotateUsers(ctx, live, in.Getenv, login, ping); err != nil {
		o.Fatal("broker ACL users: " + err.Error())
		return 1
	}
	o.OK("broker ACL users: each password replaced on the running broker and in its file beside " + bus.PasswordFile(in.Getenv) + " (0600, the previous ones kept as .prev), and saved to users.acl")
	code := in.restartRunningServices(ctx)
	o.Info("reopen busmon; a client on another machine (SSH tunnel) needs its user's new password file")
	return code
}
