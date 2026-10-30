package bus

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// redis-password: the broker's password lives in one file the person owns
// ($XDG_CONFIG_HOME/agentbus/redis.env, else ~/.config/agentbus/redis.env),
// written 0600 in a 0700 directory, one REDIS_PASSWORD="..." line that
// compose's env_file reads too.

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPasswordFilePath(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"XDG_CONFIG_HOME": "/x/cfg", "HOME": "/h"}, "/x/cfg/agentbus/redis.env"},
		{map[string]string{"HOME": "/h"}, "/h/.config/agentbus/redis.env"},
		{map[string]string{}, ""}, // nowhere to look: no file, never the working directory
		{map[string]string{"XDG_CONFIG_HOME": "rel", "HOME": "/h"}, "/h/.config/agentbus/redis.env"}, // a relative XDG dir is ignored
	} {
		if got := PasswordFile(envOf(c.env)); got != c.want {
			t.Errorf("PasswordFile(%v) = %q, want %q", c.env, got, c.want)
		}
	}
}

func TestPasswordFileWriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "agentbus", "redis.env")
	if _, err := ReadPassword(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no file: %v, want not-exist", err)
	}
	if err := WritePassword(path, "s3cr-et_X"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v (%v), want 0600", st.Mode().Perm(), err)
	}
	if dir, _ := os.Stat(filepath.Dir(path)); dir.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v, want 0700", dir.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "REDIS_PASSWORD=\"s3cr-et_X\"\n") {
		t.Fatalf("file content is not compose's KEY=\"value\" line:\n%s", data)
	}
	if pw, err := ReadPassword(path); err != nil || pw != "s3cr-et_X" {
		t.Fatalf("read back %q, %v", pw, err)
	}
	// Replacing it leaves no temporary file behind.
	if err := WritePassword(path, "other"); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(filepath.Dir(path)); len(ents) != 1 {
		t.Fatalf("%d entries beside the file, want only it", len(ents))
	}
}

func TestReadPasswordRefusesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		body, want string
		ok         bool
	}{
		{"# agentbus\nREDIS_PASSWORD=\"abc\"\n", "abc", true},
		{"REDIS_PASSWORD=abc\n", "abc", true}, // unquoted, as compose accepts
		{"REDIS_PASSWORD=\"\"\n", "", false},  // empty: never a password
		{"OTHER=1\n", "", false},              // no password line
		{"REDIS_PASSWORD=\"abc\n", "", false}, // broken quoting
	} {
		path := filepath.Join(dir, "redis.env")
		if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		pw, err := ReadPassword(path)
		if (err == nil) != c.ok || pw != c.want {
			t.Errorf("%q: %q, %v; want %q ok=%v", c.body, pw, err, c.want, c.ok)
		}
		if err != nil && strings.Contains(err.Error(), "abc") {
			t.Errorf("%q: the error discloses the value: %v", c.body, err)
		}
	}
}

// The credential Connect uses: the environment's REDIS_PASSWORD, else the
// file, else the historical default. A file that exists but cannot be read
// is an error, never a silent fallback to the default.
func TestResolvePassword(t *testing.T) {
	cfg := t.TempDir()
	path := filepath.Join(cfg, "agentbus", "redis.env")
	env := map[string]string{"XDG_CONFIG_HOME": cfg}
	if pw, err := resolvePassword(envOf(env)); err != nil || pw != LegacyPassword {
		t.Fatalf("no file: %q, %v", pw, err)
	}
	if err := WritePassword(path, "from-file"); err != nil {
		t.Fatal(err)
	}
	if pw, err := resolvePassword(envOf(env)); err != nil || pw != "from-file" {
		t.Fatalf("file: %q, %v", pw, err)
	}
	env["REDIS_PASSWORD"] = "from-env"
	if pw, err := resolvePassword(envOf(env)); err != nil || pw != "from-env" {
		t.Fatalf("env over file: %q, %v", pw, err)
	}
	delete(env, "REDIS_PASSWORD")
	if err := os.WriteFile(path, []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pw, err := resolvePassword(envOf(env)); err == nil || pw != "" {
		t.Fatalf("unreadable file: %q, %v; want an error", pw, err)
	}
}

func TestNewPassword(t *testing.T) {
	a, err := NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewPassword()
	if a == b || len(a) < 40 {
		t.Fatalf("passwords %q %q: want two different, >= 40 characters", a, b)
	}
	if strings.ContainsAny(a, "\"\\$' \n=") {
		t.Fatalf("%q holds a character that needs quoting in a config or env file", a)
	}
}

// The health check reads the file too: a generated password there is not the
// legacy one; the legacy one written there is.
func TestLegacyPasswordInUseReadsTheFile(t *testing.T) {
	cfg := t.TempDir()
	path := filepath.Join(cfg, "agentbus", "redis.env")
	env := map[string]string{"XDG_CONFIG_HOME": cfg}
	if !LegacyPasswordInUse(envOf(env)) {
		t.Fatal("no env, no file: legacy")
	}
	if err := WritePassword(path, "generated"); err != nil {
		t.Fatal(err)
	}
	if LegacyPasswordInUse(envOf(env)) {
		t.Fatal("a generated password in the file read as legacy")
	}
	if err := WritePassword(path, LegacyPassword); err != nil {
		t.Fatal(err)
	}
	if !LegacyPasswordInUse(envOf(env)) {
		t.Fatal("the legacy password in the file not recognised")
	}
	env["REDIS_PASSWORD"] = "exported"
	if LegacyPasswordInUse(envOf(env)) {
		t.Fatal("an exported password over the file read as legacy")
	}
}
