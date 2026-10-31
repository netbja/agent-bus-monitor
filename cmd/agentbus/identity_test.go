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
	}, filepath.Join(dir, "agentbus", "config.toml")
}

func TestIdentitySetCreatesThePrivateFile(t *testing.T) {
	env, path := identityEnv(t)
	var out, errw bytes.Buffer
	if code := runIdentity(nil, env, &out, &errw); code != 1 {
		t.Fatalf("unset: code %d", code)
	}
	if code := runIdentity([]string{"set", "charlie"}, env, &out, &errw); code != 0 {
		t.Fatalf("set: %d %s", code, errw.String())
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", st, err)
	}
	out.Reset()
	if code := runIdentity(nil, env, &out, &errw); code != 0 || strings.TrimSpace(out.String()) != "charlie" {
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
	if code := runIdentity([]string{"set", "charlie"}, env, &out, &errw); code != 0 {
		t.Fatalf("set: %d %s", code, errw.String())
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), orig) || !strings.Contains(string(b), "[identity]\nas = \"charlie\"") {
		t.Fatalf("file:\n%s", b)
	}
	if code := runIdentity([]string{"set", "other"}, env, &out, &errw); code != 1 {
		t.Fatalf("a recorded name was replaced: %d", code)
	}
}

func TestIdentityRefusesBadNamesAndBrokenFiles(t *testing.T) {
	env, path := identityEnv(t)
	var out, errw bytes.Buffer
	if code := runIdentity([]string{"set", "Charlie J"}, env, &out, &errw); code != 1 {
		t.Fatalf("bad name: %d", code)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[waiting\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runIdentity([]string{"set", "charlie"}, env, &out, &errw); code != 1 {
		t.Fatalf("broken file: %d", code)
	}
	if b, _ := os.ReadFile(path); string(b) != "[waiting\n" {
		t.Fatalf("a broken file was touched: %q", b)
	}
}

func TestUpDefaultsAsToTheRecordedName(t *testing.T) {
	work, _, _, boot, deps := upFixture(t)
	env, _ := identityEnv(t)
	if code := runIdentity([]string{"set", "charlie"}, env, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
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
	if len(*boot) < 2 || (*boot)[0] != "demo "+work+" --as charlie" || !strings.HasSuffix((*boot)[len(*boot)-1], "--as pilot") ||
		strings.Contains((*boot)[len(*boot)-1], "charlie") {
		t.Fatalf("bootstrap calls = %q", *boot)
	}
}

// A name in busmon.toml is still read; set records the new one in config.toml,
// which wins, and says the old one is shadowed.
func TestIdentitySetShadowsBusmonTOML(t *testing.T) {
	env, path := identityEnv(t)
	legacy := filepath.Join(filepath.Dir(path), "busmon.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("[identity]\nas = \"oldname\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := runIdentity(nil, env, &out, &errw); code != 0 || strings.TrimSpace(out.String()) != "oldname" || !strings.Contains(errw.String(), "busmon.toml") {
		t.Fatalf("legacy read: %d %q %q", code, out.String(), errw.String())
	}
	out.Reset()
	if code := runIdentity([]string{"set", "charlie"}, env, &out, &errw); code != 0 || !strings.Contains(out.String(), "recorded charlie in config.toml; busmon.toml's name oldname is now shadowed") {
		t.Fatalf("set: %d %q %q", code, out.String(), errw.String())
	}
	if b, _ := os.ReadFile(legacy); string(b) != "[identity]\nas = \"oldname\"\n" {
		t.Fatalf("busmon.toml was touched: %q", b)
	}
	out.Reset()
	errw.Reset()
	if code := runIdentity(nil, env, &out, &errw); code != 0 || strings.TrimSpace(out.String()) != "charlie" || !strings.Contains(errw.String(), "config.toml") {
		t.Fatalf("after set: %d %q %q", code, out.String(), errw.String())
	}
	// A config.toml with [identity] but no as refuses, as busmon.toml did.
	env2, path2 := identityEnv(t)
	if err := os.MkdirAll(filepath.Dir(path2), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path2, []byte("[identity]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runIdentity([]string{"set", "charlie"}, env2, &out, &errw); code != 1 {
		t.Fatalf("an [identity] section without as: %d", code)
	}
}

// up's default --as reads config.toml before busmon.toml.
func TestUpDefaultAsPrefersConfigTOML(t *testing.T) {
	env, path := identityEnv(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(filepath.Dir(path), "busmon.toml"), []byte("[identity]\nas = \"oldname\"\n"), 0o600)
	os.WriteFile(path, []byte("[identity]\nas = \"charlie\"\n"), 0o600)
	if got := settingsIdentity(env); got != "charlie" {
		t.Fatalf("settingsIdentity = %q, want charlie", got)
	}
}
