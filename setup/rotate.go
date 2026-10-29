package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/redis/go-redis/v9"
)

// The broker password's rotation (redis-password): ./setup.sh
// rotate-redis-password, the person's explicit act, never automatic.

// PasswordSetter changes the live broker's password.
type PasswordSetter interface {
	SetPassword(ctx context.Context, pw string) error
}

// RedisSetter sets it with CONFIG SET requirepass on an authenticated
// connection, which stays authenticated after the change.
type RedisSetter struct{ C *redis.Client }

func (s RedisSetter) SetPassword(ctx context.Context, pw string) error {
	return s.C.ConfigSet(ctx, "requirepass", pw).Err()
}

// RotatePassword replaces the password: the file first (the previous one
// kept as path.prev), then the live broker, then a fresh connection with the
// new one (verify). If the broker refuses, the file is put back exactly as it
// was; if the proof fails, the broker is set back to old and the file put
// back. Nothing is left half-written: every file is replaced by a rename.
func RotatePassword(ctx context.Context, live PasswordSetter, path, old, pw string, verify func(context.Context, string) error) error {
	prior, err := os.ReadFile(path)
	had := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	restore := func() error {
		var err error
		if had {
			err = writeSecret(path, prior)
		} else {
			err = os.Remove(path)
		}
		if rerr := os.Remove(path + ".prev"); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) && err == nil {
			err = rerr
		}
		return err
	}
	if had {
		if err := writeSecret(path+".prev", prior); err != nil {
			return err
		}
	}
	if err := bus.WritePassword(path, pw); err != nil {
		_ = restore()
		return err
	}
	if err := live.SetPassword(ctx, pw); err != nil {
		if rerr := restore(); rerr != nil {
			return fmt.Errorf("the broker refused the new password (%v), and %s could not be put back: %v", err, path, rerr)
		}
		return fmt.Errorf("the broker refused the new password (%v); nothing changed", err)
	}
	if err := verify(ctx, pw); err != nil {
		if back := live.SetPassword(ctx, old); back != nil {
			// The broker may hold the new one: the file keeps it, the old one
			// stays beside it, and nothing is restored (reviewer-codex).
			prev := "the previous one is the historical default"
			if had {
				prev = "the previous one is in " + path + ".prev"
			}
			return fmt.Errorf("the new password does not log in (%v), and the broker could not be set back to the old one (%v): it may hold the new one, kept in %s; %s. Try both before anything else", err, back, path, prev)
		}
		rerr := restore()
		switch {
		case rerr != nil:
			return fmt.Errorf("the new password does not log in (%v); the broker is back on the old one, but %s could not be put back: %v", err, path, rerr)
		}
		return fmt.Errorf("the new password does not log in (%v); the broker and %s are back as they were", err, path)
	}
	return nil
}

