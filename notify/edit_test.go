package notify

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// notify-guided-setup §4: notify.Edit, the one writer of notify.toml.

const handFile = `# my notifications, written by hand
[ntfy.phone]   # the phone
topic = "agentbus-7f3c9a1b"
token = "tk_secret"   # keep

# the team
[discord.team]
webhook = "https://discord.com/api/webhooks/123/abcdef"

[desktop.here]
`

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// Appending a target keeps every other byte, and the new target loads.
func TestEditAppendKeepsEveryOtherByte(t *testing.T) {
	p := writeFile(t, handFile, 0o600)
	if err := Edit(p, func(d *Doc) error {
		return d.Add(Target{Name: "phone-2", Kind: "ntfy", Server: DefaultServer, Topic: "agentbus-abcdefghij0123456789"})
	}); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if want := handFile + "\n[ntfy.phone-2]\ntopic = \"agentbus-abcdefghij0123456789\"\n"; got != want {
		t.Fatalf("after append:\n%s\nwant:\n%s", got, want) // one blank line, then the table, encoded
	}
	f := Load(p)
	if tg, ok := f.Target("phone-2"); f.Refused != "" || !ok || tg.Topic != "agentbus-abcdefghij0123456789" || len(f.Targets) != 4 {
		t.Fatalf("refused %q, targets %v", f.Refused, f.Targets)
	}
	if m := mode(t, p); m != 0o600 {
		t.Fatalf("mode %o", m)
	}
}

// Remove and rename touch only their own table.
func TestEditRemoveAndRenameTouchOnlyTheirTable(t *testing.T) {
	p := writeFile(t, handFile, 0o600)
	if err := Edit(p, func(d *Doc) error { return d.Remove("team") }); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if strings.Contains(got, "discord.team") || strings.Contains(got, "webhooks") || !strings.Contains(got, "token = \"tk_secret\"   # keep") ||
		!strings.Contains(got, "[desktop.here]") || !strings.HasPrefix(got, "# my notifications, written by hand\n[ntfy.phone]   # the phone\n") {
		t.Fatalf("after remove:\n%s", got)
	}
	if err := Edit(p, func(d *Doc) error { return d.Rename("phone", "pixel") }); err != nil {
		t.Fatal(err)
	}
	got = read(t, p)
	if !strings.Contains(got, "[ntfy.pixel]   # the phone\ntopic = \"agentbus-7f3c9a1b\"") || strings.Contains(got, "[ntfy.phone]") {
		t.Fatalf("after rename:\n%s", got)
	}
	for _, c := range []struct {
		name, why string
		fn        func(*Doc) error
	}{
		{"remove an unknown name", "no target called nope", func(d *Doc) error { return d.Remove("nope") }},
		{"rename onto a taken name", "already called here", func(d *Doc) error { return d.Rename("pixel", "here") }},
		{"rename to a bad name", "not a valid target name", func(d *Doc) error { return d.Rename("pixel", "Pixel 9") }},
		{"add a taken name", "already called here", func(d *Doc) error { return d.Add(Target{Name: "here", Kind: "desktop"}) }},
	} {
		before := read(t, p)
		if err := Edit(p, c.fn); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v, want %q", c.name, err, c.why)
		}
		if read(t, p) != before {
			t.Errorf("%s: the file changed", c.name)
		}
	}
}

// A value Load would refuse is never written: values are checked by Load's
// rules, then encoded (a quote or a newline cannot break out of the string).
func TestEditRefusesWhatLoadRefuses(t *testing.T) {
	p := writeFile(t, "", 0o600)
	for _, c := range []struct {
		tg  Target
		why string // the rule, named before anything is written
	}{
		{Target{Name: "a", Kind: "ntfy", Server: DefaultServer, Topic: "short"}, "ntfy.a.topic: want 12 to 64"},
		{Target{Name: "a", Kind: "ntfy", Server: DefaultServer, Topic: "agentbus-0123456789\"\n[x]"}, "ntfy.a.topic: want a one-line string"},
		{Target{Name: "a", Kind: "ntfy", Server: "http://ntfy.example.org", Topic: "agentbus-0123456789"}, "ntfy.a.server: http only on a loopback"},
		{Target{Name: "a", Kind: "ntfy", Server: DefaultServer, Topic: "agentbus-0123456789", SelfHosted: true}, "ntfy.a.self_hosted: ntfy.sh is a public server"},
		{Target{Name: "a", Kind: "discord", Webhook: "https://evil.example/api/webhooks/1/x"}, "discord.a.webhook: want a webhook on discord.com"},
		{Target{Name: "a", Kind: "carrier-pigeon"}, "unknown kind"},
	} {
		tg := c.tg
		err := Edit(p, func(d *Doc) error { return d.Add(tg) })
		if err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v, want %q", tg.Kind, err, c.why)
		}
		for _, secret := range []string{tg.Topic, tg.Webhook} {
			if secret != "" && err != nil && strings.Contains(err.Error(), secret) {
				t.Errorf("the error quotes a secret: %v", err)
			}
		}
	}
	if got := read(t, p); got != "" {
		t.Fatalf("a refused target reached the file:\n%s", got)
	}
}

