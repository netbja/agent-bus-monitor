package dispatchsvc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/holder"
)

// agent-resource-limits step 3e: each answering build's unit cap and
// terminals, by key; a build that does not answer is left out, one without
// a cgroup has no cap.
func TestHolderCaps(t *testing.T) {
	run := t.TempDir()
	if err := os.Chmod(run, 0o700); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return run
		}
		return ""
	}
	dir, err := holder.ProjectDir(getenv, "demo")
	if err != nil {
		t.Fatal(err)
	}
	const capped, bare, dead = "v0.34.0.0123456789", "v0.33.0.0123456789", "v0.32.0.0123456789"
	for _, k := range []string{capped, bare, dead} {
		if err := os.WriteFile(filepath.Join(dir, "holder-"+k+".sock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	describe := func(project, key string) (*holder.Info, error) {
		if project != "demo" {
			t.Fatalf("asked project %q", project)
		}
		switch key {
		case capped:
			return &holder.Info{Terms: 4, Memory: &holder.UnitMemory{Current: 1, Max: 40 << 30}}, nil
		case bare:
			return &holder.Info{Terms: 2}, nil
		}
		return nil, errors.New("no answer")
	}
	caps, err := holderCaps(getenv, "demo", describe)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]dispatch.HolderCap{capped: {Max: 40 << 30, Terms: 4}, bare: {Terms: 2}}
	if len(caps) != len(want) || caps[capped] != want[capped] || caps[bare] != want[bare] {
		t.Fatalf("caps %+v, want %+v", caps, want)
	}
}
