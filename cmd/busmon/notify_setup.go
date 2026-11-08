package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
	"github.com/netbja/agent-bus-monitor/notify"
)

// The guided setup (notify-guided-setup §5): page C → Notifications → Set
// up …, or a target line, opens ovNotifySetup. busmon writes notify.toml
// through notify.Edit, on the person's own keys only: an agent (by its
// environment or its ancestry) gets a page that says so and nothing else.
// Nothing is written before the write step; a secret is held in the page
// only, never drawn (a masked field shows its count), never copied, gone
// when the page closes.

// nsField is one field of a step.
type nsField struct {
	label  string
	val    []rune
	saved  []rune // the value when the edit began: Esc puts it back
	masked bool
}

// notifySetup is the page's state.
type notifySetup struct {
	kind       string // ntfy, discord, desktop
	step       int    // 1..4
	own        bool   // ntfy: my own server (step 1's choice)
	fields     map[string]*nsField
	focus      int  // the focused field of the step (-1 none)
	editing    bool // the focused field takes every key
	topic      string
	target     string // a target's page: its name ("" = a new target)
	written    string // the name this page wrote (its edit replaces it)
	notifySend bool
	refused    string // the agent refusal: the page shows only this
	busy       bool   // a write or a test is in flight
	result     string // the test's outcome, in plain words (§7)
	resultOK   bool
	removeAsk  bool // x waits for y or n (modal)
	offAsk     bool // the last listed target removed: "turn notifications off too?" waits for y or n (modal)
	removed    bool
	showCode   bool // c, s: the code alone until any key
	showTopic  bool // s: the topic and its code on a target's page
	err        string
	last       string // a target's page: its last test
}

// nsStepFields are the fields of a step, in Tab order.
func (s *notifySetup) stepFields() []string {
	if s.target != "" && s.step == 0 { // a target's page: the rename field only
		return []string{"rename"}
	}
	switch {
	case s.kind == "ntfy" && s.step == 2:
		return []string{"server", "token", "yours"}
	case s.kind == "ntfy" && s.step == 4, s.kind != "ntfy" && s.step == 2:
		return []string{"name"}
	case s.kind == "discord" && s.step == 1:
		return []string{"webhook"}
	}
	return nil
}

func (s *notifySetup) field(k string) *nsField {
	if f, ok := s.fields[k]; ok {
		return f
	}
	f := &nsField{}
	s.fields[k] = f
	return f
}

func (s *notifySetup) value(k string) string { return strings.TrimSpace(string(s.field(k).val)) }

// newNotifySetup is a setup of kind, its name the kind's usual one (free in
// f), or, for a target's page, that target's values (target != "").
func newNotifySetup(kind string, f *notify.File, t *notify.Target, notifySend bool) *notifySetup {
	s := &notifySetup{kind: kind, step: 1, focus: -1, fields: map[string]*nsField{}, notifySend: notifySend}
	labels := map[string]string{"server": "address", "token": "access token", "yours": "is this server yours? (y/n)",
		"name": "its name", "webhook": "the webhook", "rename": "new name"}
	for k, l := range labels {
		s.fields[k] = &nsField{label: l, masked: k == "token" || k == "webhook"}
	}
	s.field("yours").val = []rune("n")
	base := map[string]string{"ntfy": "phone", "discord": "team", "desktop": "here"}[kind]
	s.field("name").val = []rune(notifyAssistName(f, base))
	if t != nil {
		s.target, s.step, s.topic = t.Name, 0, t.Topic
		s.field("name").val = []rune(t.Name)
		s.field("rename").val = []rune(t.Name)
		if t.Server != "" && t.Server != notify.DefaultServer {
			s.own = true
			s.field("server").val = []rune(t.Server)
		}
		s.field("token").val = []rune(t.Token)
		if t.SelfHosted {
			s.field("yours").val = []rune("y")
		}
		s.field("webhook").val = []rune(t.Webhook)
	} else if kind == "ntfy" {
		s.topic = drawTopic()
	}
	return s
}

