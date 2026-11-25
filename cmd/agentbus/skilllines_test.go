package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var goTestWord = regexp.MustCompile(`\bgo test\b`)

// goTestCommands are the go test commands a document tells an agent to run:
// every line of a fenced code block, and, in a skill, every inline code
// span. Prose around a span is not a command (testing-redis.md's "Plain `go
// test` does not connect" stays out by scope: spans count in skills only).
func goTestCommands(text string, spans bool) []string {
	var out []string
	fenced := false
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			if goTestWord.MatchString(line) {
				out = append(out, strings.TrimSpace(line))
			}
			continue
		}
		if !spans {
			continue
		}
		parts := strings.Split(line, "`")
		for i := 1; i < len(parts); i += 2 {
			if goTestWord.MatchString(parts[i]) {
				out = append(out, parts[i])
			}
		}
	}
	return out
}

// cappedGoTest: agentbus run --cap comes before go test, and the run sets its
// own -timeout (agent-resource-limits §6: Go's 10m default per package
// binary is gone once a script passes -timeout 0).
func cappedGoTest(cmd string) bool {
	i, j := strings.Index(cmd, "agentbus run --cap"), goTestWord.FindStringIndex(cmd)
	return i >= 0 && j != nil && i < j[0] && strings.Contains(cmd[j[1]:], "-timeout ")
}

// Every go test the skills, the Redis test guide and CLAUDE.md tell an agent
// to run goes through agentbus run --cap with its own -timeout (§6, How
// agents run tests): a runaway suite took 56 GB on 2026-11-25.
func TestSkillGoTestLinesAreCapped(t *testing.T) {
	root := filepath.Join("..", "..")
	skills, err := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	if err != nil || len(skills) == 0 {
		t.Fatalf("no skills: %v", err)
	}
	files := map[string]bool{filepath.Join(root, "docs", "testing-redis.md"): false, filepath.Join(root, "CLAUDE.md"): false}
	for _, s := range skills {
		files[s] = true
	}
	seen := 0
	for path, spans := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, cmd := range goTestCommands(string(b), spans) {
			seen++
			if !cappedGoTest(cmd) {
				t.Errorf("%s: go test without agentbus run --cap and -timeout: %s", path, cmd)
			}
		}
	}
	if seen < 4 {
		t.Fatalf("only %d go test commands found: the scan reads nothing", seen)
	}
}

func TestGoTestCommandsScan(t *testing.T) {
	doc := "Plain `go test ./...` here.\n```sh\nAGENTBUS_TEST_REDIS_URL=x go test ./... -count=1\nagentbus run --cap -- go test ./x -timeout 5m\n```\n`go build` and `agentbus run --cap -- go test ./y -count=1`\n"
	if got := goTestCommands(doc, false); len(got) != 2 {
		t.Fatalf("fenced only: %q", got)
	}
	got := goTestCommands(doc, true)
	if len(got) != 4 {
		t.Fatalf("with spans: %q", got)
	}
	for cmd, want := range map[string]bool{
		"AGENTBUS_TEST_REDIS_URL=x go test ./... -count=1":                       false,
		"agentbus run --cap -- go test ./x -timeout 5m":                          true,
		"agentbus run --cap -- go test ./y -count=1":                             false,
		"go test ./x -timeout 5m # agentbus run --cap":                           false,
		"X=1 agentbus run --cap --mem 4G --time 5m -- go test ./... -timeout 4m": true,
		"agentbus run --cap -- go test ./x -run TestTimeout":                     false,
	} {
		if cappedGoTest(cmd) != want {
			t.Errorf("cappedGoTest(%q) = %v", cmd, !want)
		}
	}
}
