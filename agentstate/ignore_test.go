package agentstate

import (
	"testing"
	"time"
)

// dispatch.background_ignore: a process whose name is listed is never
// background work, whatever its CPU and wherever it runs (under a shell, or
// its branch unknown): the client's own helpers (codebase-memory,
// codex-code-mode, MainThread, import) held a hand-off's /clear and a pop's
// retire. A real command (go test, a dev server) still counts.
func TestJudgeIgnoresListedProcesses(t *testing.T) {
	now := time.Unix(1000000, 0)
	ignore := []string{"codebase-memory", "codex-code-mode", "MainThread", "import"}
	helpers := []Proc{
		{Name: "codebase-memory", Branch: "bash", CPUGrowth: 9 * time.Second},
		{Name: "codex-code-mode", CPUGrowth: 9 * time.Second},
		{Name: "MainThread", Branch: "bash", CPUGrowth: 9 * time.Second},
		{Name: "import", CPUGrowth: 9 * time.Second},
	}
	claude := Background{Authoritative: []string{SourceHooks, SourceProcess}, DirectHelpers: true}
	codex := Background{Authoritative: []string{SourceProcess}}
	for name, bg := range map[string]Background{"claude": claude, "codex": codex} {
		f := Facts{Hooks: &HookFacts{}, Process: &ProcessFacts{Window: 10 * time.Second, Procs: helpers}, Ignore: ignore}
		if j := bg.Judge(f, now); j.Blocks() || j.Procs() != 0 {
			t.Errorf("%s, helpers only: %v %q", name, j.State, j.What())
		}
		f.Process = &ProcessFacts{Window: 10 * time.Second, Procs: append(append([]Proc(nil), helpers...),
			Proc{Name: "go", Branch: "bash", CPUGrowth: 9 * time.Second})}
		if j := bg.Judge(f, now); !j.Blocks() || j.Procs() != 1 || j.What() != "process 'go'" {
			t.Errorf("%s, a go test among them: %v %d %q", name, j.State, j.Procs(), j.What())
		}
		f.Ignore = nil
		f.Process = &ProcessFacts{Window: 10 * time.Second, Procs: helpers}
		if j := bg.Judge(f, now); !j.Blocks() {
			t.Errorf("%s, no list: the helpers under bash should still count, %v", name, j.State)
		}
	}
}