// nsTarget is the target the fields describe.
func (s *notifySetup) nsTarget() notify.Target {
	t := notify.Target{Kind: s.kind, Name: s.value("name"), Topic: s.topic}
	switch s.kind {
	case "ntfy":
		t.Server = notify.DefaultServer
		if s.own {
			t.Server = strings.TrimRight(s.value("server"), "/")
			t.Token = s.value("token")
			y := strings.ToLower(s.value("yours"))
			t.SelfHosted = y == "y" || y == "yes"
		}
	case "discord":
		t.Webhook = s.value("webhook")
	}
	return t
}

// notifySetupMsg opens the page once its facts are read.
type notifySetupMsg struct {
	kind, target string
	file         *notify.File
	last         notifyAssistFacts
	refused      string
}

// notifySetupDone is a write's, a test's or a remove's outcome.
type notifySetupDone struct {
	serial          int
	name            string
	writeErr        string
	tested          bool
	testOK          bool
	explained       string
	removed, rename bool
	lastListed      bool // the remove left no target listed (§3: ask whether to turn notifications off)
}

// nsAgentCaller names the registered agent busmon runs under ("" none): a
// test replaces it.
var nsAgentCaller = agentcaller.Ancestor

// nsSend sends the test message to t (a test replaces it).
var nsSend = func(ctx context.Context, t notify.Target) error {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "this host"
	}
	msg := notify.Message{Title: "agentbus: a test message from " + host, Priority: notify.Default,
		Lines: []string{"If you read this, the target " + t.Name + " works."}}
	return t.Sender(nsDesktop()).Send(ctx, msg)
}

// nsDesktop is notify-send, nil when it is not on PATH.
func nsDesktop() notify.DesktopRun {
	bin, err := lookNotifySend("notify-send")
	if err != nil {
		return nil
	}
	return func(ctx context.Context, title, body, urgency string) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, bin, "-a", "agentbus", "-u", urgency, title, body).Run()
	}
}

// notifySetupRefusal is §2's rule for busmon: its environment or its
// ancestry names an agent, or the ancestry cannot be checked.
func notifySetupRefusal(getenv func(string) string) string {
	const why = "setting up a target is the person's act: open busmon from your own shell"
	if getenv("AGENT_BUS_AGENT") != "" {
		return why
	}
	caller, err := nsAgentCaller(context.Background())
	switch {
	case errors.Is(err, agentcaller.ErrNoAncestry):
	case err != nil:
		return "the agents could not be read, so busmon cannot tell it runs under none: " + why
	case caller != "":
		return why
	}
	return ""
}

// notifySetupOpen reads the facts in a command; its message opens the page.
func (m *Model) notifySetupOpen(kind, target string) tea.Cmd {
	getenv := m.settingsGetenv()
	return func() tea.Msg {
		if r := notifySetupRefusal(getenv); r != "" {
			return notifySetupMsg{kind: kind, target: target, refused: r}
		}
		return notifySetupMsg{kind: kind, target: target, file: notify.Load(notify.Path(getenv)), last: readNotifyAssist(getenv, "")}
	}
}

// openNotifySetup pushes the page for msg.
func (m *Model) openNotifySetup(msg notifySetupMsg) tea.Cmd {
	_, err := lookNotifySend("notify-send")
	var t *notify.Target
	if msg.target != "" && msg.file != nil {
		if got, ok := msg.file.Target(msg.target); ok {
			t = &got
		}
	}
	s := newNotifySetup(msg.kind, msg.file, t, err == nil)
	s.refused = msg.refused
	if t != nil {
		s.last = notifyLastTest(msg.last, t.Name)
	}
	if msg.file != nil && msg.file.Refused != "" {
		s.err = "notify.toml refused: " + msg.file.Refused + " (chmod 600 " + notify.Path(m.settingsGetenv()) + ")"
	}
	return m.push(page{kind: ovNotifySetup, ns: s})
}

