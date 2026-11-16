package config

import (
	"os"
	"path/filepath"
	"testing"
)

// display.date_format (quota settings §2.3): relative, iso or clock12, from
// config.toml only (no environment variable: a service reads the file as it
// is now, never a start-time copy); any other value is refused.
func TestDateFormatKey(t *testing.T) {
	k, ok := Lookup("display.date_format")
	if !ok || k.Default != "relative" || k.Env != "" || k.Group() != GroupDisplay {
		t.Fatalf("key %+v ok %v", k, ok)
	}
	for _, v := range []string{"relative", "iso", "clock12"} {
		if got, why := k.file(v); got != v || why != "" {
			t.Errorf("%s: %v %q", v, got, why)
		}
	}
	if _, why := k.file("long"); why == "" {
		t.Error("an unknown format accepted")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agentbus", "config.toml"), []byte("[display]\ndate_format = \"iso\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	}
	if got := Machine(env).String("display.date_format"); got != "iso" {
		t.Fatalf("read %q", got)
	}
}
