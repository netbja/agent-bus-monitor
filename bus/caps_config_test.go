package bus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/config"
)

// The writers' caps follow config.toml below the env; a test binary reads no
// file unless it says so, so the person's own file never changes a test.
func TestCapsReadConfigTOML(t *testing.T) {
	if len(capFiles()) != 0 {
		t.Fatal("a test binary read a settings file")
	}
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[bus]\nreport_max = 120\nreport_full_max = 900\nverdict_max = 50\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := capFiles
	capFiles = func() []*config.File { return []*config.File{config.Load(p, "config.toml")} }
	t.Cleanup(func() { capFiles = old })
	if reportMaxLen() != 120 || reportFullMax() != 900 || verdictMaxLen() != 50 {
		t.Fatalf("caps = %d %d %d", reportMaxLen(), reportFullMax(), verdictMaxLen())
	}
	t.Setenv("AGENT_BUS_REPORT_MAX", "300")
	t.Setenv("AGENT_BUS_VERDICT_MAX", "0") // ignored, as today: the file applies
	if reportMaxLen() != 300 || verdictMaxLen() != 50 {
		t.Fatalf("env over file: %d %d", reportMaxLen(), verdictMaxLen())
	}
}

// The real branch reads the process's config.toml through XDG_CONFIG_HOME.
func TestCapFilesRealBranch(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agentbus", "config.toml"), []byte("[bus]\nreport_max = 77\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	old := inTestBinary
	inTestBinary = func() bool { return false }
	t.Cleanup(func() { inTestBinary = old })
	if got := reportMaxLen(); got != 77 {
		t.Fatalf("reportMaxLen = %d, want 77 from config.toml", got)
	}
}