// nsWrite writes t (replacing old, when set), turns notifications on and
// lists it, then tests it: one command, its outcome a notifySetupDone.
func nsWrite(getenv func(string) string, serial int, t notify.Target, old string) tea.Cmd {
	return func() tea.Msg {
		done := notifySetupDone{serial: serial, name: t.Name}
		path := notify.Path(getenv)
		err := notify.Edit(path, func(d *notify.Doc) error {
			if old != "" {
				if err := d.Remove(old); err != nil {
					return err
				}
			}
			return d.Add(t)
		})
		if err != nil {
			done.writeErr = "notify.toml was not changed: " + err.Error()
			return done
		}
		if err := nsList(getenv, t.Name, old); err != nil {
			done.writeErr = "the target is written, but config.toml was not changed: " + err.Error()
		}
		done.tested = true
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sendErr := nsSend(ctx, t)
		done.testOK = sendErr == nil
		if sendErr != nil {
			done.explained = notify.Explain(t.Kind, sendErr)
		}
		nsRecord(getenv, t.Name, sendErr)
		return done
	}
}

// nsTest sends the test message again.
func nsTest(getenv func(string) string, serial int, t notify.Target) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := nsSend(ctx, t)
		nsRecord(getenv, t.Name, err)
		done := notifySetupDone{serial: serial, name: t.Name, tested: true, testOK: err == nil}
		if err != nil {
			done.explained = notify.Explain(t.Kind, err)
		}
		return done
	}
}

// nsRecord records a test in notify-last.json, as the CLI's test does.
func nsRecord(getenv func(string) string, name string, err error) {
	d := notifyStateDir(getenv)
	if d == "" {
		return
	}
	o := notify.Outcome{At: time.Now(), OK: err == nil, Test: true}
	if err != nil {
		o.Err = err.Error()
	}
	_ = notify.UpdateLast(notify.LastPath(d), func(l *notify.Last) error { l.Add(name, o, false); return nil })
}

// nsList lists name in notify.targets once (renaming old) and turns
// notifications on (the person's answer 5); a rename alone never turns
// them on.
func nsList(getenv func(string) string, name, old string) error {
	path := config.Path(getenv)
	r := config.Resolver{Getenv: getenv, Files: []*config.File{config.Load(path, "config.toml")}}
	listed := config.SplitList(r.String("notify.targets"))
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
		out = append(out, name)
	}
	if err := config.Set(path, "notify.targets", out); err != nil {
		return err
	}
	return config.Set(path, "notify.on", true)
}

// nsRemove removes the target's table and its listing (§3 x).
func nsRemove(getenv func(string) string, serial int, name string) tea.Cmd {
	return func() tea.Msg {
		done := notifySetupDone{serial: serial, name: name, removed: true}
		if err := notify.Edit(notify.Path(getenv), func(d *notify.Doc) error { return d.Remove(name) }); err != nil {
			done.writeErr = "notify.toml was not changed: " + err.Error()
			return done
		}
		path := config.Path(getenv)
		r := config.Resolver{Getenv: getenv, Files: []*config.File{config.Load(path, "config.toml")}}
		listed := config.SplitList(r.String("notify.targets"))
		left := slices.DeleteFunc(slices.Clone(listed), func(n string) bool { return n == name })
		if len(left) != len(listed) {
			if err := config.Set(path, "notify.targets", left); err != nil {
				done.writeErr = "config.toml was not changed: " + err.Error()
				return done
			}
		}
		// No target listed after the remove, whether or not it was listed
		// (reviewer-codex), while notifications are on: ask (§3).
		done.lastListed = len(left) == 0 && r.Bool("notify.on")
		return done
	}
}

// nsRename renames the target: its table, its listing, its record.
func nsRename(getenv func(string) string, serial int, old, name string) tea.Cmd {
	return func() tea.Msg {
		done := notifySetupDone{serial: serial, name: name, rename: true}
		if err := notify.Edit(notify.Path(getenv), func(d *notify.Doc) error { return d.Rename(old, name) }); err != nil {
			done.writeErr = "notify.toml was not changed: " + err.Error()
			return done
		}
		path := config.Path(getenv)
		r := config.Resolver{Getenv: getenv, Files: []*config.File{config.Load(path, "config.toml")}}
		listed := config.SplitList(r.String("notify.targets"))
		if slices.Contains(listed, old) {
			for i, n := range listed {
				if n == old {
					listed[i] = name
				}
			}
			if err := config.Set(path, "notify.targets", listed); err != nil {
				done.writeErr = "config.toml was not changed: " + err.Error()
			}
		}
		if d := notifyStateDir(getenv); d != "" {
			_ = notify.UpdateLast(notify.LastPath(d), func(l *notify.Last) error { l.Rename(old, name); return nil })
		}
		return done
	}
}

