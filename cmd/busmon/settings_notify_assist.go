package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/notify"
)

// The notifications assistant (notify-assistant): page C's Notifications
// group ends with three lines, one per kind of target; Enter on one opens its
// page: the steps, notify.toml's state (names and kinds only), a template to
// copy (y) and the kind's last test. busmon never writes notify.toml and
// never shows a saved topic, token or webhook: the only topic drawn is a new
// one, in the template, for the person to paste.

// notifyAssistKinds are the set-up lines, in the page's order.
var notifyAssistKinds = []struct{ kind, label, name string }{
	{"ntfy", "Set up ntfy (your phone)", "phone"},
	{"discord", "Set up Discord", "team"},
	{"desktop", "Set up the desktop (notify-send)", "here"},
}

// notifyAssistFacts is what a page shows, read in a command.
type notifyAssistFacts struct {
	Path       string
	File       *notify.File
	Last       notify.Last
	LastErr    bool // notify-last.json cannot be read: no test outcome is claimed
	NotifySend bool
	Topic      string // ntfy: drawn now, never saved by busmon
}

// notifyAssistMsg opens a kind's page once its facts are read.
type notifyAssistMsg struct {
	kind string
	f    notifyAssistFacts
}

// lookNotifySend finds notify-send on PATH (a test replaces it).
var lookNotifySend = exec.LookPath

// readNotifyAssist reads notify.toml (as the service does) and the record.
func readNotifyAssist(getenv func(string) string, kind string) notifyAssistFacts {
	f := notifyAssistFacts{Path: notify.Path(getenv), File: notify.Load(notify.Path(getenv))}
	if d := notifyStateDir(getenv); d == "" {
		f.LastErr = true
	} else if l, err := notify.ReadLast(notify.LastPath(d)); err != nil {
		f.LastErr = true
	} else {
		f.Last = l
	}
	switch kind {
	case "ntfy":
		f.Topic = drawTopic()
	case "desktop":
		_, err := lookNotifySend("notify-send")
		f.NotifySend = err == nil
	}
	return f
}

// drawTopic is a new ntfy topic, drawn as the CLI draws it (notify.DrawTopic).
func drawTopic() string { return notify.DrawTopic() }

