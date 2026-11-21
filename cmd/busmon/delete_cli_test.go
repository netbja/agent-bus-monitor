package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// busmon-cli-flags item 2: busmon --delete keeps the Projects page's rules:
// a project that is not asleep is refused, -y included; the hosts where
// its units remain are named; a backup is taken just before the delete, and
// a failed one deletes nothing.

// cliProject is a project with keys on the test broker.
func cliProject(t *testing.T) (*redis.Client, string) {
	t.Helper()
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	project := "dc" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, _ := bus.Open(c, project)
	ctx := context.Background()
	b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}})
	t.Cleanup(func() { c.Del(ctx, bus.ServicesKey(project)) })
	return c, project
}

func keysLeft(t *testing.T, c *redis.Client, project string) int {
	t.Helper()
	keys, err := bus.ProjectKeys(context.Background(), c, project)
	if err != nil {
		t.Fatal(err)
	}
	return len(keys)
}

// A live project, or one whose quiet cannot be checked, is refused with -y
// too, before any backup or removal.
func TestDeleteCLIRefusesWhatIsNotAsleep(t *testing.T) {
	c, project := cliProject(t)
	for _, q := range []struct {
		why  string
		err  error
		want string
	}{
		{"1 agent is live here: Z puts the project to sleep", nil, "not asleep: 1 agent is live here"},
		{"", errors.New("systemctl did not answer"), "cannot check that " + project + " is quiet (systemctl did not answer)"},
	} {
		backedUp, removed := false, false
		d := testDeleteDeps(func(string) ([]string, error) { removed = true; return nil, nil }, nil)
		d.quiet = func(context.Context, *redis.Client, string) (string, error) { return q.why, q.err }
		d.backup = func(string, string, string) (string, error) { backedUp = true; return "", nil }
		err := deleteProject("", project, true, strings.NewReader(""), time.Now(), d)
		if err == nil || !strings.Contains(err.Error(), q.want) || !strings.Contains(err.Error(), "nothing deleted") {
			t.Fatalf("%q: %v", q.want, err)
		}
		if backedUp || removed || keysLeft(t, c, project) == 0 {
			t.Fatalf("%q: backed up %v, services removed %v, keys %d", q.want, backedUp, removed, keysLeft(t, c, project))
		}
	}
	// Without a quiet check or a backup to run, nothing is deleted.
	d := testDeleteDeps(nil, nil)
	d.quiet = nil
	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), d); err == nil || keysLeft(t, c, project) == 0 {
		t.Fatalf("no quiet check: %v", err)
	}
}

// The backup runs after the yes and before the delete, the keys still
// there; a failed one deletes nothing; a declined prompt runs none.
func TestDeleteCLIBacksUpFirst(t *testing.T) {
	c, project := cliProject(t)
	d := testDeleteDeps(nil, nil)
	calls := 0
	d.backup = func(string, string, string) (string, error) { calls++; return "", errors.New("exit status 1") }
	if err := deleteProject("", project, false, strings.NewReader("n\n"), time.Now(), d); err != nil || calls != 0 {
		t.Fatalf("declined: %v, %d backups", err, calls)
	}
	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), d); err == nil ||
		!strings.Contains(err.Error(), "backup of "+project+" failed") || !strings.Contains(err.Error(), "nothing deleted") || keysLeft(t, c, project) == 0 {
		t.Fatalf("failed backup: %v (keys %d)", err, keysLeft(t, c, project))
	}

	keysAtBackup := 0
	var out bytes.Buffer
	d.out = &out
	d.backup = func(p, _, _ string) (string, error) {
		keysAtBackup = keysLeft(t, c, p)
		return "", nil
	}
	d.unitHosts = func(context.Context, *redis.Client, string) ([]unitHosts, error) {
		return []unitHosts{{host: "vdr", kinds: []string{"dispatch", "missions"}}}, nil
	}
	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), d); err != nil {
		t.Fatal(err)
	}
	if keysAtBackup == 0 || keysLeft(t, c, project) != 0 {
		t.Fatalf("keys at the backup %d, after %d", keysAtBackup, keysLeft(t, c, project))
	}
	text := out.String()
	for _, w := range []string{"warning: " + project + "'s units remain on vdr (dispatch, missions)", "A backup of " + project + " is taken just before the delete",
		"Units remain on vdr (dispatch, missions): agentbus --project " + project + " service remove there"} {
		if !strings.Contains(text, w) {
			t.Errorf("output lacks %q:\n%s", w, text)
		}
	}
	if strings.Index(text, "warning:") > strings.Index(text, "Deleted ") {
		t.Errorf("the remote-host warning comes after the delete:\n%s", text)
	}
}

// busmon --delete's real wiring has the page's checks and a backup.
func TestRealDeleteDepsAreWired(t *testing.T) {
	d := realDeleteDeps(nil)
	if d.quiet == nil || d.backup == nil || d.unitHosts == nil || d.removeServices == nil || d.out == nil {
		t.Fatalf("%+v", d)
	}
}

// The backup targets the broker the delete deletes from (reviewer-codex,
// foureyes on 3538d7bb): it gets --host, and the delete's broker to check.
func TestDeleteCLIBacksUpTheDeletedBroker(t *testing.T) {
	c, project := cliProject(t)
	d := testDeleteDeps(nil, nil)
	var gotHost, gotBroker string
	d.backup = func(p, host, broker string) (string, error) { gotHost, gotBroker = host, broker; return "", nil }
	// The test broker is REDIS_URL, which wins over the host on both sides:
	// the host still reaches the backup as given.
	if err := deleteProject("vdr", project, true, strings.NewReader(""), time.Now(), d); err != nil {
		t.Fatal(err)
	}
	if gotHost != "vdr" || gotBroker != brokerKey(c.Options()) {
		t.Fatalf("backup got host %q broker %q, want %q", gotHost, gotBroker, brokerKey(c.Options()))
	}
}

// The real backup runs agentbus with the delete's host as REDIS_HOST, and
// refuses, before running anything, a child that would reach another
// broker.
func TestRealDeleteBackupTarget(t *testing.T) {
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIS_HOST", "box")
	env := deleteBackupEnv([]string{"PATH=/bin", "REDIS_HOST=box"}, "vdr")
	if !slices.Contains(env, "REDIS_HOST=vdr") || slices.Contains(env, "REDIS_HOST=box") {
		t.Fatalf("env %v", env)
	}
	if env := deleteBackupEnv([]string{"REDIS_HOST=box"}, ""); !slices.Equal(env, []string{"REDIS_HOST=box"}) {
		t.Fatalf("no host: env %v", env)
	}
	_, err := realDeleteBackup("/nonexistent/agentbus")("demo", "vdr", "tcp box:6380 db0")
	if err == nil || !strings.Contains(err.Error(), "another broker") {
		t.Fatalf("mismatch: %v", err)
	}
	_, err = realDeleteBackup("/nonexistent/agentbus")("demo", "vdr", "tcp vdr:6380 db0")
	if err == nil || strings.Contains(err.Error(), "another broker") {
		t.Fatalf("a matching broker was refused, or the backup ran nothing: %v", err)
	}
}
