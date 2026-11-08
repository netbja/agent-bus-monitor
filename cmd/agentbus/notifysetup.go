package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/notify"
)

// agentbus notifications setup | add | remove | rename | show-topic
// (notify-guided-setup §6): the person writes notify.toml without editing
// it. Every one of them refuses an agent first (§2). No secret is read from
// the command line (a token or a webhook comes from a file, stdin or a
// hidden prompt), and a topic is shown only on the controlling terminal,
// never on stdout or stderr (§4).

// notifyTerm is the controlling terminal: shown text, a typed line, a typed
// secret (not echoed).
type notifyTerm interface {
	io.Writer
	ReadLine() (string, error)
	ReadSecret() (string, error)
}

// ttyTerm is /dev/tty.
type ttyTerm struct {
	f *os.File
	r *bufio.Reader
}

func (t *ttyTerm) Write(p []byte) (int, error) { return t.f.Write(p) }

func (t *ttyTerm) ReadLine() (string, error) {
	l, err := t.r.ReadString('\n')
	if err != nil && l == "" {
		return "", err
	}
	return strings.TrimRight(l, "\r\n"), nil
}

func (t *ttyTerm) ReadSecret() (string, error) {
	b, err := term.ReadPassword(int(t.f.Fd()))
	fmt.Fprintln(t.f)
	return strings.TrimSpace(string(b)), err
}

// openTTY is the controlling terminal, or an error when there is none (a
// script, cron, a service).
func openTTY() (notifyTerm, func(), error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	return &ttyTerm{f: f, r: bufio.NewReader(f)}, func() { f.Close() }, nil
}

func stdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// errUsage is a malformed command line (exit 2); every other error exits 1.
var errUsage = errors.New("usage")

const notifySetupUsage = `usage: agentbus notifications setup [--no-test]
       agentbus notifications add ntfy [--name N] [--server URL] [--token-file F] [--self-hosted] [--insecure] [--no-test]
       agentbus notifications add discord [--name N] (--webhook-file F | --webhook-stdin) [--no-test]
       agentbus notifications add desktop [--name N] [--no-test]
       agentbus notifications remove <name> [--yes]
       agentbus notifications rename <old> <new>
       agentbus notifications show-topic <name>`

// refuseAgent is §2: the person's act, refused for an agent caller by its
// environment or its process ancestry; an ancestry that cannot be read
// refuses too (as test does). true = refused, said on errw.
func refuseAgent(ctx context.Context, d notificationsDeps, errw io.Writer) bool {
	if self := d.Getenv("AGENT_BUS_AGENT"); self != "" {
		fmt.Fprintf(errw, "agentbus notifications: setting up a target is the person's act, not %s's (run it from your own shell, without AGENT_BUS_AGENT)\n", self)
		return true
	}
	if d.AgentCaller == nil {
		return false
	}
	switch caller, err := d.AgentCaller(ctx); {
	case errors.Is(err, errNoAncestry):
	case err != nil:
		fmt.Fprintf(errw, "agentbus notifications: setting up a target needs the broker: it checks that no registered agent runs it, and could not read the agents (%v). Start the broker (docker compose up -d in the agent-bus checkout) or check REDIS_URL, then run it again.\n", err)
		return true
	case caller != "":
		fmt.Fprintf(errw, "agentbus notifications: setting up a target is the person's act, and this runs under the agent %s\n", caller)
		return true
	}
	return false
}

// runNotifySetup handles the writing and revealing subcommands; ok=false
// when args is none of them.
func runNotifySetup(ctx context.Context, args []string, d notificationsDeps, out, errw io.Writer) (int, bool) {
	if len(args) == 0 || !slices.Contains([]string{"setup", "add", "remove", "rename", "show-topic"}, args[0]) {
		return 0, false
	}
	if refuseAgent(ctx, d, errw) {
		return 1, true
	}
	var err error
	code := 0
	switch args[0] {
	case "setup":
		code, err = notifySetup(ctx, args[1:], d, out)
	case "add":
		code, err = notifyAdd(ctx, args[1:], d, out)
	case "remove":
		err = notifyRemove(args[1:], d, out)
	case "rename":
		err = notifyRename(args[1:], d, out)
	case "show-topic":
		err = notifyShowTopic(args[1:], d)
	}
	switch {
	case errors.Is(err, errUsage):
		fmt.Fprintln(errw, notifySetupUsage)
		return 2, true
	case err != nil:
		fmt.Fprintln(errw, "agentbus notifications:", err)
		return 1, true
	}
	return code, true
}

