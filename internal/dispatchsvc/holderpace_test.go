package dispatchsvc

import (
	"path/filepath"
	"testing"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/selfrepair"
)

// H3: holder_restarted names self-repair's queue only when self-repair
// relaunches (on, the default); off or shadow relaunch nothing.
func TestHolderRelaunchPace(t *testing.T) {
	env := func(mode string) func(string) string {
		home := t.TempDir()
		return func(k string) string {
			switch k {
			case "HOME":
				return home
			case "XDG_CONFIG_HOME":
				return filepath.Join(home, ".config")
			case config.EnvName("dispatch.self_repair"):
				return mode
			}
			return ""
		}
	}
	if got := holderRelaunchPace(env("")); got != selfrepair.ProjectPause {
		t.Errorf("default (on): %s", got)
	}
	for _, mode := range []string{"off", "shadow", "bogus"} {
		if got := holderRelaunchPace(env(mode)); got != 0 {
			t.Errorf("%s: %s", mode, got)
		}
	}
}