// writeSecret replaces path with data: 0600 in a 0700 directory, through a
// temporary file renamed over it.
func writeSecret(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".redis.env-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Dialer opens an authenticated connection to the local broker with pw; the
// caller closes it.
type Dialer func(ctx context.Context, pw string) (PasswordSetter, func(), error)

// RotateRedisPassword is ./setup.sh rotate-redis-password: everything is
// checked before anything changes (no exported REDIS_URL or REDIS_PASSWORD,
// which would win over the file; this checkout's broker running; no
// REDIS_PASSWORD in the checkout's .env, which compose would use; no
// project's services env file carrying a password; the current one logs
// in), then the
// person confirms (or --yes), then RotatePassword, then the running project
// services are restarted so their new connections use it. It never prints a
// password.
func (in *Installer) RotateRedisPassword(ctx context.Context, p *Prompter, dial Dialer) int {
	o := in.O
	path := bus.PasswordFile(in.Getenv)
	for _, k := range []string{"REDIS_URL", "REDIS_PASSWORD"} {
		if in.Getenv(k) != "" {
			o.Fatal(k + " is exported in this shell: it wins over the password file, so a rotation would not reach this shell's clients. Unset it (and remove it from your shell profile), then run this again.")
			return 1
		}
	}
	if path == "" {
		o.Fatal("no HOME: there is nowhere to keep the password file")
		return 1
	}
	if st := in.RedisState(ctx); st != "running" {
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
		o.Fatal("the broker belongs to " + where + ": rotate its password from there")
		return 1
	}
	if setsKey(filepath.Join(in.Repo, ".env"), "REDIS_PASSWORD") {
		o.Fatal(filepath.Join(in.Repo, ".env") + " sets REDIS_PASSWORD: compose would start the broker with it, not with the password file. Remove that line, then run this again.")
		return 1
	}
	if carriers := in.envFilesWithPassword(); len(carriers) > 0 {
		o.Fatal("these projects' services carry their own Redis password in their env file (copied from a shell that exported it): " +
			strings.Join(carriers, ", ") + ". Run agentbus up <project> from a shell without REDIS_PASSWORD and REDIS_URL, then run this again.")
		return 1
	}
	old, err := bus.ReadPassword(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		old = bus.LegacyPassword
	case err != nil:
		o.Fatal(err.Error())
		return 1
	}
	live, closeLive, err := dial(ctx, old)
	if err != nil {
		o.Fatal("the current password does not log in to the broker (" + err.Error() + "): nothing changed")
		return 1
	}
	defer closeLive()
	if in.Opt.DryRun {
		o.Info("[dry-run] would write a new random password to " + path + " (0600), set it on the running broker, prove it, then restart the running project services")
		return 0
	}
	if !p.Yes {
		yes, err := Ask(o, p, "replace the Redis broker's password? (busmon and any client on another machine must reload it)", false)
		if err != nil || !yes {
			o.Info("Redis password: left as it is")
			return 0
		}
	}
	pw, err := bus.NewPassword()
	if err != nil {
		o.Fatal(err.Error())
		return 1
	}
	verify := func(ctx context.Context, pw string) error {
		_, closeNew, err := dial(ctx, pw)
		if err == nil {
			closeNew()
		}
		return err
	}
	if err := RotatePassword(ctx, live, path, old, pw, verify); err != nil {
		o.Fatal("Redis password: " + err.Error())
		return 1
	}
	kept := ""
	if old != bus.LegacyPassword {
		kept = "; the previous one kept as " + path + ".prev"
	}
	o.OK("Redis password: replaced on the running broker and in " + path + " (0600)" + kept)
	code := in.restartRunningServices(ctx)
	o.Info("reopen busmon; a client on another machine (SSH tunnel) needs the new " + path)
	return code
}

// envFilesWithPassword names the projects whose services env file sets
// REDIS_PASSWORD or REDIS_URL (their values are read, never printed).
func (in *Installer) envFilesWithPassword() []string {
	paths, err := service.PathsFrom(in.Getenv)
	if err != nil {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(paths.ConfigHome, "agentbus", "services", "*.env"))
	var out []string
	for _, f := range files {
		if setsKey(f, "REDIS_PASSWORD") || setsKey(f, "REDIS_URL") {
			out = append(out, strings.TrimSuffix(filepath.Base(f), ".env"))
		}
	}
	return out
}

// setsKey reports whether an env file (KEY=value or KEY="value" lines, an
// optional export) sets key to a non-empty value; its values are never
// returned. A file that exists but cannot be read counts as setting it, so a
// rotation is refused rather than guessed.
func setsKey(path, key string) bool {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
		if ok && strings.TrimSpace(k) == key && strings.Trim(strings.TrimSpace(v), `"'`) != "" {
			return true
		}
	}
	return false
}

// restartRunningServices restarts each project service that runs, so its
// new connections use the new password (a stopped one reads it when it
// starts).
func (in *Installer) restartRunningServices(ctx context.Context) int {
	ab := filepath.Join(in.Opt.BinDir, "agentbus")
	if !isExec(ab) {
		ab = filepath.Join(in.Repo, "agentbus")
	}
	code := 0
	for _, l := range in.serviceLines(ctx) {
		f := strings.Fields(l)
		if len(f) < 3 || !(strings.HasPrefix(f[2], "active") || strings.HasPrefix(f[2], "running")) {
			continue
		}
		if out, err := in.R.Run(ctx, Cmd{Name: ab, Args: []string{"--project", f[0], "service", "restart", f[1]}}); err != nil {
			showTail(in.O.err, out)
			in.O.Warn("could not restart " + f[0] + " " + f[1] + ": agentbus --project " + f[0] + " service restart " + f[1])
			code = 1
			continue
		}
		in.O.OK("restarted " + f[0] + " " + f[1])
	}
	return code
}
