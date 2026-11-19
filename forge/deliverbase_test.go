package forge

import (
	"os"
	"path/filepath"
	"testing"
)

// missions and config read one resolution: the template, then the machine.
func TestProjectDeliveryBase(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"AGENTBUS_PROJECTS_DIR": dir, "XDG_CONFIG_HOME": t.TempDir(), "HOME": t.TempDir()}
	get := func(k string) string { return env[k] }
	if b, src, err := ProjectDeliveryBase(get, "demo"); err != nil || b != "main" || src != "default" {
		t.Fatalf("no template: %q %q %v", b, src, err)
	}
	env["AGENT_BUS_DELIVERY_BASE"] = "trunk"
	if b, src, _ := ProjectDeliveryBase(get, "demo"); b != "trunk" || src != "env AGENT_BUS_DELIVERY_BASE" {
		t.Fatalf("machine env: %q %q", b, src)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.toml"), []byte("[delivery]\nbase = \"dev\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, src, _ := ProjectDeliveryBase(get, "demo"); b != "dev" || src != "template "+filepath.Join(dir, "demo.toml") {
		t.Fatalf("template: %q %q", b, src)
	}
}