// notifySetupResult applies a command's outcome to its page.
func (m *Model) notifySetupResult(msg notifySetupDone) {
	for i := range m.rd.pages {
		p := &m.rd.pages[i]
		if p.serial != msg.serial || p.ns == nil {
			continue
		}
		s := p.ns
		s.busy, p.dirty = false, true
		switch {
		case msg.writeErr != "":
			s.err = msg.writeErr
		case msg.removed:
			s.removed, s.offAsk = true, msg.lastListed
		case msg.rename:
			s.target, s.written = msg.name, msg.name
			s.field("name").val = []rune(msg.name)
		default:
			s.written, s.err = msg.name, ""
		}
		if msg.tested {
			s.resultOK = msg.testOK
			s.result = msg.explained
			if msg.testOK {
				s.result = "delivered: check your phone"
				if s.kind == "desktop" {
					s.result = "delivered: check your screen"
				} else if s.kind == "discord" {
					s.result = "delivered to Discord: check the channel"
				}
			}
		}
		m.settingsReload()
	}
}

// settingsReload marks page C (under the setup) to read its rows again.
func (m *Model) settingsReload() {
	for i := range m.rd.pages {
		if m.rd.pages[i].kind == ovSettings && m.rd.pages[i].set != nil {
			m.rd.pages[i].set.loaded = false
		}
	}
}

// notifySetupKey is a key on the page (ModeNotifySetup).
func (m *Model) notifySetupKey(act Action, k Key) tea.Cmd {
	p := m.rd.top()
	s := p.ns
	p.dirty = true
	defer m.refresh()
	if s.offAsk { // modal: only the person's y turns notifications off (§9); any other key keeps them on
		s.offAsk = false
		if k.Code == keyRune && k.Rune == 'y' {
			path := config.Path(m.settingsGetenv())
			return m.settingsCmd(func() error { return config.Set(path, "notify.on", false) }, "notify.on")
		}
		return nil
	}
	if s.refused != "" || s.removed {
		if act == ActClose || k.Code == keyEnter {
			m.pop()
		}
		return nil
	}
	if s.showCode || s.showTopic { // any key ends it
		s.showCode, s.showTopic = false, false
		return nil
	}
	if s.removeAsk { // modal: y removes, any other key keeps it
		s.removeAsk = false
		if k.Code == keyRune && k.Rune == 'y' {
			s.busy = true
			return nsRemove(m.settingsGetenv(), p.serial, s.target)
		}
		m.ui.flash, m.ui.flashErr = s.target+" kept", false
		return nil
	}
	if s.busy {
		return nil
	}
	if s.editing {
		return m.notifyFieldKey(s, k)
	}
	fields := s.stepFields()
	switch act {
	case ActClose:
		return m.notifySetupBack(s)
	case ActUp, ActDown:
		if s.kind == "ntfy" && s.step == 1 {
			s.own = !s.own
		} else if len(fields) > 0 {
			d := 1
			if act == ActUp {
				d = -1
			}
			s.focus = clamp(s.focus+d, 0, len(fields)-1)
		}
	case ActFieldNext, ActFieldPrev:
		if len(fields) == 0 {
			return nil
		}
		d := 1
		if act == ActFieldPrev {
			d = -1
		}
		s.focus = (s.focus + d + len(fields)) % len(fields)
		s.startEdit()
	case ActOpen:
		if s.focus >= 0 && s.focus < len(fields) && !(s.target != "" && s.step == 0) {
			s.startEdit()
			return nil
		}
		return m.notifySetupNext(p, s)
	case ActCopy:
		if s.kind == "ntfy" && s.topic != "" && (s.step == 3 || s.target != "") {
			return m.copy(s.topic, "topic")
		}
	case ActCopyThread:
		if s.kind == "ntfy" && s.topic != "" && (s.step == 3 || s.target != "") {
			return m.copy(notify.SubscribeLink(s.nsTarget()), "subscribe link")
		}
	case ActShowCode:
		if s.kind == "ntfy" && (s.step == 3 || s.target != "") {
			s.showCode = true
		}
	case ActTestAgain:
		if s.written != "" || s.target != "" {
			s.busy = true
			t := s.nsTarget()
			return nsTest(m.settingsGetenv(), p.serial, t)
		}
	case ActEditSetup:
		if s.written != "" || s.target != "" {
			if s.target != "" && s.written == "" {
				s.written = s.target
			}
			s.target, s.step, s.focus, s.result, s.err = "", 1, -1, "", ""
		}
	case ActRemoveTarget:
		if s.target != "" && s.step == 0 {
			s.removeAsk = true
		}
	case ActRenameTarget:
		if s.target != "" && s.step == 0 {
			s.focus = 0
			s.startEdit()
		}
	case ActShowTopic:
		if s.target != "" && s.kind == "ntfy" {
			s.showTopic = true
		}
	case ActShowFormat: // the read-only template, for a person who writes the file by hand (§5.1)
		if s.target == "" && s.step == 1 {
			return m.notifyAssistOpen(s.kind)
		}
	}
	return nil
}

