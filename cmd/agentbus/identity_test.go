package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func identityEnv(t *testing.T) (func(string) string, string) {
	dir := t.TempDir()
	return func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	}, filepath.Join(dir, "agentbus", "busmon.toml")
}

func TestIdentitySetCreatesThePrivateFile(t *testing.T) {
	env, path := identityEnv(t)
	var out, errw bytes.Buffer
	if code := runIdentity(nil, env, &out, &errw); code != 1 {
		t.Fatalf("unset: code %d", code)
	}
	if code := runIdentity([]string{"set", "bernard"}, env, &out, &errw); code != 0 {
		t.Fatalf("set: %d %s", code, errw.String())
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", st, err)
	}
	out.Reset()
	if code := runIdentity(nil, env, &out, &errw); code != 0 || strings.TrimSpace(out.String()) != "bernard" {
		t.Fatalf("get: %d %q", code, out.String())
	}
}

func TestIdentitySetKeepsTheOtherSettings(t *testing.T) {
	env, path := identityEnv(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	orig := "# mine\n[waiting]\npopup_after = \"10m\"\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := runIdentity([]string{"set", "bernard"}, env, &out, &errw); code != 0 {
		t.Fatalf("set: %d %s", code, errw.String())
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), orig) || !strings.Contains(string(b), "[identity]\nas = \"bernard\"") {
		t.Fatalf("file:\n%s", b)
	}
	if code := runIdentity([]string{"set", "other"}, env, &out, &errw); code != 1 {
		t.Fatalf("a recorded name was replaced: %d", code)
	}
}

func TestIdentityRefusesBadNamesAndBrokenFiles(t *testing.T) {
	env, path := identityEnv(t)
	var out, errw bytes.Buffer
	if code := runIdentity([]string{"set", "Bernard J"}, env, &out, &errw); code != 1 {
		t.Fatalf("bad name: %d", code)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[waiting\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runIdentity([]string{"set", "bernard"}, env, &out, &errw); code != 1 {
		t.Fatalf("broken file: %d", code)
	}
	if b, _ := os.ReadFile(path); string(b) != "[waiting\n" {
		t.Fatalf("a broken file was touched: %q", b)
	}
}

func TestUpDefaultsAsToTheRecordedName(t *testing.T) {
	work, _, _, boot, deps := upFixture(t)
	env, _ := identityEnv(t)
	if code := runIdentity([]string{"set", "bernard"}, env, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("set failed")
	}
	base := deps.Env
	deps.Env = func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return env(k)
		}
		return base(k)
	}
	var out, errw bytes.Buffer
	runUp([]string{"demo", work}, deps, &out, &errw)
	runUp([]string{"demo", work, "--as", "pilot"}, deps, &out, &errw)
	if len(*boot) < 2 || (*boot)[0] != "demo "+work+" --as bernard" || !strings.HasSuffix((*boot)[len(*boot)-1], "--as pilot") ||
		strings.Contains((*boot)[len(*boot)-1], "bernard") {
		t.Fatalf("bootstrap calls = %q", *boot)
	}
}
