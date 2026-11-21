package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/redis/go-redis/v9"
)

// usersBroker is a broker with ACL users: their live passwords, the ones
// ACL SAVE wrote, and the order of the changes.
type usersBroker struct {
	live, saved map[string]string
	refuse      string // the user whose ACL SETUSER fails
	saveErr     error
	order       []string
}

func (b *usersBroker) SetUserPassword(_ context.Context, user, pw string) error {
	if user == b.refuse {
		return errors.New("ERR refused")
	}
	b.live[user] = pw
	b.order = append(b.order, user)
	return nil
}

func (b *usersBroker) SaveACL(context.Context) error {
	if b.saveErr != nil {
		return b.saveErr
	}
	for u, p := range b.live {
		b.saved[u] = p
	}
	return nil
}

func (b *usersBroker) login(_ context.Context, user, pw string) error {
	if p, ok := b.live[user]; ok && p == pw {
		return nil
	}
	return errors.New("WRONGPASS")
}

// usersWorld: a password file per user, the broker holding them.
func usersWorld(t *testing.T) (func(string) string, *usersBroker) {
	t.Helper()
	home := t.TempDir()
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	b := &usersBroker{live: map[string]string{}, saved: map[string]string{}}
	for _, u := range bus.ACLRoles {
		if err := bus.WritePassword(bus.UserPasswordFile(getenv, u), "old-"+u); err != nil {
			t.Fatal(err)
		}
		b.live[u], b.saved[u] = "old-"+u, "old-"+u
	}
	return getenv, b
}

func fileOf(t *testing.T, getenv func(string) string, user string) string {
	t.Helper()
	pw, err := bus.ReadPassword(bus.UserPasswordFile(getenv, user))
	if err != nil {
		t.Fatal(err)
	}
	return pw
}

// broker-hardening step 4b part 3: each user's password replaced on the
// broker and in its file, admin last (the connection rotating them is
// admin's), then ACL SAVE; ping's new password handed to after (the
// healthcheck's copy).
func TestRotateUsers(t *testing.T) {
	ctx := context.Background()
	getenv, b := usersWorld(t)
	var after []string
	err := RotateUsers(ctx, b, getenv, b.login, func(user, pw string) error {
		after = append(after, user+"="+pw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.order) != len(bus.ACLRoles) || b.order[len(b.order)-1] != bus.ACLAdmin {
		t.Fatalf("order %v: admin must be last", b.order)
	}
	for _, u := range bus.ACLRoles {
		pw := fileOf(t, getenv, u)
		if pw == "old-"+u || b.live[u] != pw || b.saved[u] != pw {
			t.Errorf("%s: file %q, live %q, saved %q", u, pw, b.live[u], b.saved[u])
		}
		if prev, _ := bus.ReadPassword(bus.UserPasswordFile(getenv, u) + ".prev"); prev != "old-"+u {
			t.Errorf("%s: .prev %q", u, prev)
		}
	}
	if !slices.Equal(after, []string{bus.ACLPing + "=" + b.live[bus.ACLPing]}) {
		t.Errorf("after: %v", after)
	}
}

// A user the broker refuses: it and every user after it keep their
// password (file and broker), the ones before stay rotated and are saved.
func TestRotateUsersStopsAtARefusal(t *testing.T) {
	ctx := context.Background()
	getenv, b := usersWorld(t)
	b.refuse = bus.ACLAgent
	err := RotateUsers(ctx, b, getenv, b.login, func(string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), bus.ACLAgent) {
		t.Fatalf("err %v", err)
	}
	i := slices.Index(bus.ACLRoles, bus.ACLAgent)
	for _, u := range bus.ACLRoles {
		pw := fileOf(t, getenv, u)
		rotated := u != bus.ACLAdmin && slices.Index(bus.ACLRoles, u) < i
		if rotated != (pw != "old-"+u) || b.live[u] != pw || b.saved[u] != pw {
			t.Errorf("%s: rotated=%v, file %q, live %q, saved %q", u, rotated, pw, b.live[u], b.saved[u])
		}
	}
}

// Every file is read before anything changes: a missing one changes nothing.
func TestRotateUsersNeedsEveryFile(t *testing.T) {
	getenv, b := usersWorld(t)
	_ = os.Remove(bus.UserPasswordFile(getenv, bus.ACLPing))
	if err := RotateUsers(context.Background(), b, getenv, b.login, func(string, string) error { return nil }); err == nil || !strings.Contains(err.Error(), bus.ACLPing) {
		t.Fatalf("err %v", err)
	}
	if len(b.order) != 0 {
		t.Fatalf("changed %v", b.order)
	}
}

// ACL SAVE fails: the error says the broker holds the new passwords only
// until it restarts.
func TestRotateUsersSaveFails(t *testing.T) {
	getenv, b := usersWorld(t)
	b.saveErr = errors.New("ERR no aclfile")
	err := RotateUsers(context.Background(), b, getenv, b.login, func(string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "ACL SAVE") {
		t.Fatalf("err %v", err)
	}
}

// The same on a real broker loaded from bus.ACLFile (a private
// redis-server on a socket, started by the test): every user logs in with
// its new file, the old password is refused, and users.acl on disk holds
// the new hashes.
func TestRotateUsersOnADisposableRedis(t *testing.T) {
	if os.Getenv("AGENTBUS_TEST_REDIS_URL") == "" {
		t.Skip("Redis integration test: set AGENTBUS_TEST_REDIS_URL (this one starts its own disposable redis-server)")
	}
	server, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("no redis-server on PATH")
	}
	getenv, _ := usersWorld(t)
	old := map[string]string{}
	for _, u := range bus.ACLRoles {
		old[u] = "old-" + u
	}
	var acl strings.Builder // users.acl as ./setup.sh acl on writes it
	for _, u := range bus.ACLRoles {
		acl.WriteString("user " + u + " reset on >" + old[u] + " " + strings.Join(bus.ACLRules(u), " ") + "\n")
	}
	acl.WriteString("user default off\n")
	data := []byte(acl.String())
	dir := t.TempDir()
	aclfile := filepath.Join(dir, "users.acl")
	_ = os.WriteFile(aclfile, data, 0o600)
	sock := filepath.Join(dir, "r.sock")
	cmd := exec.Command(server, "--port", "0", "--unixsocket", sock, "--unixsocketperm", "700", "--save", "", "--appendonly", "no", "--aclfile", aclfile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for range 50 {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx := context.Background()
	login := func(ctx context.Context, user, pw string) error {
		c := redis.NewClient(&redis.Options{Network: "unix", Addr: sock, Username: user, Password: pw})
		defer c.Close()
		return c.Ping(ctx).Err()
	}
	admin := redis.NewClient(&redis.Options{Network: "unix", Addr: sock, Username: bus.ACLAdmin, Password: old[bus.ACLAdmin]})
	defer admin.Close()
	if err := RotateUsers(ctx, RedisUsers{C: admin}, getenv, login, func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(aclfile)
	for _, u := range bus.ACLRoles {
		pw := fileOf(t, getenv, u)
		if login(ctx, u, pw) != nil || login(ctx, u, old[u]) == nil {
			t.Errorf("%s: the new password does not log in alone", u)
		}
		sum := sha256.Sum256([]byte(pw))
		if !strings.Contains(string(saved), hex.EncodeToString(sum[:])) {
			t.Errorf("%s: users.acl does not hold the new password", u)
		}
	}
	if login(ctx, "default", "") == nil {
		t.Error("the default user logs in")
	}
}
