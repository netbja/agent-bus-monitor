package holder

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/termhost"
)

func writeCgroup(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The holder reads its own unit cgroup (agent-resource-limits §3.1): four
// small files, no process. Its leaf "holder" (cap B, step 4) reads as the
// unit; a holder outside a holder unit (supervised) reports nothing.
func TestUnitMemory(t *testing.T) {
	root := t.TempDir()
	unit := "/user.slice/user-1000.slice/user@1000.service/app.slice/agentbus-holder.demo.k1.service"
	writeCgroup(t, root+unit, map[string]string{
		"memory.current":      "38226833408\n",
		"memory.max":          "46950400000\n",
		"memory.swap.current": "1048576\n",
		"memory.events":       "low 0\nhigh 12\nmax 3\noom 7\noom_kill 2\noom_group_kill 9\n",
	})
	want := UnitMemory{Current: 38226833408, Max: 46950400000, SwapCurrent: 1048576, OOMKills: 2}
	for _, self := range []string{"0::" + unit + "\n", "0::" + unit + "/holder\n", "1:name=x:/y\n0::" + unit + "\n"} {
		got := unitMemory(self, root)
		if got == nil || *got != want {
			t.Errorf("unitMemory(%q) = %+v, want %+v", self, got, want)
		}
	}
	// No MemoryMax: "max" reads 0, no limit.
	other := "/user.slice/user-1000.slice/user@1000.service/app.slice/agentbus-holder.demo.k2.service"
	writeCgroup(t, root+other, map[string]string{"memory.current": "1000\n", "memory.max": "max\n", "memory.events": "oom_kill 0\n"})
	if got := unitMemory("0::"+other+"\n", root); got == nil || *got != (UnitMemory{Current: 1000}) {
		t.Errorf("no cap: %+v", got)
	}
	for _, self := range []string{
		"", // no cgroup (macOS)
		"0::/user.slice/user-1000.slice/session-2.scope\n",                                               // a supervised holder
		"0::" + unit + "/term-3/client\n",                                                                // never a terminal's cgroup
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/agentbus-holder.demo.gone.service\n", // unreadable
	} {
		if got := unitMemory(self, root); got != nil {
			t.Errorf("unitMemory(%q) = %+v, want none", self, got)
		}
	}
}

// The wire's ms and KiB become termhost's duration and bytes.
func TestProcsOf(t *testing.T) {
	got := procsOf([]ProcInfo{{PID: 7, PPID: 1, Start: 9, Command: "go", CPUms: 1500, RSSkb: 2048}})
	want := termhost.Proc{PID: 7, PPID: 1, Start: 9, Command: "go", CPU: 1500 * time.Millisecond, RSS: 2 << 20}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%+v", got)
	}
}
