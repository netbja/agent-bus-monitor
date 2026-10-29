package setup

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// RedisContainer is the broker's container name, shared by every checkout.
const RedisContainer = "agent-bus-redis"

// Docker is what step 1 learned of docker: the daemon answers, and the
// compose command to use ("" when there is none).
type Docker struct {
	OK      bool
	Compose string
}

// Owner is the checkout a broker container belongs to, read from its
// compose label. A zero Owner is no container at all.
type Owner struct {
	Exists bool
	Dir    string // com.docker.compose.project.working_dir, "" when unlabelled
	Ours   bool   // Dir is this checkout
}

// BrokerOwner reads which checkout the broker container belongs to (spec:
// Safety, layer 2). Every checkout shares the compose project and container
// names, so compose from another checkout would recreate the live broker
// from its own config. An error is an owner that could not be read: the
// caller then acts as for a foreign one.
func (in *Installer) BrokerOwner(ctx context.Context) (Owner, error) {
	out, err := in.R.Run(ctx, Cmd{Name: "docker", Args: []string{"inspect", "--format",
		`{{index .Config.Labels "com.docker.compose.project.working_dir"}}`, RedisContainer}})
	if err != nil {
		if strings.Contains(string(out), "No such object") || strings.Contains(string(out), "No such container") {
			return Owner{}, nil
		}
		return Owner{}, err
	}
	dir, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return Owner{Exists: true, Dir: dir, Ours: dir != "" && sameDir(dir, in.Repo)}, nil
}

// mayCompose reports whether a compose verb may run from this checkout:
// no broker container, or one this checkout owns. Otherwise it says why not,
// ending an unreadable owner's warning with refused ("not started.").
func (in *Installer) mayCompose(ctx context.Context, refused string) bool {
	o, err := in.BrokerOwner(ctx)
	switch {
	case err != nil:
		in.O.Warn("Redis broker: could not read who owns " + RedisContainer + " (docker inspect failed): " + refused)
	case !o.Exists || o.Ours:
		return true
	case o.Dir == "":
		in.O.Warn("Redis broker: the broker belongs to no checkout (" + RedisContainer + " has no compose label): start or stop it by hand.")
	default:
		in.O.Warn("Redis broker: the broker belongs to " + o.Dir + ": start or stop it from there.")
	}
	return false
}

func (in *Installer) open() bool {
	if in.Probe != nil {
		return in.Probe("127.0.0.1", 6380)
	}
	return PortOpen("127.0.0.1", 6380)
}

// PortOpen is setup.sh's is_port_open: a TCP connect.
func PortOpen(host string, port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// Redis is step 4/5: the broker on 127.0.0.1:6380, started with compose
// from this checkout when it is not reachable, and only when no other
// checkout owns its container.
func (in *Installer) Redis(ctx context.Context, d Docker) {
	switch {
	case in.open():
		in.O.OK("Redis broker: active and listening on 127.0.0.1:6380")
		return
	case !in.Opt.StartDocker:
		in.O.Info("Redis broker not reachable on 127.0.0.1:6380 (--no-docker: not started).")
		return
	case !d.OK || d.Compose == "":
		in.O.Warn("Redis broker not reachable on 127.0.0.1:6380 (run 'docker compose up -d' manually in " + in.Repo + ").")
		return
	case !in.mayCompose(ctx, "not started."):
		return
	case in.Opt.DryRun:
		in.password(ctx)
		in.O.Info("[dry-run] would start Redis container via " + d.Compose + " up -d")
		return
	}
	in.password(ctx)
	in.O.Info("Redis not detected on :6380. Starting broker container...")
	name, args := composeArgs(d.Compose, "up", "-d")
	if out, err := in.R.Run(ctx, Cmd{Name: name, Args: args, Dir: in.Repo}); err != nil {
		showTail(in.O.err, out)
		in.O.Warn("Failed to launch Redis container via " + d.Compose + " up -d")
		return
	}
	// Wait up to 10 seconds for the port to become ready.
	for range 10 {
		if in.open() {
			in.O.OK("Redis broker: started successfully on 127.0.0.1:6380")
			return
		}
		in.sleep(time.Second)
	}
	in.O.Warn("Redis container started but port 6380 is not yet responsive.")
}

// RedisVolume is the broker's data volume (compose project agent-bus).
const RedisVolume = "agent-bus_redis-data"

// password writes a random password file before compose starts a fresh
// broker (redis-password): no file yet, no exported REDIS_PASSWORD (compose
// would use it instead), no broker container and no data volume. An
// existing broker keeps its password: the person replaces it with
// ./setup.sh rotate-redis-password. It never prints the password.
func (in *Installer) password(ctx context.Context) {
	path := bus.PasswordFile(in.Getenv)
	if path == "" {
		return
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return // there already (or unreadable): never replaced here
	}
	if in.Getenv("REDIS_PASSWORD") != "" {
		in.O.Info("Redis password: REDIS_PASSWORD is exported, compose uses it (no file written)")
		return
	}
	if setsKey(filepath.Join(in.Repo, ".env"), "REDIS_PASSWORD") { // compose would start the broker with it (reviewer-codex)
		in.O.Info("Redis password: " + filepath.Join(in.Repo, ".env") + " sets REDIS_PASSWORD, compose uses it (no file written); export the same value for the clients")
		return
	}
	if !in.freshBroker(ctx) {
		in.O.Info("Redis password: an existing broker keeps its password (./setup.sh rotate-redis-password replaces it)")
		return
	}
	if in.Opt.DryRun {
		in.O.Info("[dry-run] would write a random Redis password to " + path + " (0600)")
		return
	}
	pw, err := bus.NewPassword()
	if err == nil {
		err = bus.WritePassword(path, pw)
	}
	if err != nil {
		in.O.Warn("Redis password: could not write " + path + " (" + err.Error() + "); the broker starts with the historical default")
		return
	}
	in.O.OK("Redis password: a random one written to " + path + " (0600)")
}

// freshBroker: neither the broker's container nor its data volume exists,
// both read for sure (an unreadable answer is not fresh).
func (in *Installer) freshBroker(ctx context.Context) bool {
	if o, err := in.BrokerOwner(ctx); err != nil || o.Exists {
		return false
	}
	out, err := in.R.Run(ctx, Cmd{Name: "docker", Args: []string{"volume", "inspect", "--format", "{{.Name}}", RedisVolume}})
	return err != nil && strings.Contains(strings.ToLower(string(out)), "no such volume")
}

func (in *Installer) sleep(d time.Duration) {
	if in.Sleep != nil {
		in.Sleep(d)
		return
	}
	time.Sleep(d)
}

// composeArgs splits "docker compose" or "docker-compose" and adds verb.
func composeArgs(compose string, verb ...string) (string, []string) {
	f := strings.Fields(compose)
	return f[0], append(f[1:], verb...)
}
