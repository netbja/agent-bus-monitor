package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/notify"
)

// assistEnv is a config home with notify.toml holding a ntfy target phone
// (topic secret-topic-xyz) and a Discord target team, and a record where
// phone's last test failed after an ordinary send.
func assistEnv(t *testing.T) (getenv func(string) string, notifyPath string) {
	t.Helper()
	state := t.TempDir()
	getenv = settingsEnv(t, "", "", map[string]string{"XDG_STATE_HOME": state, "AGENT_BUS_NO_UPDATE_CHECK": "1"})
	notifyPath = filepath.Join(filepath.Dir(config.Path(getenv)), "notify.toml")
	if err := os.WriteFile(notifyPath, []byte("[ntfy.phone]\ntopic = \"secret-topic-xyz\"\n[discord.team]\nwebhook = \"https://discord.com/api/webhooks/1/hook-secret\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := notify.UpdateLast(notify.LastPath(filepath.Join(state, "agentbus")), func(l *notify.Last) error {
		l.Add("phone", notify.Outcome{At: time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local), OK: false, Err: "phone: 401", Test: true}, false)
		l.Add("phone", notify.Outcome{Project: "p", At: time.Date(2026, 11, 4, 10, 0, 0, 0, time.Local), OK: true}, true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return getenv, notifyPath
}

var topicRE = regexp.MustCompile(`^agentbus-[a-z0-9]{20}$`)

// The template the assistant hands out is accepted by notify.Load as it is,
// and a topic that is not the drawn shape (guessable) in the same template is
// refused: the drawn topic is only worth anything while the loader keeps its
// own rule.
func TestNotifyTemplateTopicRoundTrip(t *testing.T) {
	load := func(topic string) *notify.File {
		dir := filepath.Join(t.TempDir(), "agentbus")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "notify.toml")
		if err := os.WriteFile(p, []byte(notifyTemplate("ntfy", "phone", topic)), 0o600); err != nil {
			t.Fatal(err)
		}
		return notify.Load(p)
	}
	if f := load(drawTopic()); len(f.Targets) != 1 {
		t.Fatalf("the drawn template is refused: %v %q", f.Warnings, f.Refused)
	}
	for _, bad := range []string{"phone", "agentbus", "my topic", "agentbus/abc12345678901"} {
		if f := load(bad); len(f.Targets) != 0 {
			t.Errorf("guessable topic %q taken", bad)
		}
	}
}

// A drawn topic is long, of ntfy's own characters, and new each time.
func TestDrawTopic(t *testing.T) {
	a, b := drawTopic(), drawTopic()
	if !topicRE.MatchString(a) || !topicRE.MatchString(b) || a == b {
		t.Fatalf("topics %q %q", a, b)
	}
}

// The ntfy page: the steps, notify.toml's state by name and kind only (a
// saved topic or webhook never shown), a template with a freshly drawn
// topic under a name not taken yet, which y copies, and the last test of the
// kind's targets; nothing written.
func TestNotifyAssistNtfy(t *testing.T) {
	getenv, path := assistEnv(t)
	before, _ := os.ReadFile(path)
	f := readNotifyAssist(getenv, "ntfy")
	p := notifyAssistPage("ntfy", f)
	body := tokLinesText(p.body)
	for _, s := range []string{path, "2 targets", "phone", "ntfy", "team", "discord", "[ntfy.phone-2]", f.Topic,
		"agentbus notifications test phone-2", "last test", "failed", "Targets"} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q:\n%s", s, body)
		}
	}
	for _, s := range []string{"secret-topic-xyz", "hook-secret", "https://discord.com"} {
		if strings.Contains(body, s) || strings.Contains(p.plain, s) {
			t.Fatalf("a saved secret %q is shown:\n%s", s, body)
		}
	}
	if !topicRE.MatchString(f.Topic) || !strings.Contains(p.plain, "[ntfy.phone-2]\ntopic = \""+f.Topic+"\"") {
		t.Fatalf("y copies %q (topic %q)", p.plain, f.Topic)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("notify.toml was written")
	}
}

// Discord: the webhook steps and a placeholder, never a URL drawn or saved;
// the desktop: notify-send's presence said.
func TestNotifyAssistDiscordAndDesktop(t *testing.T) {
	getenv, _ := assistEnv(t)
	p := notifyAssistPage("discord", readNotifyAssist(getenv, "discord"))
	body := tokLinesText(p.body)
	if !strings.Contains(body, "Webhooks") || !strings.Contains(p.plain, "[discord.team-2]") || strings.Contains(body, "hook-secret") {
		t.Fatalf("discord page:\n%s\ncopy %q", body, p.plain)
	}
	defer func(l func(string) (string, error)) { lookNotifySend = l }(lookNotifySend)
	lookNotifySend = func(string) (string, error) { return "", os.ErrNotExist }
	p = notifyAssistPage("desktop", readNotifyAssist(getenv, "desktop"))
	if body := tokLinesText(p.body); !strings.Contains(body, "notify-send is not on PATH") || p.plain != "[desktop.here]\n" {
		t.Fatalf("desktop page:\n%s\ncopy %q", body, p.plain)
	}
}

// A missing or refused notify.toml is said, with how to create it.
func TestNotifyAssistFileStates(t *testing.T) {
	getenv := settingsEnv(t, "", "", nil)
	body := tokLinesText(notifyAssistPage("ntfy", readNotifyAssist(getenv, "ntfy")).body)
	if !strings.Contains(body, "does not exist yet") || !strings.Contains(body, "[ntfy.phone]") {
		t.Fatalf("absent:\n%s", body)
	}
	path := filepath.Join(filepath.Dir(config.Path(getenv)), "notify.toml")
	os.WriteFile(path, []byte("[ntfy.phone]\ntopic = \"secret-topic-xyz\"\n"), 0o644)
	body = tokLinesText(notifyAssistPage("ntfy", readNotifyAssist(getenv, "ntfy")).body)
	if !strings.Contains(body, "refused") || !strings.Contains(body, "chmod 600") || strings.Contains(body, "secret-topic-xyz") {
		t.Fatalf("refused:\n%s", body)
	}
}

// On page C, the Notifications group's three set-up lines follow its target
// lines; Enter on one opens its page.
func TestSettingsNotifyAssistLines(t *testing.T) {
	getenv, _ := assistEnv(t)
	var themed string
	m := showGroup(t, press(t, settingsShell(t, getenv, &themed), 'C'), config.GroupNotifications)
	m.rd.top().set.keys = true
	body := tokLinesText(m.rd.top().body)
	for _, s := range []string{"Set up ntfy", "Set up Discord", "Set up the desktop"} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q:\n%s", s, body)
		}
	}
	p := m.rd.top()
	for range p.set.rows() {
		m = press(t, m, tea.Key{Code: tea.KeyDown})
	}
	m = press(t, m, tea.Key{Code: tea.KeyDown}) // the second line: Discord
	if p := m.rd.top(); p.sel != len(p.set.rows())+1 {
		t.Fatalf("sel %d", p.sel)
	}
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("Enter reads nothing")
	}
	msg, ok := cmd().(notifyAssistMsg)
	if !ok {
		t.Fatal("Enter's command is not the assistant's read")
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if p := m.rd.top(); p.kind != ovMessage || !strings.Contains(p.title, "Discord") {
		t.Fatalf("opened %v %q", p.kind, p.title)
	}
}
