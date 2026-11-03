package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/notify"
)

// notifyWorld is a home with config.toml, notify.toml and a state folder,
// and a loopback ntfy server: no test reaches a real host.
type notifyWorld struct {
	home, cfg, state string
	srv              *httptest.Server
	mu               sync.Mutex
	got              []string // the titles the server received
	status           int
	desktop          [][3]string
}

func newNotifyWorld(t *testing.T) *notifyWorld {
	t.Helper()
	w := &notifyWorld{home: t.TempDir(), status: 200}
	w.cfg = filepath.Join(w.home, ".config", "agentbus")
	w.state = filepath.Join(w.home, ".local", "state")
	if err := os.MkdirAll(w.cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.mu.Lock()
		w.got = append(w.got, r.Header.Get("Title"))
		status := w.status
		w.mu.Unlock()
		rw.WriteHeader(status)
	}))
	t.Cleanup(w.srv.Close)
	return w
}

func (w *notifyWorld) write(t *testing.T, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(w.cfg, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// notifyToml has a self-hosted ntfy target on the loopback server, a
// desktop target and a Discord one (never reached by these tests).
func (w *notifyWorld) notifyToml() string {
	return `[ntfy.phone]
topic = "agentbus-secret-topic"
server = "` + w.srv.URL + `"
insecure = true
self_hosted = true
token = "tk_never_shown"

[desktop.here]

[discord.team]
webhook = "https://discord.com/api/webhooks/1/hook-secret"
`
}

func (w *notifyWorld) deps() notificationsDeps {
	env := map[string]string{"HOME": w.home, "XDG_STATE_HOME": w.state}
	return notificationsDeps{
		Getenv:   func(k string) string { return env[k] },
		Now:      func() time.Time { return time.Date(2026, 11, 3, 14, 2, 0, 0, time.Local) },
		Hostname: func() (string, error) { return "box", nil },
		Desktop: func(_ context.Context, title, body, urgency string) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.desktop = append(w.desktop, [3]string{title, body, urgency})
			return nil
		},
	}
}

func (w *notifyWorld) run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errw bytes.Buffer
	code := runNotifications(context.Background(), args, w.deps(), &out, &errw)
	return out.String(), errw.String(), code
}

func noLeak(t *testing.T, s string) {
	t.Helper()
	for _, leak := range []string{"agentbus-secret-topic", "tk_never_shown", "hook-secret", "127.0.0.1", "http://", "https://"} {
		if strings.Contains(s, leak) {
			t.Fatalf("output leaks %q:\n%s", leak, s)
		}
	}
}

func TestNotificationsTestSendsAndRecords(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	out, errw, code := w.run(t, "test", "phone")
	if code != 0 || !strings.Contains(out, "phone: ok") {
		t.Fatalf("exit %d\n%s%s", code, out, errw)
	}
	noLeak(t, out+errw)
	if len(w.got) != 1 || w.got[0] != "agentbus: a test message from box" {
		t.Fatalf("server got %q", w.got)
	}
	l, err := notify.ReadLast(notify.LastPath(filepath.Join(w.state, "agentbus")))
	if err != nil {
		t.Fatal(err)
	}
	o, ok := l.Latest("phone")
	if !ok || !o.OK || !o.Test || o.Project != "" {
		t.Fatalf("recorded %+v %v", o, ok)
	}
	if l.SentInHour("phone", time.Date(2026, 11, 3, 14, 3, 0, 0, time.Local)) != 0 {
		t.Fatal("a test send is not counted in the hour's limit")
	}
}

func TestNotificationsTestFailureSaysTheShortError(t *testing.T) {
	w := newNotifyWorld(t)
	w.status = 401
	w.write(t, "notify.toml", w.notifyToml())
	out, errw, code := w.run(t, "test", "phone")
	if code != 1 || !strings.Contains(out, "phone: 401 unauthorized") {
		t.Fatalf("exit %d\n%s%s", code, out, errw)
	}
	noLeak(t, out+errw)
	l, _ := notify.ReadLast(notify.LastPath(filepath.Join(w.state, "agentbus")))
	if o, _ := l.Latest("phone"); o.OK || o.Err != "phone: 401 unauthorized" {
		t.Fatalf("recorded %+v", o)
	}
}

func TestNotificationsTestEveryListedTarget(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	w.write(t, "config.toml", "[notify]\ntargets = [\"phone\", \"here\"]\n")
	out, errw, code := w.run(t, "test")
	if code != 0 || !strings.Contains(out, "phone: ok") || !strings.Contains(out, "here: ok") || strings.Contains(out, "team") {
		t.Fatalf("exit %d\n%s%s", code, out, errw)
	}
	if len(w.got) != 1 || len(w.desktop) != 1 || w.desktop[0][0] != "agentbus: a test message from box" {
		t.Fatalf("ntfy %q desktop %q", w.got, w.desktop)
	}
}

func TestNotificationsTestWithNoTargetListed(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	_, errw, code := w.run(t, "test")
	if code != 1 || !strings.Contains(errw, "notify.targets") {
		t.Fatalf("exit %d %q", code, errw)
	}
}

func TestNotificationsTestUnknownTarget(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	_, errw, code := w.run(t, "test", "nope")
	if code != 1 || !strings.Contains(errw, "nope") {
		t.Fatalf("exit %d %q", code, errw)
	}
}

