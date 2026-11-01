package rolecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The state rule (auto-state spec, part 4) is written where every agent
// reads it: every role prompt, the infra draft the adopted prompt is held to,
// and the shared agent-bus skill, each word for word.
func TestEveryPromptCarriesTheStateRule(t *testing.T) {
	prompts, err := filepath.Glob(repoPath(t, "roles/*.md"))
	if err != nil || len(prompts) == 0 {
		t.Fatalf("no role prompts: %v", err)
	}
	files := append(prompts, repoPath(t, "examples/drafts/infra/infra.md"), repoPath(t, "skills/agent-bus/SKILL.md"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(strings.Fields(string(b)), " "), StateRule) {
			t.Errorf("%s lacks the state rule", f)
		}
	}
	for _, must := range []string{"`working` at the start of each step or fix", "`idle` while", "`waiting` only when a person must decide",
		"follows your turns", "only you lift `waiting`"} {
		if !strings.Contains(StateRule, must) {
			t.Errorf("the rule lacks %q", must)
		}
	}
}
