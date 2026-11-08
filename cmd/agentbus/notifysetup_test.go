package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/notify"
)

// notify-guided-setup §6: the CLI twin. Every case runs in a temporary home;
// the ntfy server is the world's loopback one, Discord is never reached.

// fakeTerm is the controlling terminal: what was shown, and the answers.
type fakeTerm struct {
	shown   bytes.Buffer
	lines   []string
	secrets []string
}

func (f *fakeTerm) Write(p []byte) (int, error) { return f.shown.Write(p) }

func (f *fakeTerm) ReadLine() (string, error) {
	if len(f.lines) == 0 {
		return "", errors.New("no more answers")
	}
	l := f.lines[0]
	f.lines = f.lines[1:]
	return l, nil
}

func (f *fakeTerm) ReadSecret() (string, error) {
	if len(f.secrets) == 0 {
		return "", errors.New("no more secrets")
	}
	s := f.secrets[0]
	f.secrets = f.secrets[1:]
	return s, nil
}

type setupRun struct {
	out, err string
	code     int
	term     *fakeTerm
}

// setup runs args with term (nil = no controlling terminal) and stdin.
func (w *notifyWorld) setup(t *testing.T, term *fakeTerm, stdin string, args ...string) setupRun {
	t.Helper()
	d := w.deps()
	if term != nil {
		d.Term = func() (notifyTerm, func(), error) { return term, func() {}, nil }
	}
	d.Stdin = strings.NewReader(stdin)
	d.StdinTerminal = term != nil
	var out, errw bytes.Buffer
	code := runNotifications(context.Background(), args, d, &out, &errw)
	return setupRun{out.String(), errw.String(), code, term}
}

func (w *notifyWorld) file(t *testing.T) *notify.File {
	t.Helper()
	return notify.Load(filepath.Join(w.cfg, "notify.toml"))
}

func (w *notifyWorld) settings() (bool, []string) {
	r := config.Machine(w.deps().Getenv)
	return r.Bool("notify.on"), config.SplitList(r.String("notify.targets"))
}

// drawnTopic is the topic a write drew.
func drawnTopic(t *testing.T, f *notify.File, name string) string {
	t.Helper()
	tg, ok := f.Target(name)
	if !ok || !regexp.MustCompile(`^agentbus-[a-z0-9]{20}$`).MatchString(tg.Topic) {
		t.Fatalf("no drawn topic for %s (%v, %q)", name, ok, f.Refused)
	}
	return tg.Topic
}

// noSecret fails when s holds the topic, a URL or a token.
func noSecret(t *testing.T, where, s string, secrets ...string) {
	t.Helper()
	for _, leak := range append(secrets, "http://", "https://", "agentbus-") {
		if leak != "" && strings.Contains(s, leak) {
			t.Fatalf("%s holds %q:\n%s", where, leak, s)
		}
	}
}

// add ntfy on the person's own server: written, turned on and listed, the
// topic shown on the terminal only, the test delivered: exit 0.
func TestAddNtfyWritesShowsAndTests(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", "# my targets\n")
	term := &fakeTerm{}
	r := w.setup(t, term, "", "add", "ntfy", "--server", w.srv.URL, "--insecure", "--self-hosted")
	if r.code != 0 {
		t.Fatalf("exit %d: %s %s", r.code, r.out, r.err)
	}
	f := w.file(t)
	topic := drawnTopic(t, f, "phone")
	if tg, _ := f.Target("phone"); !tg.SelfHosted || tg.Server != w.srv.URL {
		t.Errorf("target %+v", tg)
	}
	if b, _ := os.ReadFile(filepath.Join(w.cfg, "notify.toml")); !strings.HasPrefix(string(b), "# my targets\n") {
		t.Errorf("the person's bytes lost:\n%s", b)
	}
	if on, targets := w.settings(); !on || strings.Join(targets, ",") != "phone" {
		t.Errorf("notify.on %v, targets %q", on, targets)
	}
	if len(w.got) != 1 {
		t.Fatalf("%d sends", len(w.got))
	}
	shown := term.shown.String()
	for _, want := range []string{topic, w.srv.URL + "/" + topic, "▀", "scan the code", "the topic is a password: clear the screen when you are done"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the terminal lacks %q:\n%s", want, shown)
		}
	}
	noSecret(t, "stdout", r.out, topic)
	noSecret(t, "stderr", r.err, topic)
	if !strings.Contains(r.out, "phone: delivered") {
		t.Errorf("stdout %q", r.out)
	}
}

