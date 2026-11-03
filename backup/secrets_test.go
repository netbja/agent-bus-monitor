package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testRecipient = "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq"

// fakeAge is an age stand-in: -r R encrypts to "FAKEAGE R" plus base64,
// -d -i F decrypts (and fails without the identity file). It records each
// call's arguments.
func fakeAge(t *testing.T) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "age")
	script := `#!/bin/sh
echo "$@" >> ` + calls + `
if [ "$1" = "-d" ]; then
  [ -f "$3" ] || { echo "no identity" >&2; exit 1; }
  read header
  base64 -d
else
  [ "$1" = "-r" ] || exit 2
  echo "FAKEAGE $2"
  base64
fi
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func TestValidRecipient(t *testing.T) {
	for r, want := range map[string]bool{
		testRecipient:                    true,
		"age1" + strings.Repeat("q", 58): true,
		"age1" + strings.Repeat("b", 58): false, // b is not in bech32's charset
		"AGE1" + strings.Repeat("q", 58): false,
		"age1qq":                         false,
		"ssh-ed25519 AAAA":               false,
		"":                               false,
	} {
		if got := ValidRecipient(r); got != want {
			t.Errorf("ValidRecipient(%q) = %v", r, got)
		}
	}
}

func TestSealAndOpenSecrets(t *testing.T) {
	age, calls := fakeAge(t)
	loc, _ := home(t)
	// The secrets are 0000 in home(): make them readable for --secrets.
	os.Chmod(loc.PasswordFile, 0o600)
	os.Chmod(loc.EnvFile("demo"), 0o600)
	os.Chmod(filepath.Join(loc.ConfigDir, "notify.toml"), 0o600)
	srcs := Secrets([]string{"demo", "other"}, loc)
	if len(srcs) != 3 || srcs[0].Member != "redis.env" || srcs[1].Member != "notify.toml" || srcs[2].Member != "services/demo.env" {
		t.Fatalf("secrets %+v", srcs)
	}
	sealed, err := SealSecrets(context.Background(), age, testRecipient, srcs)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("pw-SECRET")) || !bytes.HasPrefix(sealed, []byte("FAKEAGE "+testRecipient)) {
		t.Fatalf("sealed %q", sealed)
	}
	c, _ := os.ReadFile(calls)
	if strings.TrimSpace(string(c)) != "-r "+testRecipient {
		t.Fatalf("age called with %q (never the clear text, never a file)", c)
	}
	identity := filepath.Join(t.TempDir(), "key.txt")
	os.WriteFile(identity, []byte("AGE-SECRET-KEY-1..."), 0o600)
	got := map[string]string{}
	if err := OpenSecrets(context.Background(), age, identity, bytes.NewReader(sealed), func(name string, body []byte) error {
		got[name] = string(body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got["redis.env"], "pw-SECRET") || !strings.Contains(got["services/demo.env"], "pw-SECRET") ||
		!strings.Contains(got["notify.toml"], "pw-SECRET") || len(got) != 3 {
		t.Fatalf("opened %q", got)
	}
	if err := OpenSecrets(context.Background(), age, filepath.Join(t.TempDir(), "none"), bytes.NewReader(sealed),
		func(string, []byte) error { return nil }); err == nil {
		t.Fatal("opened without the identity")
	}
}

// With --secrets the archive holds secrets.age, the manifest lists nothing
// left out for them, and no member's bytes hold the clear password.
func TestRunWithSecrets(t *testing.T) {
	age, _ := fakeAge(t)
	loc, _ := home(t)
	os.Chmod(loc.PasswordFile, 0o600)
	os.Chmod(loc.EnvFile("demo"), 0o600)
	os.Chmod(filepath.Join(loc.ConfigDir, "notify.toml"), 0o600)
	p := plan(t, "demo")
	var err error
	if p.Files, _, p.Notes, err = Gather([]string{"demo"}, loc); err != nil {
		t.Fatal(err)
	}
	p.LeftOut = nil
	if p.Secrets, err = SealSecrets(context.Background(), age, testRecipient, Secrets([]string{"demo"}, loc)); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(res.Path)
	names, got := members(t, b)
	if !strings.Contains(strings.Join(names, ","), SecretsName) {
		t.Fatalf("members %q", names)
	}
	if c, err := ReadCoverage(bytes.NewReader(b)); err != nil || !c.Secrets {
		t.Fatalf("coverage %+v, %v: secrets not said", c, err)
	}
	for n, c := range got {
		if strings.Contains(c, "pw-SECRET") {
			t.Fatalf("%s holds the clear password", n)
		}
	}
	if _, err := Verify(bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
}

// Restore opens secrets.age only with --identity, writes each secret 0600,
// the password file only when no project is named.
func TestRestoreSecrets(t *testing.T) {
	age, _ := fakeAge(t)
	identity := filepath.Join(t.TempDir(), "key.txt")
	os.WriteFile(identity, []byte("AGE-SECRET-KEY-1..."), 0o600)
	srcDir := t.TempDir()
	pw := filepath.Join(srcDir, "redis.env")
	env := filepath.Join(srcDir, "demo.env")
	os.WriteFile(pw, []byte(`REDIS_PASSWORD="pw-SECRET"`+"\n"), 0o600)
	os.WriteFile(env, []byte("REDIS_URL=redis://:pw-SECRET@h\n"), 0o600)
	nt := filepath.Join(srcDir, "notify.toml")
	os.WriteFile(nt, []byte("[ntfy.phone]\ntopic = \"pw-SECRET-topic\"\n"), 0o600)
	sealed, err := SealSecrets(context.Background(), age, testRecipient,
		[]Source{{Member: "redis.env", Path: pw}, {Member: "notify.toml", Path: nt}, {Member: "services/demo.env", Path: env}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		o        RestoreOptions
		pw, demo bool
	}{
		{"no identity", RestoreOptions{}, false, false},
		{"identity", RestoreOptions{Identity: identity}, true, true},
		{"identity, scoped", RestoreOptions{Identity: identity, Projects: []string{"demo"}}, false, true},
	} {
		w := newRestoreWorld(t)
		p := plan(t, "demo")
		p.Secrets = sealed
		res, err := Run(context.Background(), t.TempDir(), p)
		if err != nil {
			t.Fatal(err)
		}
		w.archive = res.Path
		w.loc.PasswordFile = filepath.Join(w.loc.ConfigDir, "redis.env")
		var out bytes.Buffer
		_, err = Restore(context.Background(), w.archive, c.o, RestoreDeps{Broker: w.broker,
			Live:    func(context.Context, string) (string, error) { return "", nil },
			Lock:    func(string) (func(), error) { return func() {}, nil },
			Confirm: func(string, int) bool { return true }, Loc: w.loc, Age: age}, &out)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		check := func(path string, want bool) {
			st, err := os.Stat(path)
			switch {
			case want && (err != nil || st.Mode().Perm() != 0o600 || !strings.Contains(read(t, path), "pw-SECRET")):
				t.Errorf("%s: %s not restored 0600: %v\n%s", c.name, path, err, out.String())
			case !want && err == nil:
				t.Errorf("%s: %s written", c.name, path)
			}
		}
		check(w.loc.PasswordFile, c.pw)
		check(filepath.Join(w.loc.ConfigDir, "notify.toml"), c.pw) // the host's, like the password file
		check(w.loc.EnvFile("demo"), c.demo)
		if c.o.Identity == "" && !strings.Contains(out.String(), "no --identity") {
			t.Errorf("%s: output %q", c.name, out.String())
		}
	}
}

// With the real age and age-keygen on PATH, a seal opens with its key only.
func TestSecretsWithRealAge(t *testing.T) {
	age, err1 := exec.LookPath("age")
	keygen, err2 := exec.LookPath("age-keygen")
	if err1 != nil || err2 != nil {
		t.Skip("age or age-keygen not on PATH")
	}
	key := filepath.Join(t.TempDir(), "key.txt")
	if out, err := exec.Command(keygen, "-o", key).CombinedOutput(); err != nil {
		t.Fatalf("age-keygen: %v %s", err, out)
	}
	pub, err := exec.Command(keygen, "-y", key).Output()
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "redis.env")
	os.WriteFile(src, []byte("REDIS_PASSWORD=pw-SECRET\n"), 0o600)
	sealed, err := SealSecrets(context.Background(), age, strings.TrimSpace(string(pub)), []Source{{Member: "redis.env", Path: src}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("pw-SECRET")) {
		t.Fatal("clear text in the ciphertext")
	}
	var got string
	if err := OpenSecrets(context.Background(), age, key, bytes.NewReader(sealed), func(_ string, b []byte) error { got = string(b); return nil }); err != nil {
		t.Fatal(err)
	}
	if got != "REDIS_PASSWORD=pw-SECRET\n" {
		t.Fatalf("opened %q", got)
	}
}

// reviewer-codex: a secret over the cap is refused whole, never cut, and
// nothing is handed over (no earlier member written either); a sealed
// secret over the cap is refused at backup time.
func TestSecretsOverTheCap(t *testing.T) {
	age, _ := fakeAge(t)
	dir := t.TempDir()
	small := filepath.Join(dir, "redis.env")
	big := filepath.Join(dir, "big.env")
	os.WriteFile(small, []byte("REDIS_PASSWORD=pw\n"), 0o600)
	os.WriteFile(big, bytes.Repeat([]byte("x"), secretMax+1), 0o600)
	if _, err := SealSecrets(context.Background(), age, testRecipient, []Source{{Member: "services/demo.env", Path: big}}); err == nil {
		t.Fatal("an oversized secret was sealed")
	}
	// Build the sealed tar by hand, bypassing SealSecrets' check.
	var tarBuf bytes.Buffer
	writeSecretsTarNoCap(t, &tarBuf, map[string][]byte{"redis.env": []byte("REDIS_PASSWORD=pw\n"), "services/demo.env": bytes.Repeat([]byte("x"), secretMax+1)})
	cmd := exec.Command(age, "-r", testRecipient)
	cmd.Stdin = &tarBuf
	sealed, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(dir, "key.txt")
	os.WriteFile(identity, []byte("k"), 0o600)
	called := 0
	err = OpenSecrets(context.Background(), age, identity, bytes.NewReader(sealed), func(string, []byte) error { called++; return nil })
	if err == nil || called != 0 {
		t.Fatalf("an oversized secret: err %v, %d handed over", err, called)
	}
}

func writeSecretsTarNoCap(t *testing.T, w io.Writer, files map[string][]byte) {
	t.Helper()
	tw := tar.NewWriter(w)
	for _, name := range []string{"redis.env", "services/demo.env"} {
		b := files[name]
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg})
		tw.Write(b)
	}
	tw.Close()
}

// reviewer-codex: a secrets tar holding anything but regular files named
// redis.env, notify.toml or services/<project>.env is refused whole before any is
// handed over, so --force can never write an empty or foreign secret.
func TestOpenSecretsRefusesUnexpectedEntries(t *testing.T) {
	age, _ := fakeAge(t)
	identity := filepath.Join(t.TempDir(), "key.txt")
	os.WriteFile(identity, []byte("k"), 0o600)
	for name, hdrs := range map[string][]tar.Header{
		"a symlink":      {{Name: "redis.env", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		"a hard link":    {{Name: "redis.env", Typeflag: tar.TypeLink, Linkname: "x"}},
		"a directory":    {{Name: "services/", Typeflag: tar.TypeDir, Mode: 0o700}},
		"a device":       {{Name: "redis.env", Typeflag: tar.TypeChar}},
		"a foreign name": {{Name: "../.bashrc", Typeflag: tar.TypeReg, Size: 1}},
		"a bad project":  {{Name: "services/Bad.env", Typeflag: tar.TypeReg, Size: 1}},
		"after a good one": {{Name: "redis.env", Typeflag: tar.TypeReg, Size: 1},
			{Name: "services/demo.env", Typeflag: tar.TypeSymlink, Linkname: "/x"}},
	} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range hdrs {
			h.Mode = 0o600
			tw.WriteHeader(&h)
			if h.Size > 0 {
				tw.Write([]byte("x"))
			}
		}
		tw.Close()
		cmd := exec.Command(age, "-r", testRecipient)
		cmd.Stdin = &buf
		sealed, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		called := 0
		err = OpenSecrets(context.Background(), age, identity, bytes.NewReader(sealed), func(string, []byte) error { called++; return nil })
		if err == nil || called != 0 {
			t.Errorf("%s: err %v, %d handed over", name, err, called)
		}
	}
}
