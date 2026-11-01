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