// With no controlling terminal the topic is shown nowhere: stdout says how
// to see it; the target is written and tested all the same.
func TestAddNtfyWithoutATerminal(t *testing.T) {
	w := newNotifyWorld(t)
	r := w.setup(t, nil, "", "add", "ntfy", "--server", w.srv.URL, "--insecure", "--name", "pager")
	if r.code != 0 {
		t.Fatalf("exit %d: %s %s", r.code, r.out, r.err)
	}
	topic := drawnTopic(t, w.file(t), "pager")
	if !strings.Contains(r.out, "the topic was not shown (no terminal): run agentbus notifications show-topic pager on a terminal") {
		t.Errorf("stdout %q", r.out)
	}
	noSecret(t, "stdout", r.out, topic)
	noSecret(t, "stderr", r.err, topic)
}

// The exit codes (§6): 2 when written but the test failed, said in plain
// words; --no-test sends nothing; 1 when nothing was written.
func TestAddExitCodes(t *testing.T) {
	w := newNotifyWorld(t)
	w.status = 401
	r := w.setup(t, nil, "", "add", "ntfy", "--server", w.srv.URL, "--insecure")
	if r.code != 2 || !strings.Contains(r.out, "phone: the server refused the token (or this topic needs one)") {
		t.Fatalf("failed test: exit %d %q", r.code, r.out)
	}
	if _, ok := w.file(t).Target("phone"); !ok {
		t.Fatal("not written")
	}
	w.status = 200
	n := len(w.got)
	if r = w.setup(t, nil, "", "add", "ntfy", "--server", w.srv.URL, "--insecure", "--no-test"); r.code != 0 || len(w.got) != n {
		t.Fatalf("--no-test: exit %d, %d sends", r.code, len(w.got)-n)
	}
	if _, ok := w.file(t).Target("phone-2"); !ok {
		t.Fatal("the second target is not phone-2")
	}
	if _, targets := w.settings(); strings.Join(targets, ",") != "phone,phone-2" {
		t.Errorf("targets %q", targets)
	}
	before, _ := os.ReadFile(filepath.Join(w.cfg, "notify.toml"))
	for name, args := range map[string][]string{
		"a taken name":              {"add", "ntfy", "--name", "phone", "--server", w.srv.URL, "--insecure"},
		"a bad name":                {"add", "ntfy", "--name", "Phone!"},
		"http without --insecure":   {"add", "ntfy", "--server", w.srv.URL},
		"--insecure on https":       {"add", "ntfy", "--insecure"},
		"self-hosted ntfy.sh":       {"add", "ntfy", "--self-hosted"},
		"a server Load refuses":     {"add", "ntfy", "--server", "ftp://example.org"},
		"a missing token file":      {"add", "ntfy", "--token-file", filepath.Join(w.home, "none")},
		"discord without a webhook": {"add", "discord"},
		"a webhook Load refuses":    {"add", "discord", "--webhook-stdin"},
		"two webhook sources":       {"add", "discord", "--webhook-stdin", "--webhook-file", "-"},
	} {
		r := w.setup(t, nil, "https://evil.example/api/webhooks/1/x\n", args...)
		if r.code != 1 {
			t.Errorf("%s: exit %d %q %q", name, r.code, r.out, r.err)
		}
		noSecret(t, name, r.out+r.err, "evil")
	}
	if after, _ := os.ReadFile(filepath.Join(w.cfg, "notify.toml")); !bytes.Equal(before, after) {
		t.Error("a refused add wrote the file")
	}
}