// addOpts is one target to add.
type addOpts struct {
	kind, name, server, tokenFile, webhookFile string
	webhookStdin, selfHosted, insecure, noTest bool
	nameSet, serverSet                         bool
}

func parseAdd(args []string) (addOpts, error) {
	if len(args) == 0 || !slices.Contains([]string{"ntfy", "discord", "desktop"}, args[0]) {
		return addOpts{}, errUsage
	}
	o := addOpts{kind: args[0]}
	allowed := map[string][]string{
		"ntfy":    {"--name", "--server", "--token-file", "--self-hosted", "--insecure", "--no-test"},
		"discord": {"--name", "--webhook-file", "--webhook-stdin", "--no-test"},
		"desktop": {"--name", "--no-test"},
	}[o.kind]
	rest := args[1:]
	value := func() (string, error) {
		if len(rest) < 2 || rest[1] == "" {
			return "", errUsage
		}
		v := rest[1]
		rest = rest[1:]
		return v, nil
	}
	for ; len(rest) > 0; rest = rest[1:] {
		flag := rest[0]
		if !slices.Contains(allowed, flag) {
			return addOpts{}, errUsage // never echoed: it may be a secret
		}
		var err error
		switch flag {
		case "--name":
			o.name, err = value()
			o.nameSet = true
		case "--server":
			o.server, err = value()
			o.serverSet = true
		case "--token-file":
			o.tokenFile, err = value()
		case "--webhook-file":
			o.webhookFile, err = value()
		case "--webhook-stdin":
			o.webhookStdin = true
		case "--self-hosted":
			o.selfHosted = true
		case "--insecure":
			o.insecure = true
		case "--no-test":
			o.noTest = true
		}
		if err != nil {
			return addOpts{}, err
		}
	}
	return o, nil
}

// readSecretFile reads a token or a webhook from a file, "-" being stdin:
// its first line, trimmed.
func readSecretFile(path string, d notificationsDeps) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		if d.Stdin == nil {
			return "", fmt.Errorf("nothing on stdin")
		}
		b, err = io.ReadAll(io.LimitReader(d.Stdin, 64<<10))
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("cannot read the secret's file") // the path may say too much: never echoed
	}
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(line), nil
}

func notifyAdd(ctx context.Context, args []string, d notificationsDeps, out io.Writer) (int, error) {
	o, err := parseAdd(args)
	if err != nil {
		return 0, err
	}
	t := notify.Target{Kind: o.kind}
	switch o.kind {
	case "ntfy":
		t.Server = notify.DefaultServer
		if o.serverSet {
			t.Server = strings.TrimRight(o.server, "/")
		}
		u, err := url.Parse(t.Server)
		if err != nil {
			return 0, fmt.Errorf("the server is not an address")
		}
		switch {
		case u.Scheme == "http" && !o.insecure:
			return 0, fmt.Errorf("this server has no TLS (http), so it sends in clear: pass --insecure to accept it (a loopback, .local or private address only)")
		case u.Scheme != "http" && o.insecure:
			return 0, fmt.Errorf("--insecure is only for a server without TLS (http)")
		}
		t.SelfHosted = o.selfHosted
		if o.tokenFile != "" {
			if t.Token, err = readSecretFile(o.tokenFile, d); err != nil {
				return 0, err
			}
		}
	case "discord":
		switch {
		case o.webhookStdin && o.webhookFile != "":
			return 0, fmt.Errorf("give the webhook once: --webhook-file or --webhook-stdin")
		case o.webhookStdin:
			o.webhookFile = "-"
		case o.webhookFile == "":
			return 0, fmt.Errorf("a Discord target needs its webhook: --webhook-file F or --webhook-stdin (never on the command line)")
		}
		if t.Webhook, err = readSecretFile(o.webhookFile, d); err != nil {
			return 0, err
		}
	case "desktop":
		if d.Desktop == nil {
			return 0, fmt.Errorf("install libnotify (notify-send) first")
		}
	}
	return addTarget(ctx, t, o.name, !o.noTest, d, out, nil)
}