func TestNotificationsTestRefusedFile(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	os.Chmod(filepath.Join(w.cfg, "notify.toml"), 0o644)
	_, errw, code := w.run(t, "test", "phone")
	if code != 1 || !strings.Contains(errw, "chmod 600") || len(w.got) != 0 {
		t.Fatalf("exit %d %q, sent %d", code, errw, len(w.got))
	}
	noLeak(t, errw)
}

func TestNotificationsStatus(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml()+"\n[ntfy.bad]\ntopic = \"short\"\n")
	w.write(t, "config.toml", "[notify]\non = true\ntargets = [\"phone\", \"gone\"]\n")
	if _, _, code := w.run(t, "test", "phone"); code != 0 {
		t.Fatal("test send")
	}
	out, errw, code := w.run(t, "status")
	if code != 0 {
		t.Fatalf("exit %d %s", code, errw)
	}
	noLeak(t, out+errw)
	for _, want := range []string{
		"notifications: on",
		"phone", "ntfy, self-hosted", "listed", "last sent", "ok (test)",
		"here", "desktop", "not listed",
		"team", "discord",
		"gone", "listed in notify.targets but not in notify.toml",
		"ntfy.bad.topic",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "short") {
		t.Errorf("status echoes a value:\n%s", out)
	}
}

func TestNotificationsStatusOffAndMissing(t *testing.T) {
	w := newNotifyWorld(t)
	out, _, code := w.run(t, "status")
	if code != 0 || !strings.Contains(out, "notifications: off") || !strings.Contains(out, "notify.toml: none") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestNotificationsUsage(t *testing.T) {
	w := newNotifyWorld(t)
	for _, args := range [][]string{nil, {"bogus"}, {"status", "x"}, {"test", "a", "b"}} {
		if _, _, code := w.run(t, args...); code != 2 {
			t.Errorf("%q: exit %d", args, code)
		}
	}
}

// notifications test is the person's act (foureyes, master): an agent caller,
// AGENT_BUS_AGENT set, is refused by the command itself whatever the spelling
// of its command line, and nothing is sent.
func TestNotificationsTestRefusesAnAgent(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	d := w.deps()
	get := d.Getenv
	d.Getenv = func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "coder"
		}
		return get(k)
	}
	var out, errw bytes.Buffer
	code := runNotifications(context.Background(), []string{"test", "phone"}, d, &out, &errw)
	if code != 1 || !strings.Contains(errw.String(), "the person's") || len(w.got) != 0 {
		t.Fatalf("exit %d %q, sent %d", code, errw.String(), len(w.got))
	}
	// status only reads: an agent may run it.
	out.Reset()
	if code := runNotifications(context.Background(), []string{"status"}, d, &out, &errw); code != 0 {
		t.Fatalf("status exit %d", code)
	}
}

// reviewer-codex, master: env -u AGENT_BUS_AGENT is no way out. A caller
// whose process descends from a registered agent's verified pid is refused;
// a check that cannot be made refuses too; the person's shell is allowed.
func TestNotificationsTestRefusesAnAgentsChild(t *testing.T) {
	for name, c := range map[string]struct {
		caller string
		err    error
		code   int
	}{
		"agent's child": {caller: "coder", code: 1},
		"unchecked":     {err: errors.New("broker unreachable"), code: 1},
		"no /proc":      {err: errNoAncestry, code: 0}, // off Linux: the env rule alone
		"person":        {code: 0},
	} {
		w := newNotifyWorld(t)
		w.write(t, "notify.toml", w.notifyToml())
		d := w.deps()
		d.AgentCaller = func(context.Context) (string, error) { return c.caller, c.err }
		var out, errw bytes.Buffer
		code := runNotifications(context.Background(), []string{"test", "phone"}, d, &out, &errw)
		if code != c.code || (c.code == 1 && len(w.got) != 0) || (c.code == 0 && len(w.got) != 1) {
			t.Errorf("%s: exit %d %q, sent %d", name, code, errw.String(), len(w.got))
		}
	}
}

// The real check: this test process is the child of a process registered
// as an agent with its verified pid and start, so it is refused; with that
// registration gone it is the person's.
func TestAgentAncestorFindsARegisteredParent(t *testing.T) {
	b := notifyBus(t)
	ctx := context.Background()
	ppid := os.Getppid()
	start, err := dispatch.ProcStart(ppid)
	if err != nil {
		t.Skip("no /proc: ancestry is Linux only")
	}
	if got, err := agentAncestor(ctx); err != nil || got != "" {
		t.Fatalf("before any registration: %q %v", got, err)
	}
	if _, err := b.Status(ctx, "coder", "working", "x", bus.AgentIdent{PID: ppid, PIDStart: start}); err != nil {
		t.Fatal(err)
	}
	if got, err := agentAncestor(ctx); err != nil || got != b.Project()+"/coder" {
		t.Fatalf("an agent's child: %q %v", got, err)
	}
	if _, err := b.Status(ctx, "coder", "working", "x", bus.AgentIdent{PID: ppid, PIDStart: start + 1}); err != nil {
		t.Fatal(err)
	}
	if got, err := agentAncestor(ctx); err != nil || got != "" { // another start: a recycled pid is not the agent
		t.Fatalf("another start matched: %q %v", got, err)
	}
}