func (s *notifySetup) startEdit() {
	fields := s.stepFields()
	if s.focus < 0 || s.focus >= len(fields) {
		return
	}
	f := s.field(fields[s.focus])
	f.saved = slices.Clone(f.val)
	s.editing = true
}

// notifyFieldKey: a field takes every key while it is edited (§5.2); Tab,
// Enter and Esc leave it.
func (m *Model) notifyFieldKey(s *notifySetup, k Key) tea.Cmd {
	fields := s.stepFields()
	f := s.field(fields[s.focus])
	switch k.Code {
	case keyTab, keyBacktab:
		s.editing = false
		d := 1
		if k.Code == keyBacktab {
			d = -1
		}
		s.focus = (s.focus + d + len(fields)) % len(fields)
		s.startEdit()
	case keyEnter: // keeps the value; the selection goes, so the next enter is the step's
		s.editing = false
		s.focus = -1
		if s.target != "" && s.step == 0 { // the rename field: Enter renames
			if name := s.value("rename"); name != s.target {
				s.busy = true
				return nsRename(m.settingsGetenv(), m.rd.top().serial, s.target, name)
			}
		}
	case keyEscape:
		f.val, s.editing = f.saved, false
	case keyBackspace:
		if len(f.val) > 0 {
			f.val = f.val[:len(f.val)-1]
		}
	case keyRune:
		f.val = append(f.val, k.Rune)
	}
	return nil
}

// notifyPaste takes a paste into the field being edited, whole; a newline
// ends the field (§5.2).
func (m *Model) notifyPaste(text string) {
	p := m.rd.top()
	if p == nil || p.ns == nil {
		return
	}
	s := p.ns
	if !s.editing && !s.busy && s.refused == "" && len(s.stepFields()) == 1 && !(s.target != "" && s.step == 0) {
		s.focus = 0 // a step with one field takes a paste into it (§3: paste the webhook)
		s.startEdit()
	}
	if !s.editing {
		return
	}
	line, rest, nl := strings.Cut(text, "\n")
	f := s.field(s.stepFields()[s.focus])
	f.val = append(f.val, []rune(strings.TrimRight(line, "\r"))...)
	if nl || rest != "" { // a pasted newline is enter: the value kept, the selection gone
		s.editing, s.focus = false, -1
	}
	p.dirty = true
}

// notifySetupBack is Esc: one step back; on step 1 or a target's page,
// close (nothing written before the write step).
func (m *Model) notifySetupBack(s *notifySetup) tea.Cmd {
	switch {
	case s.target != "" || s.step <= 1 || s.written != "" && s.result != "":
		m.pop()
	case s.kind == "ntfy" && s.step == 3 && !s.own:
		s.step = 1
	default:
		s.step--
	}
	s.focus, s.err = -1, ""
	return nil
}

