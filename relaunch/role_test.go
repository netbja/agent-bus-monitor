package relaunch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTemplate(t *testing.T) {
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "agentbus", "projects")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "demo.toml"), []byte("name = \"demo\"\nworking_dir = \"~/src\"\n[[tabs]]\nname = \"coder\"\ncommand = \"agent-launch coder demo\"\n"), 0o644)
	env := envOf(map[string]string{"XDG_CONFIG_HOME": cfg})
	tpl, err := ReadTemplate(env, "demo")
	if err != nil || tpl.WorkingDir != "~/src" || len(tpl.Tabs) != 1 || tpl.Tabs[0].Name != "coder" {
		t.Fatalf("%+v %v", tpl, err)
	}
	if _, err := ReadTemplate(env, "other"); err == nil {
		t.Fatal("a missing template read")
	}
}

func TestCacheDir(t *testing.T) {
	if got := CacheDir(envOf(map[string]string{"XDG_CACHE_HOME": "/x", "HOME": "/h"})); got != "/x/agentbus" {
		t.Fatal(got)
	}
	if got := CacheDir(envOf(map[string]string{"HOME": "/h"})); got != "/h/.cache/agentbus" {
		t.Fatal(got)
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