// defaultNames are each kind's usual target name (§3).
var defaultNames = map[string]string{"ntfy": "phone", "discord": "team", "desktop": "here"}

// freeName is base, numbered when notify.toml already holds it.
func freeName(f *notify.File, base string) string {
	taken := map[string]bool{}
	for _, t := range f.Targets {
		taken[t.Name] = true
	}
	for _, w := range f.Warnings { // a refused table still holds its name
		if _, rest, ok := strings.Cut(w.Key, "."); ok {
			name, _, _ := strings.Cut(rest, ".")
			taken[name] = true
		}
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// addTarget writes t (a drawn topic for ntfy), turns notifications on and
// lists it, shows the topic on the terminal and tests it. tm is setup's
// terminal (it waits for Enter once subscribed), nil for add.
func addTarget(ctx context.Context, t notify.Target, name string, test bool, d notificationsDeps, out io.Writer, tm notifyTerm) (int, error) {
	path := notify.Path(d.Getenv)
	f := notify.Load(path)
	if f.Refused != "" {
		return 0, fmt.Errorf("notify.toml refused: %s", f.Refused)
	}
	if name == "" {
		name = freeName(f, defaultNames[t.Kind])
	}
	t.Name = name
	if t.Kind == "ntfy" {
		t.Topic = notify.DrawTopic()
	}
	if err := notify.Edit(path, func(doc *notify.Doc) error { return doc.Add(t) }); err != nil {
		return 0, fmt.Errorf("notify.toml was not changed: %v", err)
	}
	fmt.Fprintf(out, "%s: written to notify.toml\n", name)
	if err := listTarget(d.Getenv, name, ""); err != nil {
		fmt.Fprintln(out, "the target is written, but config.toml was not changed:", err)
	}
	if t.Kind == "ntfy" {
		tt := tm
		if tt == nil {
			if open := d.Term; open != nil {
				if got, done, err := open(); err == nil {
					defer done()
					tt = got
				}
			}
		}
		if tt == nil {
			fmt.Fprintf(out, "the topic was not shown (no terminal): run agentbus notifications show-topic %s on a terminal\n", name)
		} else {
			showTopic(tt, t)
			if tm != nil {
				fmt.Fprint(tm, "press Enter once subscribed ")
				_, _ = tm.ReadLine()
			}
		}
	}
	if !test {
		return 0, nil
	}
	if !testOne(ctx, t, d, out) {
		return 2, nil
	}
	return 0, nil
}

// listTarget adds name to notify.targets once (renaming old when set) and
// turns notifications on (§3 step 4, the person's answer 5).
func listTarget(getenv func(string) string, name, old string) error {
	path := config.Path(getenv)
	listed := config.SplitList(config.Machine(getenv).String("notify.targets"))
	out := []string{}
	for _, n := range listed {
		if n == old {
			n = name
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if !slices.Contains(out, name) {
		if old != "" { // a rename of a target that was not listed lists nothing
			return nil
		}
		out = append(out, name)
	}
	if err := config.Set(path, "notify.targets", out); err != nil {
		return err
	}
	if old != "" {
		return nil
	}
	return config.Set(path, "notify.on", true)
}

// testOne sends the test message to t and records it; the outcome on out
// in plain words (§7), never a URL.
func testOne(ctx context.Context, t notify.Target, d notificationsDeps, out io.Writer) bool {
	host, err := d.Hostname()
	if err != nil || host == "" {
		host = "this host"
	}
	msg := notify.Message{Title: "agentbus: a test message from " + host, Priority: notify.Default,
		Lines: []string{"If you read this, the target " + t.Name + " works."}}
	err = t.Sender(d.Desktop).Send(ctx, msg)
	o := notify.Outcome{At: d.Now(), OK: err == nil, Test: true}
	if err != nil {
		o.Err = err.Error()
		fmt.Fprintf(out, "%s: %s\n", t.Name, notify.Explain(t.Kind, err))
	} else if t.Kind == "desktop" {
		fmt.Fprintf(out, "%s: delivered: check your screen\n", t.Name)
	} else {
		fmt.Fprintf(out, "%s: delivered: check your phone\n", t.Name)
	}
	if state := notifyStateDir(d.Getenv); state != "" {
		_ = notify.UpdateLast(notify.LastPath(state), func(l *notify.Last) error {
			l.Add(t.Name, o, false)
			return nil
		})
	}
	return err == nil
}

// showTopic is §3 step 3 on the terminal: the topic, its link with the
// server's own scheme, the code drawn dark on light, and how to subscribe.
func showTopic(tt io.Writer, t notify.Target) {
	link := notify.SubscribeLink(t)
	fmt.Fprintf(tt, "\nthe topic: %s\nits link:  %s\n\n", t.Topic, link)
	if rows, err := notify.QR(link); err == nil {
		for _, r := range rows {
			fmt.Fprintf(tt, "  \x1b[30;47m%s\x1b[0m\n", r) // black on white whatever the theme (§5.3)
		}
		fmt.Fprintln(tt)
	}
	fmt.Fprintln(tt, "in the ntfy app: + → subscribe to topic → paste the topic, or scan the code with the phone's camera")
	fmt.Fprintln(tt, "the topic is a password: clear the screen when you are done")
}

// confirm asks a y/n question on the terminal: true on y or yes.
func confirm(tt notifyTerm, q string) bool {
	fmt.Fprint(tt, q+" ")
	a, err := tt.ReadLine()
	a = strings.ToLower(strings.TrimSpace(a))
	return err == nil && (a == "y" || a == "yes")
}

func notifyRemove(args []string, d notificationsDeps, out io.Writer) error {
	yes := false
	var names []string
	for _, a := range args {
		if a == "--yes" {
			yes = true
		} else {
			names = append(names, a)
		}
	}
	if len(names) != 1 || strings.HasPrefix(names[0], "-") {
		return errUsage
	}
	name := names[0]
	path := notify.Path(d.Getenv)
	if f := notify.Load(path); f.Refused != "" {
		return fmt.Errorf("notify.toml refused: %s", f.Refused)
	} else if freeName(f, name) == name { // a refused table still holds its name
		return fmt.Errorf("no target called %s in notify.toml", name)
	}
	var tt notifyTerm
	if d.Term != nil {
		if got, done, err := d.Term(); err == nil {
			defer done()
			tt = got
		}
	}
	if !yes {
		if tt == nil {
			return fmt.Errorf("remove asks first, on a terminal: run it on one, or pass --yes")
		}
		if !confirm(tt, "remove "+name+"? its topic is lost, y/n") {
			return fmt.Errorf("%s kept", name)
		}
	}
	if err := notify.Edit(path, func(doc *notify.Doc) error { return doc.Remove(name) }); err != nil {
		return fmt.Errorf("notify.toml was not changed: %v", err)
	}
	fmt.Fprintf(out, "%s: removed from notify.toml\n", name)
	cpath := config.Path(d.Getenv)
	listed := config.SplitList(config.Machine(d.Getenv).String("notify.targets"))
	if slices.Contains(listed, name) {
		left := slices.DeleteFunc(slices.Clone(listed), func(n string) bool { return n == name })
		if err := config.Set(cpath, "notify.targets", left); err != nil {
			return fmt.Errorf("config.toml was not changed: %v", err)
		}
		// notify.on changes only on the person's yes (§9), never on --yes.
		if len(left) == 0 && !yes && tt != nil && confirm(tt, "turn notifications off too? y/n") {
			if err := config.Set(cpath, "notify.on", false); err != nil {
				return fmt.Errorf("config.toml was not changed: %v", err)
			}
			fmt.Fprintln(out, "notifications: off")
		}
	}
	return nil
}

func notifyRename(args []string, d notificationsDeps, out io.Writer) error {
	if len(args) != 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return errUsage
	}
	old, name := args[0], args[1]
	path := notify.Path(d.Getenv)
	if f := notify.Load(path); f.Refused != "" {
		return fmt.Errorf("notify.toml refused: %s", f.Refused)
	}
	if err := notify.Edit(path, func(doc *notify.Doc) error { return doc.Rename(old, name) }); err != nil {
		return fmt.Errorf("notify.toml was not changed: %v", err)
	}
	fmt.Fprintf(out, "%s: renamed %s\n", old, name)
	if err := listTarget(d.Getenv, name, old); err != nil {
		fmt.Fprintln(out, "the target is renamed, but config.toml was not changed:", err)
	}
	if state := notifyStateDir(d.Getenv); state != "" {
		if err := notify.UpdateLast(notify.LastPath(state), func(l *notify.Last) error {
			l.Rename(old, name)
			return nil
		}); err != nil {
			fmt.Fprintln(out, "its last test was not moved:", err)
		}
	}
	return nil
}

func notifyShowTopic(args []string, d notificationsDeps) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errUsage
	}
	f := notify.Load(notify.Path(d.Getenv))
	if f.Refused != "" {
		return fmt.Errorf("notify.toml refused: %s", f.Refused)
	}
	t, ok := f.Target(args[0])
	switch {
	case !ok:
		return fmt.Errorf("no valid target %s in notify.toml", args[0])
	case t.Kind != "ntfy":
		return fmt.Errorf("%s is a %s target: it has no topic", t.Name, t.Kind)
	}
	if d.Term == nil {
		return fmt.Errorf("show-topic shows the topic on a terminal only: run it on one")
	}
	tt, done, err := d.Term()
	if err != nil {
		return fmt.Errorf("show-topic shows the topic on a terminal only: run it on one")
	}
	defer done()
	showTopic(tt, t)
	return nil
}

// notifySetup asks §3's questions on the terminal, then writes and tests as
// add does.
func notifySetup(ctx context.Context, args []string, d notificationsDeps, out io.Writer) (int, error) {
	test := true
	for _, a := range args {
		if a != "--no-test" {
			return 0, errUsage
		}
		test = false
	}
	if !d.StdinTerminal || d.Term == nil {
		return 0, fmt.Errorf("setup asks its questions on a terminal: use agentbus notifications add … instead")
	}
	tt, done, err := d.Term()
	if err != nil {
		return 0, fmt.Errorf("setup asks its questions on a terminal: use agentbus notifications add … instead")
	}
	defer done()
	ask := func(q, def string) (string, error) {
		fmt.Fprintf(tt, "%s [%s]: ", q, def)
		a, err := tt.ReadLine()
		if err != nil {
			return "", fmt.Errorf("setup stopped: nothing written")
		}
		if a = strings.TrimSpace(a); a == "" {
			a = def
		}
		return a, nil
	}
	kind, err := ask("What should ring? 1 a phone (ntfy)  2 Discord  3 this desktop", "1")
	if err != nil {
		return 0, err
	}
	t := notify.Target{}
	switch kind {
	case "1":
		t.Kind, t.Server = "ntfy", notify.DefaultServer
		where, err := ask("Which server? 1 ntfy.sh (free, public)  2 my own server", "1")
		if err != nil {
			return 0, err
		}
		if where == "2" {
			addr, err := ask("its address (https://…, or http:// on your own network)", "")
			if err != nil {
				return 0, err
			}
			t.Server = strings.TrimRight(addr, "/")
			fmt.Fprint(tt, "an access token (hidden; Enter for none): ")
			if t.Token, err = tt.ReadSecret(); err != nil {
				return 0, fmt.Errorf("setup stopped: nothing written")
			}
			yours, err := ask("Is this server yours? Then full text may be sent to it (y/n)", "n")
			if err != nil {
				return 0, err
			}
			t.SelfHosted = strings.EqualFold(yours, "y") || strings.EqualFold(yours, "yes")
		}
	case "2":
		t.Kind = "discord"
		fmt.Fprintln(tt, "Make a webhook: Server settings → Integrations → Webhooks → New webhook → Copy webhook URL")
		fmt.Fprint(tt, "paste it (hidden): ")
		if t.Webhook, err = tt.ReadSecret(); err != nil {
			return 0, fmt.Errorf("setup stopped: nothing written")
		}
		fmt.Fprintln(tt, "the webhook: https://discord.com/api/webhooks/…")
	case "3":
		t.Kind = "desktop"
		if d.Desktop == nil {
			return 0, fmt.Errorf("install libnotify (notify-send) first")
		}
	default:
		return 0, fmt.Errorf("answer 1, 2 or 3: nothing written")
	}
	f := notify.Load(notify.Path(d.Getenv))
	name, err := ask("its name", freeName(f, defaultNames[t.Kind]))
	if err != nil {
		return 0, err
	}
	return addTarget(ctx, t, name, test, d, out, tt)
}