// notifySetupNext is Enter on a step: check it, then the next one, or the
// write.
func (m *Model) notifySetupNext(p *page, s *notifySetup) tea.Cmd {
	s.err = ""
	s.focus = -1
	switch {
	case s.target != "":
		return nil
	case s.kind == "ntfy" && s.step == 1:
		s.step = 2
		if !s.own {
			s.step = 3
		}
	case s.kind == "ntfy" && s.step == 2:
		if why := nsServerCheck(s.value("server")); why != "" {
			s.err = why
			return nil
		}
		s.step = 3
	case s.kind == "ntfy" && s.step == 3:
		s.step = 4
	case s.kind == "discord" && s.step == 1:
		if s.value("webhook") == "" {
			s.err = "paste the webhook first"
			return nil
		}
		s.step = 2
	case s.kind == "desktop" && s.step == 1:
		if !s.notifySend {
			s.err = "install libnotify (notify-send) first"
			return nil
		}
		s.step = 2
	case s.kind != "ntfy" && s.step == 2:
		s.step = 3
	case s.kind == "ntfy" && s.step == 4, s.kind != "ntfy" && s.step == 3:
		if s.result != "" { // written and tested: Enter closes
			m.pop()
			return nil
		}
		s.busy = true
		if s.kind != "ntfy" {
			s.step = 4
		}
		return nsWrite(m.settingsGetenv(), p.serial, s.nsTarget(), s.written)
	case s.step == 4:
		m.pop()
	}
	return nil
}

// nsServerCheck refuses an address before anything is written; the writer
// judges it again by Load's rules.
func nsServerCheck(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if raw == "" || err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "the address: https://… (or http:// on your own network)"
	}
	return ""
}

// notifySetupTitle is the frame's title: the kind and the step.
func notifySetupTitle(s *notifySetup) string {
	label := map[string]string{"ntfy": "Set up ntfy", "discord": "Set up Discord", "desktop": "Set up the desktop"}[s.kind]
	switch {
	case s.refused != "":
		return label
	case s.target != "":
		return "Target " + s.target + " (" + s.kind + ")"
	}
	return fmt.Sprintf("%s %s step %d/4", label, GlyphNone, s.step)
}