// notifyAssistName is the template's name: the kind's usual one, numbered
// when notify.toml already holds a table of that name.
func notifyAssistName(f *notify.File, base string) string {
	taken := map[string]bool{}
	if f != nil {
		for _, t := range f.Targets {
			taken[t.Name] = true
		}
		for _, w := range f.Warnings { // a refused table still holds its name
			if _, rest, ok := strings.Cut(w.Key, "."); ok {
				name, _, _ := strings.Cut(rest, ".")
				taken[name] = true
			}
		}
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// notifyTemplate is the table to paste into notify.toml.
func notifyTemplate(kind, name, topic string) string {
	switch kind {
	case "ntfy":
		return "[ntfy." + name + "]\ntopic = \"" + topic + "\"\n"
	case "discord":
		return "[discord." + name + "]\nwebhook = \"<paste the webhook URL here>\"\n"
	}
	return "[desktop." + name + "]\n"
}

// notifyAssistPage is a kind's page.
func notifyAssistPage(kind string, f notifyAssistFacts) page {
	label, base := kind, kind
	for _, k := range notifyAssistKinds {
		if k.kind == kind {
			label, base = k.label, k.name
		}
	}
	name := notifyAssistName(f.File, base)
	tmpl := notifyTemplate(kind, name, f.Topic)
	var b body
	head := func(s string) { b.text("\n").segs(Seg{Text: " " + s, Role: RoleText, Bold: true}).text("\n") }
	line := func(role Role, s string) { b.seg(role, "   "+Preview(s)).text("\n") }

	head("notify.toml")
	path := f.Path
	if path == "" {
		path = "~/.config/agentbus/notify.toml (no HOME: unknown here)"
	}
	line(RoleText, path)
	switch {
	case f.File == nil || !f.File.Exists:
		line(RoleMuted, "does not exist yet: create it, mode 0600 in a folder only you can write:")
		dir := filepath.Dir(path)
		line(RoleAccent, "mkdir -p -m 700 "+shellWord(dir)+" && touch "+shellWord(path)+" && chmod 600 "+shellWord(path))
	case f.File.Refused != "":
		line(RoleAttention, "refused whole: "+f.File.Refused+" (nothing is sent until it is fixed)")
		line(RoleAccent, "chmod 600 "+shellWord(path))
	default:
		line(RoleMuted, fmt.Sprintf("%d targets, read as the service reads them:", len(f.File.Targets)))
		for _, t := range f.File.Targets { // names and kinds only: never a topic, a token or a webhook
			line(RoleText, fmt.Sprintf("  %-10s %s", t.Name, t.Kind))
		}
		for _, w := range f.File.Warnings {
			line(RoleAttention, "  "+w.String())
		}
	}

	head("Steps")
	steps := notifyAssistSteps(kind, name, f)
	for i, s := range steps {
		line(RoleText, fmt.Sprintf("%d. %s", i+1, s))
	}

	head("Template (y copies it)")
	for _, l := range strings.Split(strings.TrimSuffix(tmpl, "\n"), "\n") {
		line(RoleAccent, l)
	}
	if kind == "ntfy" {
		line(RoleMuted, "the topic is drawn now and kept nowhere: once saved, busmon never shows it again")
	}

	head("Last test")
	tested := false
	if f.File != nil {
		for _, t := range f.File.Targets {
			if t.Kind != kind {
				continue
			}
			tested = true
			line(RoleText, t.Name+": "+notifyLastTest(f, t.Name))
		}
	}
	if !tested {
		line(RoleMuted, "no "+kind+" target in notify.toml yet")
	}
	return page{kind: ovMessage, title: label, body: b.Lines(), plain: tmpl, plainWhat: "template", fixed: true}
}

// notifyAssistSteps are a kind's steps, the last two the same for all.
func notifyAssistSteps(kind, name string, f notifyAssistFacts) []string {
	var s []string
	switch kind {
	case "ntfy":
		s = []string{
			"Install the ntfy app on your phone and subscribe to the topic of the template below (server ntfy.sh, or your own: add server = \"https://...\").",
			"Paste the template into notify.toml with your editor (busmon never writes it).",
		}
	case "discord":
		s = []string{
			"In Discord: Server Settings, Integrations, Webhooks, New Webhook; choose the channel, then Copy Webhook URL.",
			"Paste the template into notify.toml with your editor and put the URL in place of the placeholder (busmon never writes it).",
		}
	default:
		nsend := "notify-send is on PATH"
		if !f.NotifySend {
			nsend = "notify-send is not on PATH: install libnotify (your system's package) first"
		}
		s = []string{
			"This host shows the notices itself: " + nsend + ".",
			"Paste the template into notify.toml with your editor (busmon never writes it).",
		}
	}
	return append(s,
		"Here, Notifications: turn Send notifications on and add "+name+" to Targets.",
		"In your own shell (agents are refused it): agentbus notifications test "+name+"; its outcome shows here and in the target lines.")
}

// notifyLastTest is a target's latest test, in the page's words.
func notifyLastTest(f notifyAssistFacts, name string) string {
	if f.LastErr {
		return "last test unknown (notify-last.json cannot be read)"
	}
	if r := f.Last.Targets[name]; r != nil {
		for i := len(r.Recent) - 1; i >= 0; i-- {
			if o := r.Recent[i]; o.Test {
				when := o.At.Local().Format("Jan 2 15:04")
				if o.OK {
					return "last test " + when + " ok"
				}
				return "last test " + when + " failed (" + o.Err + ")"
			}
		}
	}
	return "never tested"
}

// notifyAssistOpen reads a kind's facts in a command; its message opens the
// page.
func (m *Model) notifyAssistOpen(kind string) tea.Cmd {
	getenv := m.settingsGetenv()
	return func() tea.Msg { return notifyAssistMsg{kind: kind, f: readNotifyAssist(getenv, kind)} }
}

// notifyAssistLines draws the set-up lines after the target lines; sel is
// the page's selection, first the index of the first line. It returns the
// lines and the selected one's offset (-1 = none).
func notifyAssistLines(s *settingsPage, first, sel, width int) ([]Line, int) {
	out := []Line{{}}
	at := -1
	for i, k := range notifyAssistKinds {
		if first+i == sel {
			at = len(out)
		}
		out = append(out, FitLine(append(settingsItemMark(s, first+i, sel), S(RoleAccent, k.label)), width))
	}
	return out, at
}

// settingsItemMark is the selection mark of a line after a group's keys, as
// a key's: the accent while the keys have the focus.
func settingsItemMark(s *settingsPage, i, sel int) Line {
	switch {
	case i == sel && s.keys:
		return append(Line{S(RoleText, " ")}, planMark(true)...)
	case i == sel:
		return Line{S(RoleText, " "), S(RoleMuted, GlyphSelect), S(RoleText, " ")}
	}
	return append(Line{S(RoleText, " ")}, planMark(false)...)
}
