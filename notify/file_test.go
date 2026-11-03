package notify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, text string, mode os.FileMode) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "agentbus")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "notify.toml")
	if err := os.WriteFile(p, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// noSecret fails when any warning carries a value from the file.
func noSecret(t *testing.T, f *File, secrets ...string) {
	t.Helper()
	for _, w := range f.Warnings {
		for _, s := range secrets {
			if strings.Contains(w.String(), s) {
				t.Fatalf("warning %q echoes %q", w, s)
			}
		}
	}
}

const goodFile = `
[ntfy.phone]
topic = "agentbus-7f3c9a1b"
token = "tk_secret"

[ntfy.home]
topic = "agentbus-home-01"
server = "https://ntfy.example.org"
self_hosted = true

[discord.team]
webhook = "https://discord.com/api/webhooks/123/abcdef"

[desktop.here]
`

func TestLoadReadsEveryKind(t *testing.T) {
	f := Load(writeFile(t, goodFile, 0o600))
	if f.Refused != "" || len(f.Warnings) != 0 {
		t.Fatalf("refused %q warnings %v", f.Refused, f.Warnings)
	}
	got := map[string]Target{}
	for _, tg := range f.Targets {
		got[tg.Name] = tg
	}
	if p := got["phone"]; p.Kind != "ntfy" || p.URL() != "https://ntfy.sh/agentbus-7f3c9a1b" || p.Token != "tk_secret" || p.Private() {
		t.Fatalf("phone %#v", p)
	}
	if h := got["home"]; h.URL() != "https://ntfy.example.org/agentbus-home-01" || !h.Private() {
		t.Fatalf("home %#v", h)
	}
	if d := got["team"]; d.Kind != "discord" || d.URL() != "https://discord.com/api/webhooks/123/abcdef" || d.Private() {
		t.Fatalf("team %#v", d)
	}
	if d := got["here"]; d.Kind != "desktop" || !d.Private() {
		t.Fatalf("here %#v", d)
	}
	if len(f.Targets) != 4 || f.Targets[0].Name != "here" {
		t.Fatalf("targets sorted by name: %v", f.Targets)
	}
}

func TestTargetNeverPrintsItsSecrets(t *testing.T) {
	f := Load(writeFile(t, goodFile, 0o600))
	for _, tg := range f.Targets {
		for _, form := range []string{"%v", "%+v", "%#v", "%s"} {
			s := fmt.Sprintf(form, tg)
			for _, secret := range []string{"tk_secret", "agentbus-7f3c9a1b", "abcdef", "agentbus-home-01"} {
				if strings.Contains(s, secret) {
					t.Fatalf("%s of %s prints %q: %s", form, tg.Name, secret, s)
				}
			}
		}
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	f := Load(filepath.Join(t.TempDir(), "notify.toml"))
	if f.Exists || f.Refused != "" || len(f.Targets) != 0 {
		t.Fatalf("%+v", f)
	}
}

func TestReadableByOthersRefusedWhole(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o620} {
		f := Load(writeFile(t, goodFile, mode))
		if f.Refused == "" || len(f.Targets) != 0 || !f.Exists {
			t.Fatalf("mode %o: %+v", mode, f)
		}
		if !strings.Contains(f.Refused, "chmod 600") {
			t.Fatalf("the refusal says the fix: %q", f.Refused)
		}
	}
}

func TestSymlinkFollowedToOwnFile(t *testing.T) {
	real := writeFile(t, goodFile, 0o600)
	link := filepath.Join(t.TempDir(), "notify.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if f := Load(link); f.Refused != "" || len(f.Targets) != 4 {
		t.Fatalf("%+v", f)
	}
}

func TestNotARegularFileRefused(t *testing.T) {
	dir := t.TempDir()
	if f := Load(dir); f.Refused == "" {
		t.Fatalf("a directory is refused: %+v", f)
	}
}

func TestUnparsableNamesTheLineOnly(t *testing.T) {
	f := Load(writeFile(t, "[ntfy.phone]\ntopic = \"tk_leak_value\n", 0o600))
	if f.Refused == "" || len(f.Targets) != 0 {
		t.Fatalf("%+v", f)
	}
	if strings.Contains(f.Refused, "tk_leak_value") {
		t.Fatalf("refusal echoes the file: %q", f.Refused)
	}
}

func TestTargetRules(t *testing.T) {
	cases := []struct {
		name, text, key, msg string
		secrets              []string
	}{
		{"http without insecure", `[ntfy.x]
topic = "agentbus-0123456789"
server = "http://192.168.1.4"`, "ntfy.x.server", "https", nil},
		{"http insecure on a public host", `[ntfy.x]
topic = "agentbus-0123456789"
server = "http://ntfy.example.org"
insecure = true`, "ntfy.x.server", "private", []string{"ntfy.example.org"}},
		{"user info", `[ntfy.x]
topic = "agentbus-0123456789"
server = "https://bob:pw-123@ntfy.example.org"`, "ntfy.x.server", "user info", []string{"pw-123", "bob"}},
		{"query", `[ntfy.x]
topic = "agentbus-0123456789"
server = "https://ntfy.example.org/?auth=zzz"`, "ntfy.x.server", "query", []string{"zzz"}},
		{"short topic", `[ntfy.x]
topic = "short"`, "ntfy.x.topic", "12", []string{"short"}},
		{"topic with a slash", `[ntfy.x]
topic = "agentbus/0123456789"`, "ntfy.x.topic", "12", []string{"agentbus/0123456789"}},
		{"no topic", `[ntfy.x]
token = "tk_zzz"`, "ntfy.x", "no topic", []string{"tk_zzz"}},
		{"self_hosted on ntfy.sh", `[ntfy.x]
topic = "agentbus-0123456789"
self_hosted = true`, "ntfy.x.self_hosted", "ntfy.sh", nil},
		{"self_hosted on ntfy.sh named", `[ntfy.x]
topic = "agentbus-0123456789"
server = "https://NTFY.sh"
self_hosted = true`, "ntfy.x.self_hosted", "ntfy.sh", nil},
		{"unknown key", `[ntfy.x]
topic = "agentbus-0123456789"
password = "hunter2-zz"`, "ntfy.x.password", "secret", []string{"hunter2-zz"}},
		{"token not a string", `[ntfy.x]
topic = "agentbus-0123456789"
token = 42`, "ntfy.x.token", "string", nil},
		{"foreign webhook host", `[discord.x]
webhook = "https://evil.example/api/webhooks/1/abc-zz"`, "discord.x.webhook", "discord.com", []string{"abc-zz", "evil.example"}},
		{"webhook over http", `[discord.x]
webhook = "http://discord.com/api/webhooks/1/abc-zz"`, "discord.x.webhook", "https", []string{"abc-zz"}},
		{"webhook not a webhook path", `[discord.x]
webhook = "https://discord.com/channels/1/abc-zz"`, "discord.x.webhook", "/api/webhooks/", []string{"abc-zz"}},
		{"no webhook", `[discord.x]`, "discord.x", "no webhook", nil},
		{"desktop with a key", `[desktop.x]
command = "rm -rf /"`, "desktop.x.command", "unknown key", []string{"rm -rf"}},
		{"unknown kind", `[signal.x]
number = "+3312345"`, "signal.x", "unknown", []string{"+3312345"}},
		{"bad name", `[ntfy.Phone]
topic = "agentbus-0123456789"`, "ntfy.Phone", "name", nil},
		{"not a table", `ntfy = "agentbus-0123456789"`, "ntfy", "table", []string{"agentbus-0123456789"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := Load(writeFile(t, c.text, 0o600))
			if f.Refused != "" {
				t.Fatalf("refused whole: %q", f.Refused)
			}
			if len(f.Targets) != 0 {
				t.Fatalf("target taken: %v", f.Targets)
			}
			found := false
			for _, w := range f.Warnings {
				if w.Key == c.key && strings.Contains(w.Msg, c.msg) && w.Line > 0 {
					found = true
				}
			}
			if !found {
				t.Fatalf("want a warning on %s with %q at its line, got %v", c.key, c.msg, f.Warnings)
			}
			noSecret(t, f, c.secrets...)
		})
	}
}

func TestHTTPAcceptedOnPrivateHostWithInsecure(t *testing.T) {
	for _, host := range []string{"127.0.0.1:8080", "[::1]", "localhost", "192.168.1.4", "10.0.0.2", "172.16.3.3", "nas.local", "[fd00::1]"} {
		f := Load(writeFile(t, `[ntfy.x]
topic = "agentbus-0123456789"
server = "http://`+host+`"
insecure = true
self_hosted = true`, 0o600))
		if len(f.Targets) != 1 || len(f.Warnings) != 0 {
			t.Fatalf("%s: targets %v warnings %v", host, f.Targets, f.Warnings)
		}
		if got := f.Targets[0].URL(); got != "http://"+host+"/agentbus-0123456789" {
			t.Fatalf("%s: url %q", host, got)
		}
	}
}

func TestServerPathKept(t *testing.T) {
	f := Load(writeFile(t, `[ntfy.x]
topic = "agentbus-0123456789"
server = "https://example.org/ntfy/"`, 0o600))
	if len(f.Targets) != 1 || f.Targets[0].URL() != "https://example.org/ntfy/agentbus-0123456789" {
		t.Fatalf("%v %v", f.Targets, f.Warnings)
	}
}

func TestDiscordAppHostAccepted(t *testing.T) {
	f := Load(writeFile(t, `[discord.x]
webhook = "https://discordapp.com/api/webhooks/1/abc"`, 0o600))
	if len(f.Targets) != 1 {
		t.Fatalf("%v", f.Warnings)
	}
}

func TestSameNameInTwoKindsRefusesBoth(t *testing.T) {
	f := Load(writeFile(t, `[ntfy.x]
topic = "agentbus-0123456789"
[desktop.x]
`, 0o600))
	if len(f.Targets) != 0 {
		t.Fatalf("targets %v", f.Targets)
	}
	n := 0
	for _, w := range f.Warnings {
		if strings.Contains(w.Msg, "twice") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("one warning per table: %v", f.Warnings)
	}
}

func TestOneBadTargetKeepsTheOthers(t *testing.T) {
	f := Load(writeFile(t, `[ntfy.bad]
topic = "x"
[desktop.here]
`, 0o600))
	if len(f.Targets) != 1 || f.Targets[0].Name != "here" || len(f.Warnings) != 1 {
		t.Fatalf("targets %v warnings %v", f.Targets, f.Warnings)
	}
}

func TestPath(t *testing.T) {
	env := map[string]string{"XDG_CONFIG_HOME": "/x/cfg", "HOME": "/home/u"}
	get := func(k string) string { return env[k] }
	if got := Path(get); got != "/x/cfg/agentbus/notify.toml" {
		t.Fatal(got)
	}
	env["XDG_CONFIG_HOME"] = "relative"
	if got := Path(get); got != "/home/u/.config/agentbus/notify.toml" {
		t.Fatal(got)
	}
	env["HOME"] = ""
	if got := Path(get); got != "" {
		t.Fatal(got)
	}
}

func TestSenderForEachKind(t *testing.T) {
	f := Load(writeFile(t, goodFile, 0o600))
	for _, tg := range f.Targets {
		s := tg.Sender(nil)
		if s.Name() != tg.Name || s.Kind() != tg.Kind {
			t.Fatalf("%s: sender %s/%s", tg.Name, s.Name(), s.Kind())
		}
	}
}
