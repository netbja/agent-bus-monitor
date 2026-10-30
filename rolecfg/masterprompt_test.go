package rolecfg

import (
	"os"
	"strings"
	"testing"
)

// master-human-reply: a person reads busmon, not master's pane, so master's prompt and the
// master skill both tell it to answer every message a person sent from busmon ("human": true)
// with a report addressed to that person. The rule is every message: the paragraph that states it
// offers no `reply --ref` alternative in either file, so prompt and skill cannot disagree
// (reviewer-codex).
func TestMasterAnswersPeopleOnTheBus(t *testing.T) {
	for _, path := range []string{"roles/master.md", "skills/agent-bus-master/SKILL.md"} {
		b, err := os.ReadFile(repoPath(t, path))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(b)), " ")
		for _, para := range strings.Split(string(b), "\n\n") {
			if strings.Contains(para, `"human": true`) && strings.Contains(para, "reply --ref") {
				t.Errorf("%s: the paragraph on a person's message offers reply --ref instead of the report", path)
			}
		}
		for _, want := range []string{`"human": true`, "agentbus report master --to <person>", "not only in your pane"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", path, want)
			}
		}
	}
}
