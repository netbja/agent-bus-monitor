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
// connection that rotates them is admin's and stays logged in; then ACL
// SAVE writes users.acl, so a restart keeps them.

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

// RotateUsers rotates every user of bus.ACLRoles, admin last, then saves.
// Every file is read before anything changes. A user that fails stops the
// rotation: it and the users after it keep their passwords, the ones before
// stay rotated and are saved. login proves a fresh login as user; after
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
	var failed, afterErr error
	rotated := 0
	for _, u := range order {
		pw, err := bus.NewPassword()
		if err == nil {
			err = RotatePassword(ctx, userSetter{b, u}, bus.UserPasswordFile(getenv, u), old[u], pw,
				func(ctx context.Context, p string) error { return login(ctx, u, p) })
		}
		if err != nil {
			failed = fmt.Errorf("%s: %v", u, err)
			break
		}
		rotated++
		if u == bus.ACLPing {
			afterErr = after(u, pw)
		}
	}
	if rotated > 0 {
		if err := b.SaveACL(ctx); err != nil {
			return fmt.Errorf("ACL SAVE failed (%v): the broker and the files hold the new passwords, but users.acl the old ones, so a restart would bring those back. Run the rotation again now", err)
		}
	}
	if failed != nil {
		return fmt.Errorf("%v; the users before it are rotated and saved, it and the ones after keep their passwords", failed)
	}
	if afterErr != nil {
		return fmt.Errorf("ping is rotated, but the healthcheck's copy could not be written (%v): the broker shows unhealthy until it is", afterErr)
	}
	return nil
}
