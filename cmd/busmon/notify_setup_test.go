package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
	"github.com/netbja/agent-bus-monitor/notify"
)

// notify-guided-setup §5: the guided setup in busmon. Every case runs in a
// temporary config home; nothing is sent (nsSend is replaced) and no broker
// is read (nsAgentCaller is replaced).

const goldenTopic = "agentbus-goldentopic0000000"

// setupWorld is a settings shell with page C open on Notifications and the
// sends recorded.
type setupWorld struct {
	m      Model
	getenv func(string) string
	sent   []notify.Target
	fail   error
}

func newSetupWorld(t *testing.T, notifyToml string) *setupWorld {
	t.Helper()
	w := &setupWorld{}
	w.getenv = settingsEnv(t, "", "", map[string]string{"XDG_STATE_HOME": t.TempDir(), "AGENT_BUS_NO_UPDATE_CHECK": "1"})
	if notifyToml != "" {
		if err := os.WriteFile(w.notifyPath(), []byte(notifyToml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stubAgentCaller(t, "", agentcaller.ErrNoAncestry) // off Linux: the env rule alone
	old := nsSend
	nsSend = func(_ context.Context, tg notify.Target) error { w.sent = append(w.sent, tg); return w.fail }
	t.Cleanup(func() { nsSend = old })
	oldLook := lookNotifySend
	lookNotifySend = func(string) (string, error) { return "/usr/bin/notify-send", nil }
	t.Cleanup(func() { lookNotifySend = oldLook })
	var themed string
	w.m = showGroup(t, press(t, settingsShell(t, w.getenv, &themed), 'C'), config.GroupNotifications)
	w.m.rd.top().set.keys = true
	return w
}

func (w *setupWorld) notifyPath() string {
	return filepath.Join(filepath.Dir(config.Path(w.getenv)), "notify.toml")
}

// run sends msg and runs every command it returns, as the program would.
func (w *setupWorld) run(t *testing.T, msg tea.Msg) {
	t.Helper()
	next, cmd := w.m.Update(msg)
	w.m = next.(Model)
	for i := 0; cmd != nil && i < 10; i++ {
		out := cmd()
		if out == nil {
			break
		}
		if _, ok := out.(tea.BatchMsg); ok {
			break
		}
		if isClip(out) {
			break
		}
		next, cmd = w.m.Update(out)
		w.m = next.(Model)
	}
}

// isClip is bubbletea's clipboard message (its type is unexported).
func isClip(msg tea.Msg) bool { return fmt.Sprintf("%T", msg) == "tea.setClipboardMsg" }

func (w *setupWorld) key(t *testing.T, ks ...any) {
	t.Helper()
	for _, k := range ks {
		switch k := k.(type) {
		case rune:
			w.run(t, tea.KeyPressMsg(tea.Key{Code: k, Text: string(k)}))
		case tea.Key:
			w.run(t, tea.KeyPressMsg(k))
		}
	}
}

func (w *setupWorld) typeText(t *testing.T, s string) {
	t.Helper()
	for _, r := range s {
		w.key(t, r)
	}
}

// open selects the set-up line (0 ntfy, 1 discord, 2 desktop) or, past them,
// a target line, and presses enter.
func (w *setupWorld) open(t *testing.T, line int) *notifySetup {
	t.Helper()
	p := w.m.rd.top()
	p.sel = len(p.set.rows()) + line
	w.key(t, tea.Key{Code: tea.KeyEnter})
	top := w.m.rd.top()
	if top.kind != ovNotifySetup {
		t.Fatalf("opened %v", top.kind)
	}
	return top.ns
}

func (w *setupWorld) body() string { return tokLinesText(w.m.rd.top().body) }

func (w *setupWorld) file(t *testing.T) *notify.File {
	t.Helper()
	return notify.Load(w.notifyPath())
}

func (w *setupWorld) targets() (bool, []string) {
	r := config.Resolver{Getenv: w.getenv, Files: []*config.File{config.Load(config.Path(w.getenv), "config.toml")}}
	return r.Bool("notify.on"), config.SplitList(r.String("notify.targets"))
}

// ntfy.sh, the default: step 1, the subscribe step with the drawn topic
// and its code, the name, then one enter writes, turns on, lists and tests.
func TestNotifySetupNtfyPublic(t *testing.T) {
	w := newSetupWorld(t, "# mine\n")
	s := w.open(t, 0)
	if !strings.Contains(w.m.rd.top().title, "step 1/4") || !strings.Contains(w.body(), "ntfy.sh (free, public)") {
		t.Fatalf("step 1: %q\n%s", w.m.rd.top().title, w.body())
	}
	w.key(t, kEnter)
	if s.step != 3 || !strings.Contains(w.body(), s.topic) || !strings.Contains(w.body(), "https://ntfy.sh/"+s.topic) {
		t.Fatalf("subscribe step %d:\n%s", s.step, w.body())
	}
	if !topicRE.MatchString(s.topic) {
		t.Fatalf("topic %q", s.topic)
	}
	before, _ := os.ReadFile(w.notifyPath())
	w.key(t, kEnter)
	if after, _ := os.ReadFile(w.notifyPath()); s.step != 4 || string(after) != string(before) {
		t.Fatalf("written before the write step (step %d)", s.step)
	}
	w.key(t, kEnter)
	tg, ok := w.file(t).Target("phone")
	if !ok || tg.Topic != s.topic || tg.Server != notify.DefaultServer {
		t.Fatalf("target %v %+v", ok, tg)
	}
	if b, _ := os.ReadFile(w.notifyPath()); !strings.HasPrefix(string(b), "# mine\n") {
		t.Errorf("the person's bytes lost:\n%s", b)
	}
	if on, targets := w.targets(); !on || strings.Join(targets, ",") != "phone" {
		t.Errorf("on %v targets %q", on, targets)
	}
	if len(w.sent) != 1 || !strings.Contains(w.body(), "delivered: check your phone") {
		t.Fatalf("%d sent:\n%s", len(w.sent), w.body())
	}
	// The page C lines read again: phone is a target line now.
	w.key(t, kEnter)
	if p := w.m.rd.top(); p.kind != ovSettings || len(p.set.refs) != 1 || p.set.refs[0].name != "phone" {
		t.Fatalf("back on %v, refs %+v", p.kind, p.set.refs)
	}
}

// An own server: the fields take every key while edited (§5.2: a typed q,
// t, y, Y, c, x is text), the token is masked, never drawn; a failed test
// says so in plain words.
func TestNotifySetupNtfyOwnServerFields(t *testing.T) {
	w := newSetupWorld(t, "")
	w.fail = &notify.SendError{Target: "phone", Status: 401}
	s := w.open(t, 0)
	w.key(t, tea.Key{Code: tea.KeyDown}, kEnter)
	if s.step != 2 {
		t.Fatalf("step %d", s.step)
	}
	w.key(t, kTab) // into the address, edited at once
	w.typeText(t, "https://ntfy.my-host.de/qtyYcxens")
	if got := s.value("server"); got != "https://ntfy.my-host.de/qtyYcxens" || s.step != 2 || w.m.ui.flash != "" {
		t.Fatalf("a bound rune ran an action: %q, step %d, flash %q", got, s.step, w.m.ui.flash)
	}
	w.key(t, tea.Key{Code: tea.KeyBackspace})
	for _, r := range "qtyYcxens" {
		_ = r
		w.key(t, tea.Key{Code: tea.KeyBackspace})
	}
	w.key(t, kTab) // the token
	w.typeText(t, "tk_secret_typed")
	if strings.Contains(w.body(), "tk_secret_typed") || !strings.Contains(w.body(), "(15 characters)") {
		t.Fatalf("the token is drawn:\n%s", w.body())
	}
	w.key(t, kTab)                            // yours?
	w.key(t, tea.Key{Code: tea.KeyBackspace}) // "n" out
	w.typeText(t, "y")
	w.key(t, kEnter) // keeps it, the selection goes
	w.key(t, kEnter) // the subscribe step
	if s.step != 3 || !strings.Contains(w.body(), "https://ntfy.my-host.de/"+s.topic) {
		t.Fatalf("step %d:\n%s", s.step, w.body())
	}
	w.key(t, kEnter, kEnter)
	tg, ok := w.file(t).Target("phone")
	if !ok || tg.Token != "tk_secret_typed" || !tg.SelfHosted || tg.Server != "https://ntfy.my-host.de" {
		t.Fatalf("target %v %+v", ok, tg)
	}
	if !strings.Contains(w.body(), "the server refused the token (or this topic needs one)") || strings.Contains(w.body(), "tk_secret_typed") {
		t.Fatalf("result:\n%s", w.body())
	}
	// t tests again; e goes back to step 1 with the values kept, and the write replaces.
	w.fail = nil
	w.key(t, 't')
	if len(w.sent) != 2 || !strings.Contains(w.body(), "delivered") {
		t.Fatalf("t: %d sent\n%s", len(w.sent), w.body())
	}
	w.key(t, 'e')
	if s.step != 1 || !s.own || s.value("token") != "tk_secret_typed" {
		t.Fatalf("e: step %d own %v", s.step, s.own)
	}
	w.key(t, kEnter)              // step 2, values kept
	w.key(t, kTab, kTab)          // the token
	for range "tk_secret_typed" { // replace it
		w.key(t, tea.Key{Code: tea.KeyBackspace})
	}
	w.typeText(t, "tk_new")
	w.key(t, kEnter, kEnter, kEnter, kEnter) // keep, step 3, step 4, write
	f := w.file(t)
	if len(f.Targets) != 1 || f.Targets[0].Name != "phone" || f.Targets[0].Token != "tk_new" || s.err != "" {
		t.Fatalf("the edit did not replace phone: %d targets, err %q", len(f.Targets), s.err)
	}
}

// §5.2: a field entered by enter or tab takes every printable key, the name
// field included: a typed q never closes, y never copies.
func TestNotifySetupFieldTakesBoundRunes(t *testing.T) {
	w := newSetupWorld(t, "")
	s := w.open(t, 0)
	w.key(t, kEnter, kEnter) // ntfy.sh, the subscribe step
	w.key(t, kEnter)         // step 4: the name
	w.key(t, tea.Key{Code: tea.KeyDown}, kEnter)
	for range "phone" {
		w.key(t, tea.Key{Code: tea.KeyBackspace})
	}
	w.typeText(t, "ntfy.my-host.de/qtyYcxens")
	if got := s.value("name"); got != "ntfy.my-host.de/qtyYcxens" || w.m.rd.top().kind != ovNotifySetup || w.m.ui.flash != "" {
		t.Fatalf("name %q, page %v, flash %q", got, w.m.rd.top().kind, w.m.ui.flash)
	}
	w.key(t, kEsc) // puts the value back
	if got := s.value("name"); got != "phone" || s.editing {
		t.Fatalf("esc kept %q", got)
	}
}

// Esc at each step before the write closes or steps back and writes nothing.
func TestNotifySetupEscWritesNothing(t *testing.T) {
	w := newSetupWorld(t, "")
	s := w.open(t, 0)
	w.key(t, kEnter, kEnter) // step 4
	w.key(t, kEsc)
	if s.step != 3 {
		t.Fatalf("esc on 4: step %d", s.step)
	}
	w.key(t, kEsc)
	if s.step != 1 {
		t.Fatalf("esc on 3 (ntfy.sh): step %d", s.step)
	}
	w.key(t, kEsc)
	if w.m.rd.top().kind != ovSettings {
		t.Fatalf("esc on 1: %v", w.m.rd.top().kind)
	}
	if w.file(t).Exists || len(w.sent) != 0 {
		t.Fatal("written or sent")
	}
}

// Discord: the webhook pasted whole into the masked field, never drawn; a
// pasted newline ends the field.
func TestNotifySetupDiscordPaste(t *testing.T) {
	w := newSetupWorld(t, "")
	s := w.open(t, 1)
	w.key(t, kTab)
	hook := "https://discord.com/api/webhooks/42/pasted-secret"
	w.run(t, tea.PasteMsg{Content: hook + "\n"})
	if s.value("webhook") != hook || s.editing {
		t.Fatalf("paste: %q editing %v", s.value("webhook"), s.editing)
	}
	if strings.Contains(w.body(), "pasted-secret") || strings.Contains(w.m.grid().plain(), "pasted-secret") {
		t.Fatal("the webhook is drawn")
	}
	w.key(t, kEnter, kEnter, kEnter) // name, write
	if tg, ok := w.file(t).Target("team"); !ok || tg.Webhook != hook || len(w.sent) != 1 {
		t.Fatalf("target %v", ok)
	}
}

// The desktop without notify-send stops at step 1.
func TestNotifySetupDesktopWithoutNotifySend(t *testing.T) {
	w := newSetupWorld(t, "")
	lookNotifySend = func(string) (string, error) { return "", errors.New("not found") }
	s := w.open(t, 2)
	w.key(t, kEnter)
	if s.step != 1 || !strings.Contains(w.body(), "install libnotify (notify-send) first") || w.file(t).Exists {
		t.Fatalf("step %d:\n%s", s.step, w.body())
	}
}

// §2: an agent gets the refusal and nothing else: by AGENT_BUS_AGENT, by an
// agent ancestor, or when the agents cannot be read.
func TestNotifySetupRefusesAnAgent(t *testing.T) {
	for name, set := range map[string]func(*setupWorld){
		"env": func(w *setupWorld) {
			get := w.getenv
			w.m.cfg.getenv = func(k string) string {
				if k == "AGENT_BUS_AGENT" {
					return "coder"
				}
				return get(k)
			}
		},
		"ancestor": func(*setupWorld) { nsAgentCaller = func(context.Context) (string, error) { return "p/coder", nil } },
		"unread": func(*setupWorld) {
			nsAgentCaller = func(context.Context) (string, error) { return "", errors.New("dial") }
		},
	} {
		w := newSetupWorld(t, "")
		set(w)
		s := w.open(t, 0)
		w.key(t, kEnter, kEnter, kEnter, kEnter)
		if s.refused == "" || !strings.Contains(w.body(), "the person's act") || w.file(t).Exists || len(w.sent) != 0 {
			t.Errorf("%s: refused %q\n%s", name, s.refused, w.body())
		}
	}
}

// A target's page: x asks (modal: y removes and copies nothing, n keeps),
// n renames (the record moves), s shows the topic and its code, t tests.
func TestNotifySetupTargetPage(t *testing.T) {
	w := newSetupWorld(t, "[ntfy.phone]\ntopic = \""+goldenTopic+"\"\n[desktop.here]\n")
	if err := config.Set(config.Path(w.getenv), "notify.targets", []string{"phone", "here"}); err != nil {
		t.Fatal(err)
	}
	w.key(t, 'C') // read the rows again: two target lines
	w.m = showGroup(t, w.m, config.GroupNotifications)
	w.m.rd.top().set.keys = true
	line := -1
	for i, r := range w.m.rd.top().set.refs {
		if r.name == "phone" {
			line = len(notifyAssistKinds) + i // past the three set-up lines
		}
	}
	s := w.open(t, line)
	if s.target != "phone" || strings.Contains(w.body(), goldenTopic) {
		t.Fatalf("target page %q:\n%s", s.target, w.body())
	}
	w.key(t, 's')
	if !strings.Contains(w.body(), goldenTopic) || !strings.Contains(w.body(), "▀") {
		t.Fatalf("s:\n%s", w.body())
	}
	w.key(t, 'z') // any key goes back
	w.key(t, 't')
	if len(w.sent) != 1 {
		t.Fatal("t sent nothing")
	}
	w.key(t, 'x', 'n')
	if _, ok := w.file(t).Target("phone"); !ok {
		t.Fatal("x then n removed it")
	}
	w.key(t, 'n')
	for range "phone" {
		w.key(t, tea.Key{Code: tea.KeyBackspace})
	}
	w.typeText(t, "mobile")
	w.key(t, kEnter)
	if _, ok := w.file(t).Target("mobile"); !ok {
		t.Fatalf("not renamed:\n%s", w.body())
	}
	if _, targets := w.targets(); strings.Join(targets, ",") != "mobile,here" {
		t.Errorf("targets %q", targets)
	}
	next, cmd := w.m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	w.m = next.(Model)
	if cmd != nil {
		t.Fatal("x ran a command before its y")
	}
	next, cmd = w.m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	w.m = next.(Model)
	if cmd == nil {
		t.Fatal("y removed nothing")
	}
	out := cmd()
	if isClip(out) || strings.Contains(w.m.ui.flash, "copied") {
		t.Fatal("y copied while x asked")
	}
	w.run(t, out)
	if _, ok := w.file(t).Target("mobile"); ok {
		t.Fatal("not removed")
	}
	if _, targets := w.targets(); strings.Join(targets, ",") != "here" {
		t.Errorf("targets %q", targets)
	}
}

// §5.1: the read-only template stays reachable from the first step (?).
func TestNotifySetupFormatPage(t *testing.T) {
	w := newSetupWorld(t, "")
	w.open(t, 0)
	if !strings.Contains(w.body(), "? shows notify.toml's format") {
		t.Fatalf("no hint:\n%s", w.body())
	}
	w.key(t, '?')
	if p := w.m.rd.top(); p.kind != ovMessage || !strings.Contains(p.title, "Set up ntfy") || !strings.Contains(p.plain, "[ntfy.") {
		t.Fatalf("? opened %v %q", p.kind, p.title)
	}
	if w.file(t).Exists {
		t.Fatal("written")
	}
}

// The listing: a write lists the name once, a replaced name's entry becomes
// the new one, and nothing is listed twice.
func TestNotifySetupListsOnce(t *testing.T) {
	w := newSetupWorld(t, "")
	if err := config.Set(config.Path(w.getenv), "notify.targets", []string{"phone", "mobile", "here"}); err != nil {
		t.Fatal(err)
	}
	if err := nsList(w.getenv, "mobile", "phone"); err != nil {
		t.Fatal(err)
	}
	if on, targets := w.targets(); !on || strings.Join(targets, ",") != "mobile,here" {
		t.Fatalf("on %v targets %q", on, targets)
	}
}

// Discord: enter with no webhook pasted stays on step 1 and says so.
func TestNotifySetupDiscordNeedsTheWebhook(t *testing.T) {
	w := newSetupWorld(t, "")
	s := w.open(t, 1)
	w.key(t, kEnter)
	if s.step != 1 || !strings.Contains(w.body(), "paste the webhook first") {
		t.Fatalf("step %d:\n%s", s.step, w.body())
	}
}

// foureyes: removing the last listed target asks whether to turn
// notifications off; only y does, any other key keeps them on.
func TestNotifySetupRemoveLastAsksOff(t *testing.T) {
	for _, answer := range []rune{'y', 'n'} {
		w := newSetupWorld(t, "[ntfy.phone]\ntopic = \""+goldenTopic+"\"\n")
		if err := config.Set(config.Path(w.getenv), "notify.targets", []string{"phone"}); err != nil {
			t.Fatal(err)
		}
		if err := config.Set(config.Path(w.getenv), "notify.on", true); err != nil {
			t.Fatal(err)
		}
		w.key(t, 'C')
		w.m = showGroup(t, w.m, config.GroupNotifications)
		w.m.rd.top().set.keys = true
		w.open(t, len(notifyAssistKinds))
		w.key(t, 'x', 'y')
		if !strings.Contains(w.body(), "turn notifications off too? y/n") {
			t.Fatalf("no question:\n%s", w.body())
		}
		w.key(t, answer)
		if on, targets := w.targets(); on != (answer != 'y') || len(targets) != 0 {
			t.Fatalf("answer %c: on %v targets %q", answer, on, targets)
		}
	}
}

// foureyes: Discord's step 1 takes a paste without a tab first.
func TestNotifySetupDiscordPasteWithoutTab(t *testing.T) {
	w := newSetupWorld(t, "")
	s := w.open(t, 1)
	hook := "https://discord.com/api/webhooks/42/pasted-secret"
	w.run(t, tea.PasteMsg{Content: hook + "\n"})
	if s.value("webhook") != hook || s.editing || s.focus != -1 {
		t.Fatalf("paste without tab: %q editing %v focus %d", s.value("webhook"), s.editing, s.focus)
	}
}

// The key that ends the code view does nothing else ("until any key").
func TestNotifySetupCodeViewKeyIsConsumed(t *testing.T) {
	w := newSetupWorld(t, "[ntfy.phone]\ntopic = \""+goldenTopic+"\"\n")
	w.key(t, 'C')
	w.m = showGroup(t, w.m, config.GroupNotifications)
	w.m.rd.top().set.keys = true
	s := w.open(t, len(notifyAssistKinds))
	w.key(t, 's', 't')
	if s.showTopic || len(w.sent) != 0 {
		t.Fatalf("t after s: showTopic %v, %d sent", s.showTopic, len(w.sent))
	}
}

// An address that is no address stays on step 2, said, nothing written.
func TestNotifySetupBadAddress(t *testing.T) {
	w := newSetupWorld(t, "")
	s := w.open(t, 0)
	w.key(t, tea.Key{Code: tea.KeyDown}, kEnter, kTab)
	w.typeText(t, "ftp://x")
	w.key(t, kEnter, kEnter)
	if s.step != 2 || !strings.Contains(w.body(), "the address: https://") || w.file(t).Exists {
		t.Fatalf("step %d:\n%s", s.step, w.body())
	}
}
