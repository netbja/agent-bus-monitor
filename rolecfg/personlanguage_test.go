package rolecfg

import (
	"os"
	"strings"
	"testing"
)

// person-language (spec 2026-11-18-language-setting-design.md §6): master's
// skill is the one reader the registry's ReadBy names for person.language, so
// it must name the key, say how it reads it, and carry the rule: the person
// in their language, every agent in English, the person's words relayed in
// English. The role file reads it at boot; the peer skill states the bus rule.
func TestMasterReadsThePersonsLanguage(t *testing.T) {
	read := func(path string) string {
		b, err := os.ReadFile(repoPath(t, path))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(b)), " ")
	}
	skill := read("skills/agent-bus-master/SKILL.md")
	for _, want := range []string{"## The person's language", "person.language", "agentbus config",
		"to every other agent in English", "Never quote the person's language to another agent", "choice labels stay English"} {
		if !strings.Contains(skill, want) {
			t.Errorf("master's skill lacks %q", want)
		}
	}
	if role := read("roles/master.md"); !strings.Contains(role, "person.language") {
		t.Error("roles/master.md: boot does not read person.language")
	}
	// The person's answer to §7 Q1: a peer answering the person directly stays English;
	// only master writes to the person in person.language.
	peer := read("skills/agent-bus/SKILL.md")
	if !strings.Contains(peer, "## Language on the bus") {
		t.Error("the peer skill lacks its 'Language on the bus' section")
	}
	for _, want := range []string{"Write in English", "answering the person directly", "Only master writes to the person in `person.language`"} {
		if !strings.Contains(peer, want) {
			t.Errorf("the peer skill lacks %q", want)
		}
	}
}
