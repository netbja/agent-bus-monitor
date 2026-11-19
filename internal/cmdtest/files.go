package cmdtest

import (
	"os"
	"path/filepath"
	"testing"
)

func Write(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// WithConfig is a getenv whose config home holds config.toml = body, plus env.
func WithConfig(t *testing.T, body string, env map[string]string) func(string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agentbus", "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := map[string]string{"XDG_CONFIG_HOME": dir}
	for k, v := range env {
		m[k] = v
	}
	return func(k string) string { return m[k] }
}
