package peer

import (
	"os"
	"path/filepath"
	"testing"
)

// EndpointHost is an endpoint's host and port, never its path or query:
// what agentbus config shows of it (dispatchsvc P6, moved from cmd/agentbus
// peer.go's endpointHost).
func TestEndpointHost(t *testing.T) {
	for raw, want := range map[string]string{
		"http://127.0.0.1:11434/v1":          "127.0.0.1:11434",
		"https://user:pw@api.example/v1?k=s": "api.example",
		"%zz":                                "?",
	} {
		if got := EndpointHost(raw); got != want {
			t.Errorf("EndpointHost(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The fingerprint key lives beside config.toml; with no configuration
// directory there is no key, and none is written in the current folder.
func TestEndpointFPKeyNeedsTheConfigDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	if _, _, err := EndpointFPKey(func(string) string { return "" }); err == nil {
		t.Fatal("a key with no configuration directory")
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Fatalf("written in the cwd: %v", entries)
	}
	xdg := t.TempDir()
	if key, _, err := EndpointFPKey(func(k string) string { return map[string]string{"XDG_CONFIG_HOME": xdg}[k] }); err != nil || len(key) != 32 {
		t.Fatalf("key = %d bytes, %v", len(key), err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "agentbus", "endpoint-fp.key")); err != nil {
		t.Fatal(err)
	}
}
