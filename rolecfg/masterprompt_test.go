package rolecfg

import (
	"os"
	"strings"
	"testing"
)

// master-human-reply: a person reads busmon, not master's pane, so master's prompt and the
// master skill both tell it to answer every message a person sent from busmon ("human": true)
// with a report addressed to that person.
func TestMasterAnswersPeopleOnTheBus(t *testing.T) {
	for _, path := range []string{"roles/master.md", "skills/agent-bus-master/SKILL.md"} {
		b, err := os.ReadFile(repoPath(t, path))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(b)), " ")
		for _, want := range []string{`"human": true`, "agentbus report master --to <person>", "not only in your pane"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", path, want)
			}
		}
	}
}