// A private server over http is written with insecure = true, as Load
// accepts it; its subscribe link keeps the scheme.
func TestEditPrivateHTTPServer(t *testing.T) {
	p := writeFile(t, "", 0o600)
	tg := Target{Name: "home", Kind: "ntfy", Server: "http://192.168.1.20", Topic: "agentbus-0123456789ab", SelfHosted: true}
	if err := Edit(p, func(d *Doc) error { return d.Add(tg) }); err != nil {
		t.Fatal(err)
	}
	got, ok := Load(p).Target("home")
	if !ok || !got.SelfHosted || SubscribeLink(got) != "http://192.168.1.20/agentbus-0123456789ab" {
		t.Fatalf("loaded %v ok %v link %q:\n%s", got, ok, SubscribeLink(got), read(t, p))
	}
	if l := SubscribeLink(Target{Kind: "ntfy", Server: DefaultServer, Topic: "agentbus-x"}); l != "https://ntfy.sh/agentbus-x" {
		t.Fatalf("ntfy.sh link %q", l)
	}
}

// A new file is created 0600 in a folder created 0700.
func TestEditCreatesFileAndFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg", "agentbus")
	p := filepath.Join(dir, "notify.toml")
	if err := Edit(p, func(d *Doc) error { return d.Add(Target{Name: "here", Kind: "desktop"}) }); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, p); m != 0o600 {
		t.Errorf("file mode %o", m)
	}
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("folder mode %o", m)
	}
	if _, ok := Load(p).Target("here"); !ok {
		t.Fatal("not loaded")
	}
}

// A file Load refuses whole, a folder others can write, or a file that does
// not parse is never rewritten and never chmod-ed.
func TestEditNeverTouchesARefusedFile(t *testing.T) {
	add := func(d *Doc) error { return d.Add(Target{Name: "x", Kind: "desktop"}) }
	open := writeFile(t, handFile, 0o644)
	if err := Edit(open, add); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("open file: %v", err)
	}
	if m := mode(t, open); m != 0o644 || read(t, open) != handFile {
		t.Errorf("an open file was touched: %o", m)
	}
	bad := writeFile(t, "[ntfy.phone\ntopic = 1\n", 0o600)
	if err := Edit(bad, add); err == nil || !strings.Contains(err.Error(), "has an error at line 2") {
		t.Errorf("unparsable file: %v", err)
	}
	if read(t, bad) != "[ntfy.phone\ntopic = 1\n" {
		t.Error("an unparsable file was rewritten")
	}
	shared := writeFile(t, handFile, 0o600)
	if err := os.Chmod(filepath.Dir(shared), 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(shared), 0o700) })
	if err := Edit(shared, add); err == nil || read(t, shared) != handFile {
		t.Errorf("a folder others can write: %v", err)
	}
}

// A new file in a folder others can write is not created (the folder could
// swap it).
func TestEditRefusesASharedFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agentbus")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	p := filepath.Join(dir, "notify.toml")
	if err := Edit(p, func(d *Doc) error { return d.Add(Target{Name: "x", Kind: "desktop"}) }); err == nil || !strings.Contains(err.Error(), "writable by others") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file was created in a shared folder")
	}
}

// A hand edit between the read and the write wins: nothing is written.
func TestEditRefusesWhenTheFileChangedMeanwhile(t *testing.T) {
	p := writeFile(t, handFile, 0o600)
	err := Edit(p, func(d *Doc) error {
		later := time.Now().Add(2 * time.Second)
		if err := os.WriteFile(p, []byte(handFile+"\n[desktop.other]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, later, later)
		return d.Add(Target{Name: "x", Kind: "desktop"})
	})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("err %v, want ErrChanged", err)
	}
	if got := read(t, p); strings.Contains(got, "[desktop.x]") || !strings.Contains(got, "[desktop.other]") {
		t.Fatalf("the person's edit was lost:\n%s", got)
	}
}

// A re-load that does not find the target puts the old bytes back, or
// removes a file that was not there before.
func TestEditRollsBack(t *testing.T) {
	defer func(f func(string) *File) { reload = f }(reload)
	reload = func(string) *File { return &File{Exists: true} } // the target is not there
	p := writeFile(t, handFile, 0o600)
	if err := Edit(p, func(d *Doc) error { return d.Add(Target{Name: "x", Kind: "desktop"}) }); err == nil {
		t.Fatal("no error")
	}
	if read(t, p) != handFile {
		t.Fatalf("not rolled back:\n%s", read(t, p))
	}
	fresh := filepath.Join(t.TempDir(), "agentbus", "notify.toml")
	if err := Edit(fresh, func(d *Doc) error { return d.Add(Target{Name: "x", Kind: "desktop"}) }); err == nil {
		t.Fatal("no error on a new file")
	}
	if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file that did not exist is left: %v", err)
	}
}
