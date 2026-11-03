package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/notify"
)

// agentbus notifications status|test (spec
// docs/superpowers/specs/2026-11-03-notifications-design.md, "Commands"): no
// project, no broker. status shows each target of notify.toml, whether it is
// listed and its last send; test sends one message now and records its
// outcome. Neither ever prints a URL, a topic, a token or a webhook. test is
// the person's act: every role's tool rules deny it (it reaches a phone).

type notificationsDeps struct {
	Getenv   func(string) string
	Now      func() time.Time
	Hostname func() (string, error)
	Desktop  notify.DesktopRun // nil = no notify-send on PATH
}

func realNotificationsDeps() notificationsDeps {
	return notificationsDeps{Getenv: os.Getenv, Now: time.Now, Hostname: os.Hostname,
		Desktop: desktopRun(exec.LookPath, runQuiet)}
}

// desktopRun is notify-send with an urgency, bounded to 5 s; nil when it is
// not on PATH.
func desktopRun(lookPath func(string) (string, error), run func(ctx context.Context, bin string, args ...string) error) notify.DesktopRun {
	bin, err := lookPath("notify-send")
	if err != nil {
		return nil
	}
	return func(ctx context.Context, title, body, urgency string) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return run(ctx, bin, "-a", "agentbus", "-u", urgency, title, body)
	}
}

const notificationsUsage = "usage: agentbus notifications status | test [<target>]"

func runNotifications(ctx context.Context, args []string, d notificationsDeps, out, errw io.Writer) int {
	switch {
	case len(args) == 1 && args[0] == "status":
		return notificationsStatus(d, out, errw)
	case len(args) >= 1 && args[0] == "test" && len(args) <= 2:
		target := ""
		if len(args) == 2 {
			target = args[1]
		}
		return notificationsTest(ctx, target, d, out, errw)
	}
	fmt.Fprintln(errw, notificationsUsage)
	return 2
}

// notifyStateDir is <state> = $XDG_STATE_HOME/agentbus, else
// ~/.local/state/agentbus, where notify-last.json lives.
func notifyStateDir(getenv func(string) string) string {
	if d := getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "agentbus")
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".local", "state", "agentbus")
	}
	return ""
}

func notificationsTest(ctx context.Context, name string, d notificationsDeps, out, errw io.Writer) int {
	f := notify.Load(notify.Path(d.Getenv))
	if f.Refused != "" {
		fmt.Fprintln(errw, "agentbus notifications: notify.toml refused:", f.Refused)
		return 1
	}
	var targets []notify.Target
	if name != "" {
		t, ok := f.Target(name)
		if !ok {
			fmt.Fprintf(errw, "agentbus notifications: no valid target %s in notify.toml\n", name)
			return 1
		}
		targets = []notify.Target{t}
	} else {
		listed := config.SplitList(config.Machine(d.Getenv).String("notify.targets"))
		if len(listed) == 0 {
			fmt.Fprintln(errw, "agentbus notifications: no target in notify.targets: name one (agentbus notifications test <target>)")
			return 1
		}
		for _, n := range listed {
			t, ok := f.Target(n)
			if !ok {
				fmt.Fprintf(out, "%s: not a valid target of notify.toml\n", n)
				continue
			}
			targets = append(targets, t)
		}
	}
	host, err := d.Hostname()
	if err != nil || host == "" {
		host = "this host"
	}
	msg := notify.Message{Title: "agentbus: a test message from " + host, Priority: notify.Default}
	state := notifyStateDir(d.Getenv)
	code := 0
	for _, t := range targets {
		msg.Lines = []string{"If you read this, the target " + t.Name + " works."}
		err := t.Sender(d.Desktop).Send(ctx, msg)
		o := notify.Outcome{At: d.Now(), OK: err == nil, Test: true}
		if err != nil {
			o.Err = err.Error()
			fmt.Fprintln(out, err.Error())
			code = 1
		} else {
			fmt.Fprintln(out, t.Name+": ok")
		}
		if state != "" {
			if err := notify.UpdateLast(notify.LastPath(state), func(l *notify.Last) error {
				l.Add(t.Name, o, false)
				return nil
			}); err != nil {
				fmt.Fprintln(errw, "agentbus notifications: the outcome was not recorded:", err)
			}
		}
	}
	if len(targets) == 0 {
		return 1
	}
	return code
}

func notificationsStatus(d notificationsDeps, out, errw io.Writer) int {
	r := config.Machine(d.Getenv)
	on := "off"
	if r.Bool("notify.on") {
		on = "on"
	}
	fmt.Fprintln(out, "notifications:", on)
	f := notify.Load(notify.Path(d.Getenv))
	switch {
	case !f.Exists:
		fmt.Fprintln(out, "notify.toml: none (copy a table from the guide; agentbus never writes it)")
	case f.Refused != "":
		fmt.Fprintln(out, "notify.toml: refused:", f.Refused)
	default:
		fmt.Fprintln(out, "notify.toml: ok")
	}
	listed := map[string]bool{}
	for _, n := range config.SplitList(r.String("notify.targets")) {
		listed[n] = true
	}
	var last notify.Last
	if state := notifyStateDir(d.Getenv); state != "" {
		var err error
		if last, err = notify.ReadLast(notify.LastPath(state)); err != nil {
			fmt.Fprintln(errw, "agentbus notifications:", err)
		}
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, t := range f.Targets {
		where := t.Kind
		switch {
		case t.Kind == "ntfy" && t.SelfHosted:
			where += ", self-hosted"
		case t.Kind == "ntfy":
			where += ", public: summary only"
		case t.Kind == "discord":
			where += ": summary only"
		}
		state := "not listed in notify.targets"
		if listed[t.Name] {
			state = "listed"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", t.Name, where, state, lastText(last, t.Name))
		delete(listed, t.Name)
	}
	for _, n := range config.SplitList(r.String("notify.targets")) {
		if listed[n] {
			fmt.Fprintf(tw, "  %s\t\tlisted in notify.targets but not in notify.toml\t\n", n)
		}
	}
	tw.Flush()
	for _, w := range f.Warnings {
		fmt.Fprintln(out, "warning:", w.String())
	}
	projects := make([]string, 0, len(last.Sources))
	for p := range last.Sources {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	for _, p := range projects {
		fmt.Fprintf(out, "project %s: cannot read %s\n", p, strings.Join(last.FailingSources(p), ", "))
	}
	return 0
}

func lastText(l notify.Last, target string) string {
	o, ok := l.Latest(target)
	if !ok {
		return "never sent"
	}
	what := "ok"
	if !o.OK {
		what = o.Err
	}
	if o.Test {
		what += " (test)"
	}
	return "last sent " + o.At.Local().Format("2006-01-02 15:04") + " " + what
}
