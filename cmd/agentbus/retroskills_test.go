package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// retro build step 3 (self-improving loop §8): the skills carry the retro's
// rules. The agent-bus skill the retro itself (§4), the master skill its
// routing (§6), the docs skill the apply steps (§6). Each names only the
// retro verbs agentbus has.
func TestSkillsCarryTheRetro(t *testing.T) {
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	section := func(t *testing.T, file, heading string) string {
		t.Helper()
		s := read(file)
		i := strings.Index(s, "\n"+heading)
		if i < 0 {
			t.Fatalf("%s has no %q section", file, heading)
		}
		s = s[i+1:]
		if j := strings.Index(s[len(heading):], "\n## "); j >= 0 {
			s = s[:len(heading)+j]
		}
		return s
	}
	for _, c := range []struct {
		file, heading string
		want          []string
	}{
		{"skills/agent-bus/SKILL.md", "## A retro after a task with friction", []string{
			"retro-<task>", "agentbus retro evidence <task>", "root cause", "at most three", "n | lesson | evidence | destination | file | change",
			"bus id", "read the target file", "an existing skill", "a context file", "new skill", "edit an existing rule",
			"agentbus retro rejected --file", "agentbus retro propose <task> --file", "agentbus retro none <task>",
			"agentbus request done retro-<task>", "never inferred from silence", "queued", "never a quote", "capped",
		}},
		{"skills/agent-bus-master/SKILL.md", "## Retros: route each one when its ask closes", []string{
			"retro 1 of N", "agentbus retro route <task>", "agentbus retro withdraw <task>", "retro-apply-<task>",
			"whatever the answers", "never an approval", "pop the role again", "agentbus retro request",
		}},
		{"skills/agent-bus-docs/SKILL.md", "## Applying a retro (`retro-apply-<task>`)", []string{
			"agentbus retro show <task>", "accepted", "blob", "conflict", "report docs --to master",
			"retro/<task>", "Retro: <task> #<n>", "Approved-By: <entry id>", "docs/memory/retros/<date>-<task>.md",
			"agentbus retro close <task> --pr <n>", "only the record", "never from memory",
		}},
	} {
		s := strings.ToLower(strings.Join(strings.Fields(section(t, c.file, c.heading)), " ")) // a phrase may wrap
		for _, w := range c.want {
			if !strings.Contains(s, strings.ToLower(w)) {
				t.Errorf("%s %q lacks %q", c.file, c.heading, w)
			}
		}
	}
	// Every retro verb a skill names is one agentbus has (retroUsage).
	verbs := regexp.MustCompile(`agentbus retro ([a-z]+)`)
	for _, f := range []string{"skills/agent-bus/SKILL.md", "skills/agent-bus-master/SKILL.md", "skills/agent-bus-docs/SKILL.md"} {
		for _, m := range verbs.FindAllStringSubmatch(read(f), -1) {
			if !strings.Contains(retroUsage, "retro "+m[1]+" ") {
				t.Errorf("%s names agentbus retro %s, which agentbus has not", f, m[1])
			}
		}
	}
}