// No secret on the command line (§6): a token or a webhook comes from a file
// or stdin; a flag that would carry one is a usage error that echoes nothing.
func TestAddSecretsFromFilesOnly(t *testing.T) {
	w := newNotifyWorld(t)
	tok := filepath.Join(w.home, "token")
	if err := os.WriteFile(tok, []byte("tk_from_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := w.setup(t, nil, "", "add", "ntfy", "--server", w.srv.URL, "--insecure", "--token-file", tok, "--no-test")
	if tg, _ := w.file(t).Target("phone"); r.code != 0 || tg.Token != "tk_from_file" {
		t.Fatalf("token file: exit %d %q", r.code, r.err)
	}
	hook := "https://discord.com/api/webhooks/42/hook-from-stdin"
	r = w.setup(t, nil, hook+"\n", "add", "discord", "--webhook-stdin", "--no-test")
	if tg, _ := w.file(t).Target("team"); r.code != 0 || tg.Webhook != hook {
		t.Fatalf("webhook stdin: exit %d %q", r.code, r.err)
	}
	r = w.setup(t, nil, hook+"\n", "add", "discord", "--name", "ops", "--webhook-file", "-", "--no-test")
	if _, ok := w.file(t).Target("ops"); r.code != 0 || !ok {
		t.Fatalf("webhook file -: exit %d %q", r.code, r.err)
	}
	noSecret(t, "output", r.out+r.err, "hook-from-stdin", "tk_from_file")
	for _, args := range [][]string{{"add", "discord", "--webhook", hook}, {"add", "ntfy", "--token", "tk_x"}, {"add", "ntfy", "--topic", "agentbus-mine"}} {
		r := w.setup(t, nil, "", args...)
		if r.code != 1 { // nothing written: never 2, a written target whose test failed (§6)
			t.Errorf("%q: exit %d", args, r.code)
		}
		noSecret(t, "usage", r.out+r.err, "hook-from-stdin", "tk_x", "agentbus-mine")
	}
}

// add desktop: nothing asked; without notify-send nothing is written.
func TestAddDesktop(t *testing.T) {
	w := newNotifyWorld(t)
	d := w.deps()
	d.Desktop = nil
	var out, errw bytes.Buffer
	if code := runNotifications(context.Background(), []string{"add", "desktop"}, d, &out, &errw); code != 1 ||
		!strings.Contains(errw.String(), "install libnotify (notify-send) first") || w.file(t).Exists {
		t.Fatalf("no notify-send: exit %d %q", code, errw.String())
	}
	r := w.setup(t, nil, "", "add", "desktop")
	if _, ok := w.file(t).Target("here"); r.code != 0 || !ok || len(w.desktop) != 1 {
		t.Fatalf("desktop: exit %d %q, %d shown", r.code, r.err, len(w.desktop))
	}
}

// remove: asked on the terminal (or --yes); the table and its listing go;
// notify.on changes only on the person's yes when none is left listed.
func TestRemove(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	w.write(t, "config.toml", "[notify]\non = true\ntargets = [\"phone\", \"here\"]\n")
	if r := w.setup(t, &fakeTerm{lines: []string{"n"}}, "", "remove", "phone"); r.code != 1 {
		t.Fatalf("no: exit %d", r.code)
	}
	if _, ok := w.file(t).Target("phone"); !ok {
		t.Fatal("removed on no")
	}
	if r := w.setup(t, nil, "", "remove", "phone"); r.code != 1 || !strings.Contains(r.err, "--yes") {
		t.Fatalf("no terminal, no --yes: exit %d %q", r.code, r.err)
	}
	term := &fakeTerm{lines: []string{"y"}}
	if r := w.setup(t, term, "", "remove", "phone"); r.code != 0 || !strings.Contains(term.shown.String(), "remove phone? its topic is lost, y/n") {
		t.Fatalf("yes: exit %d %q %q", r.code, r.err, term.shown.String())
	}
	if _, ok := w.file(t).Target("phone"); ok {
		t.Fatal("not removed")
	}
	if on, targets := w.settings(); !on || strings.Join(targets, ",") != "here" {
		t.Fatalf("after phone: %v %q", on, targets)
	}
	// The last listed one: asked whether to turn notifications off.
	if r := w.setup(t, &fakeTerm{lines: []string{"y", "n"}}, "", "remove", "here"); r.code != 0 {
		t.Fatalf("here: exit %d", r.code)
	}
	if on, targets := w.settings(); !on || len(targets) != 0 {
		t.Fatalf("kept on: %v %q", on, targets)
	}
	if r := w.setup(t, nil, "", "remove", "team", "--yes"); r.code != 0 {
		t.Fatalf("--yes: exit %d %q", r.code, r.err)
	}
	if on, _ := w.settings(); !on {
		t.Fatal("--yes turned notifications off: only the person's yes does")
	}
	w.write(t, "notify.toml", w.notifyToml())
	w.write(t, "config.toml", "[notify]\non = true\ntargets = [\"phone\"]\n")
	if r := w.setup(t, &fakeTerm{lines: []string{"y", "y"}}, "", "remove", "phone"); r.code != 0 {
		t.Fatalf("off: exit %d", r.code)
	}
	if on, _ := w.settings(); on {
		t.Fatal("not turned off on yes")
	}
	if r := w.setup(t, nil, "", "remove", "nobody", "--yes"); r.code != 1 {
		t.Fatalf("unknown: exit %d", r.code)
	}
}

// rename: the table's header, its listing and its record move together.
func TestRename(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	w.write(t, "config.toml", "[notify]\non = true\ntargets = [\"here\", \"phone\"]\n")
	if _, _, code := w.run(t, "test", "phone"); code != 0 {
		t.Fatal("seed test")
	}
	if r := w.setup(t, nil, "", "rename", "phone", "mobile"); r.code != 0 {
		t.Fatalf("exit %d %q", r.code, r.err)
	}
	f := w.file(t)
	if _, ok := f.Target("mobile"); !ok {
		t.Fatal("not renamed")
	}
	if _, ok := f.Target("phone"); ok {
		t.Fatal("the old name stays")
	}
	if _, targets := w.settings(); strings.Join(targets, ",") != "here,mobile" {
		t.Errorf("targets %q", targets)
	}
	last, _ := notify.ReadLast(notify.LastPath(filepath.Join(w.state, "agentbus")))
	if o, ok := last.Latest("mobile"); !ok || !o.Test {
		t.Errorf("the last test did not move: %+v %v", o, ok)
	}
	for _, args := range [][]string{{"rename", "mobile", "here"}, {"rename", "nobody", "x"}, {"rename", "mobile", "Bad"}} {
		if r := w.setup(t, nil, "", args...); r.code != 1 {
			t.Errorf("%q: exit %d", args, r.code)
		}
	}
}

// show-topic: on the terminal only, ntfy only.
func TestShowTopic(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	term := &fakeTerm{}
	r := w.setup(t, term, "", "show-topic", "phone")
	if r.code != 0 || !strings.Contains(term.shown.String(), "agentbus-secret-topic") || !strings.Contains(term.shown.String(), "█") {
		t.Fatalf("exit %d %q", r.code, term.shown.String())
	}
	noLeak(t, r.out+r.err)
	if r := w.setup(t, nil, "", "show-topic", "phone"); r.code != 1 || !strings.Contains(r.err, "terminal") {
		t.Fatalf("no terminal: exit %d %q", r.code, r.err)
	}
	if r := w.setup(t, &fakeTerm{}, "", "show-topic", "team"); r.code != 1 || !strings.Contains(r.err, "no topic") {
		t.Fatalf("discord: exit %d %q", r.code, r.err)
	}
}

// setup: the same questions on a terminal; refused when stdin is not one.
func TestSetupInteractive(t *testing.T) {
	w := newNotifyWorld(t)
	if r := w.setup(t, nil, "", "setup"); r.code != 1 || !strings.Contains(r.err, "use agentbus notifications add") {
		t.Fatalf("no terminal: exit %d %q", r.code, r.err)
	}
	// ntfy on an own server: kind, server choice, address, its name, yours?,
	// then Enter once subscribed; the token is read hidden.
	term := &fakeTerm{lines: []string{"1", "2", w.srv.URL, "y", "", ""}, secrets: []string{"tk_typed"}}
	r := w.setup(t, term, "", "setup")
	if r.code != 0 {
		t.Fatalf("exit %d %q\n%s", r.code, r.err, term.shown.String())
	}
	tg, ok := w.file(t).Target("phone")
	if !ok || !tg.SelfHosted || tg.Token != "tk_typed" || tg.Server != w.srv.URL || len(w.got) != 1 {
		t.Fatalf("target %v %+v, %d sends", ok, tg, len(w.got))
	}
	if !strings.Contains(term.shown.String(), tg.Topic) || strings.Contains(term.shown.String(), "tk_typed") {
		t.Fatalf("terminal:\n%s", term.shown.String())
	}
	noSecret(t, "stdout", r.out+r.err, tg.Topic, "tk_typed")
	// Discord: the webhook is pasted hidden.
	hook := "https://discord.com/api/webhooks/7/pasted"
	term = &fakeTerm{lines: []string{"2", ""}, secrets: []string{hook}}
	if r := w.setup(t, term, "", "setup", "--no-test"); r.code != 0 {
		t.Fatalf("discord: exit %d %q", r.code, r.err)
	}
	if tg, _ := w.file(t).Target("team"); tg.Webhook != hook || strings.Contains(term.shown.String(), "pasted") {
		t.Fatalf("discord target, or the webhook echoed:\n%s", term.shown.String())
	}
}

// §2: every writing or revealing subcommand refuses an agent before it reads
// anything, by AGENT_BUS_AGENT or by an agent ancestor, and a check that
// cannot be made refuses too.
func TestSetupRefusesAnAgent(t *testing.T) {
	cmds := [][]string{{"add", "ntfy"}, {"add", "desktop"}, {"remove", "phone", "--yes"}, {"rename", "phone", "x"}, {"show-topic", "phone"}, {"setup"}}
	for _, args := range cmds {
		for name, set := range map[string]func(*notificationsDeps){
			"env": func(d *notificationsDeps) {
				get := d.Getenv
				d.Getenv = func(k string) string {
					if k == "AGENT_BUS_AGENT" {
						return "coder"
					}
					return get(k)
				}
			},
			"ancestor": func(d *notificationsDeps) {
				d.AgentCaller = func(context.Context) (string, error) { return "coder", nil }
			},
			"unchecked": func(d *notificationsDeps) {
				d.AgentCaller = func(context.Context) (string, error) { return "", errors.New("down") }
			},
		} {
			w := newNotifyWorld(t)
			w.write(t, "notify.toml", w.notifyToml())
			before, _ := os.ReadFile(filepath.Join(w.cfg, "notify.toml"))
			d := w.deps()
			term := &fakeTerm{lines: []string{"y", "y"}}
			d.Term = func() (notifyTerm, func(), error) { return term, func() {}, nil }
			d.StdinTerminal = true
			set(&d)
			var out, errw bytes.Buffer
			code := runNotifications(context.Background(), args, d, &out, &errw)
			after, _ := os.ReadFile(filepath.Join(w.cfg, "notify.toml"))
			if code != 1 || !bytes.Equal(before, after) || term.shown.Len() != 0 || len(w.got) != 0 {
				t.Errorf("%q by %s: exit %d %q, shown %q", args, name, code, errw.String(), term.shown.String())
			}
		}
	}
}

func TestSetupUsage(t *testing.T) {
	w := newNotifyWorld(t)
	for _, args := range [][]string{{"add"}, {"add", "slack"}, {"add", "ntfy", "--bogus"}, {"remove"}, {"rename", "a"}, {"show-topic"}, {"setup", "x"}, {"add", "ntfy", "--server"}} {
		if r := w.setup(t, nil, "", args...); r.code != 1 || !strings.Contains(r.err, "usage: agentbus notifications setup") {
			t.Errorf("%q: exit %d %q", args, r.code, r.err)
		}
	}
}

var _ = time.Second

// The mutation survivors' cases: --yes never turns notifications off, even
// on a terminal; an unknown name is refused before any question; a rename
// never turns notifications on, and never lists a name twice; setup needs
// stdin on a terminal too; a secret file's first line only.
func TestSetupEdges(t *testing.T) {
	w := newNotifyWorld(t)
	w.write(t, "notify.toml", w.notifyToml())
	w.write(t, "config.toml", "[notify]\non = true\ntargets = [\"phone\"]\n")
	if r := w.setup(t, &fakeTerm{lines: []string{"y", "y"}}, "", "remove", "phone", "--yes"); r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	if on, _ := w.settings(); !on {
		t.Fatal("--yes turned notifications off on a terminal")
	}
	term := &fakeTerm{lines: []string{"y"}}
	if r := w.setup(t, term, "", "remove", "nobody"); r.code != 1 || term.shown.Len() != 0 {
		t.Fatalf("unknown: exit %d, asked %q", r.code, term.shown.String())
	}
	w.write(t, "config.toml", "[notify]\non = false\ntargets = [\"here\", \"mobile\"]\n")
	if r := w.setup(t, nil, "", "rename", "here", "mobile"); r.code != 0 {
		t.Fatalf("rename onto a listed name absent from the file: exit %d %q", r.code, r.err)
	}
	if on, targets := w.settings(); on || strings.Join(targets, ",") != "mobile" {
		t.Fatalf("after rename: on %v, targets %q", on, targets)
	}
	d := w.deps()
	d.Term = func() (notifyTerm, func(), error) { return &fakeTerm{lines: []string{"3", ""}}, func() {}, nil }
	var out, errw bytes.Buffer
	if code := runNotifications(context.Background(), []string{"setup"}, d, &out, &errw); code != 1 || !strings.Contains(errw.String(), "use agentbus notifications add") {
		t.Fatalf("setup with stdin not a terminal: exit %d %q", code, errw.String())
	}
	tok := filepath.Join(w.home, "token2")
	if err := os.WriteFile(tok, []byte("tk_first\nsecond line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := w.setup(t, nil, "", "add", "ntfy", "--name", "two", "--server", w.srv.URL, "--insecure", "--token-file", tok, "--no-test"); r.code != 0 {
		t.Fatalf("exit %d %q", r.code, r.err)
	}
	if tg, _ := w.file(t).Target("two"); tg.Token != "tk_first" {
		t.Fatalf("token %q", tg.Token)
	}
}

// reviewer-codex: a global flag before the command ("agentbus --host x
// notifications add …", "-p other notifications show-topic …") would escape
// the plain denies. The writing and revealing subcommands take none: they
// refuse one before anything, so a deny of the plain form covers every
// spelling that runs. status and test are unchanged.
func TestNotifySetupRefusesGlobalFlags(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agentbus")
	if out, err := runOut("go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	home := t.TempDir()
	for _, args := range [][]string{
		{"--host", "ignored", "notifications", "add", "desktop"},
		{"-H", "ignored", "notifications", "show-topic", "phone"},
		{"--host=ignored", "notifications", "setup"},
		{"-p", "other", "notifications", "add", "ntfy"},
		{"--project", "other", "--host", "x", "notifications", "remove", "phone", "--yes"},
		{"--project=other", "notifications", "rename", "a", "b"},
		// reviewer-codex: an empty value is still a flag, and a flag after the
		// command name is read as a global one too.
		{"--host", "", "notifications", "add", "desktop"},
		{"-H", "", "notifications", "add", "desktop"},
		{"--project", "", "notifications", "add", "desktop"},
		{"-p", "", "notifications", "show-topic", "phone"},
		{"--host=", "notifications", "add", "desktop"},
		{"notifications", "--host", "x", "add", "desktop"},
		{"notifications", "--project", "", "setup"},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "REDIS_HOST=127.0.0.1", "REDIS_PORT=1"}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 || !strings.Contains(stderr.String(), "takes no --project or --host") {
			t.Errorf("%q: %v %q", args, err, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "agentbus", "notify.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused command wrote notify.toml: %v", err)
	}
	// The deps' own rule, before the agent check.
	w := newNotifyWorld(t)
	d := w.deps()
	d.GlobalFlags = true
	d.AgentCaller = func(context.Context) (string, error) { t.Fatal("checked the agent first"); return "", nil }
	var out, errw bytes.Buffer
	if code := runNotifications(context.Background(), []string{"add", "desktop"}, d, &out, &errw); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if code := runNotifications(context.Background(), []string{"status"}, d, &out, &errw); code != 0 {
		t.Fatalf("status with a global flag: exit %d", code)
	}
}

// A short global flag after the command is no global flag there: it is the
// subcommand's word, a usage error (exit 1, never 2: foureyes), and nothing runs.
func TestNotifySetupShortFlagAfterTheCommand(t *testing.T) {
	w := newNotifyWorld(t)
	if r := w.setup(t, &fakeTerm{}, "", "-p", "", "setup"); r.code != 1 || w.file(t).Exists {
		t.Fatalf("exit %d", r.code)
	}
}