// notifySetupBody is the page's body for the reader's width and rows, at
// tier (the code's place, §5.3).
func notifySetupBody(s *notifySetup, width, rows int, tier Tier, screenW, screenH int) []Line {
	var b body
	text := func(role Role, str string) {
		for _, l := range wrapSpans([]mdSpan{{text: str, role: role}}, width-2, " ", " ") {
			b.join([]Line{l}).text("\n")
		}
	}
	blank := func() { b.text("\n") }
	if s.refused != "" {
		blank()
		text(RoleAttention, s.refused)
		return b.Lines()
	}
	if s.showCode || s.showTopic {
		link := notify.SubscribeLink(s.nsTarget())
		if s.showTopic {
			blank()
			text(RoleText, "the topic: "+s.topic)
			text(RoleAccent, link)
		}
		qr, err := notify.QR(link)
		if err != nil || len([]rune(qr[0]))+2 > screenW || len(qr)+2 > screenH {
			blank()
			text(RoleMuted, "widen the window to show the code")
			return b.Lines()
		}
		blank()
		for _, r := range qr {
			b.segs(Seg{Text: " "}, Seg{Text: r, QR: true}).text("\n")
		}
		text(RoleMuted, "any key goes back")
		return b.Lines()
	}
	if s.removed {
		blank()
		text(RoleOK, s.target+" removed from notify.toml and Targets.")
		if s.offAsk {
			blank()
			text(RoleAttention, "no target is listed now: turn notifications off too? y/n")
			return b.Lines()
		}
		text(RoleMuted, "enter or esc closes")
		return b.Lines()
	}
	blank()
	fields := s.stepFields()
	drawField := func(i int, k string) {
		f := s.field(k)
		v := string(f.val)
		if f.masked && v != "" {
			v = fmt.Sprintf("%s  (%d characters)", strings.Repeat("•", 4), len(f.val))
		}
		mark := "   "
		if i == s.focus {
			mark = " " + GlyphSelect + " "
		}
		cursor := ""
		if i == s.focus && s.editing {
			cursor = "▏"
		}
		b.segs(Seg{Text: mark, Role: RoleAccent}, Seg{Text: fmt.Sprintf("%-28s", f.label), Role: RoleMuted}, Seg{Text: v + cursor, Role: RoleText}).text("\n")
	}
	if s.target != "" {
		text(RoleText, "kind: "+s.kind)
		text(RoleText, s.last)
		blank()
		drawField(0, "rename")
		blank()
		keys := "t test again · e edit · x remove · n rename"
		if s.kind == "ntfy" {
			keys += " · s show the topic · y Y copy the topic, the link · c the code"
		}
		text(RoleMuted, keys)
		if s.removeAsk {
			blank()
			text(RoleAttention, "remove "+s.target+"? its topic is lost, y/n")
		}
		nsResult(s, text)
		if s.err != "" {
			text(RoleError, s.err)
		}
		return b.Lines()
	}
	switch {
	case s.kind == "ntfy" && s.step == 1:
		text(RoleText, "Which server?")
		for i, c := range []string{"ntfy.sh (free, public)", "my own server"} {
			mark := "   "
			if (i == 1) == s.own {
				mark = " " + GlyphSelect + " "
			}
			b.segs(Seg{Text: mark, Role: RoleAccent}, Seg{Text: c, Role: RoleText}).text("\n")
		}
	case s.kind == "ntfy" && s.step == 2:
		text(RoleText, "Your own server: its address, an optional token, and whether it is yours (then full text may be sent to it).")
	case s.kind == "ntfy" && s.step == 3:
		link := notify.SubscribeLink(s.nsTarget())
		text(RoleText, "Subscribe on your phone. The topic is drawn for you:")
		text(RoleAccent, "  "+s.topic+"   (y copies it)")
		text(RoleAccent, "  "+link+"   (Y copies it)")
		qr, err := notify.QR(link)
		if err == nil && tier != TierNarrow && rows >= 26 && len([]rune(qr[0]))+2 <= width {
			blank()
			for _, r := range qr {
				b.segs(Seg{Text: " "}, Seg{Text: r, QR: true}).text("\n")
			}
		} else {
			text(RoleMuted, "c shows the code alone")
		}
		blank()
		text(RoleText, "In the ntfy app: + → subscribe to topic → paste the topic, or scan the code with the phone's camera. Then press enter.")
		text(RoleMuted, "the topic is a password: clear the screen when you are done")
	case s.kind == "discord" && s.step == 1:
		text(RoleText, "Make a webhook: Server settings → Integrations → Webhooks → New webhook → Copy webhook URL. Then tab into the field and paste it (it stays hidden):")
	case s.kind == "desktop" && s.step == 1:
		if s.notifySend {
			text(RoleOK, "notify-send is on PATH: this host shows the notices itself. Enter continues.")
		} else {
			text(RoleAttention, "install libnotify (notify-send) first")
		}
	case s.kind != "ntfy" && s.step == 3:
		text(RoleText, "Enter writes the target to notify.toml, turns notifications on, lists it and sends a test.")
	case s.kind == "ntfy" && s.step == 4 && s.result == "" && !s.busy:
		text(RoleText, "Its name, then enter: written to notify.toml, notifications on, listed, and a test sent.")
	}
	if len(fields) > 0 {
		blank()
		for i, k := range fields {
			drawField(i, k)
		}
	}
	if s.busy {
		blank()
		text(RoleMuted, "writing and testing"+GlyphEllipsis)
	}
	nsResult(s, text)
	if s.err != "" {
		blank()
		text(RoleError, s.err)
	}
	if s.step >= 3 {
		blank()
		text(RoleMuted, "messages say what waits, never an agent's words (notify.detail = summary)")
	}
	if s.step == 1 {
		blank()
		text(RoleMuted, "? shows notify.toml's format, to write it by hand")
	}
	return b.Lines()
}

// nsResult draws the test's outcome and the keys after it.
func nsResult(s *notifySetup, text func(Role, string)) {
	if s.result == "" {
		return
	}
	role := RoleError
	if s.resultOK {
		role = RoleOK
	}
	text(RoleText, "")
	text(role, s.result)
	if s.target == "" {
		text(RoleMuted, "t test again · e edit · enter closes")
	}
}
