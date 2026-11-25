package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
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

// shellCommands splits a shell line into its simple commands, each a list
// of words: ;, &, | (so &&, ||) end a command outside quotes, a # that starts
// a word starts a comment, '...' and "..." quote. ok is false on an
// unterminated quote.
func shellCommands(line string) (cmds [][]string, ok bool) {
	var cmd []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			cmd = append(cmd, word.String())
			word.Reset()
			inWord = false
		}
	}
	endCmd := func() {
		endWord()
		if len(cmd) > 0 {
			cmds = append(cmds, cmd)
			cmd = nil
		}
	}
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\'' || c == '"':
			j := strings.IndexByte(line[i+1:], c)
			if j < 0 {
				return nil, false
			}
			word.WriteString(line[i+1 : i+1+j])
			inWord, i = true, i+1+j
		case c == ' ' || c == '\t':
			endWord()
		case c == ';' || c == '&' || c == '|':
			endCmd()
		case c == '#' && !inWord:
			endCmd()
			return cmds, true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endCmd()
	return cmds, true
}

// cappedGoTest: every simple command of cmd that runs go test is
// [VAR=value...] agentbus run --cap [--flag value...] -- go test ..., with a
// -timeout above 0 (agent-resource-limits §6: Go's 10m default per package
// binary is gone once a script passes -timeout 0). A go test after a
// separator runs outside the cap (reviewer-codex).
func cappedGoTest(cmd string) bool {
	cmds, ok := shellCommands(cmd)
	if !ok {
		return false
	}
	seen := false
	for _, words := range cmds {
		if !slices.Contains(words, "go") || !goTestWord.MatchString(strings.Join(words, " ")) {
			continue
		}
		seen = true
		w := words
		for len(w) > 0 && strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "-") {
			w = w[1:] // VAR=value
		}
		if len(w) < 3 || w[0] != "agentbus" || w[1] != "run" || w[2] != "--cap" {
			return false
		}
		w = w[3:]
		for len(w) >= 2 && w[0] != "--" && strings.HasPrefix(w[0], "--") && w[1] != "--" {
			w = w[2:] // --mem 4G
		}
		if len(w) < 3 || w[0] != "--" || w[1] != "go" || w[2] != "test" || !positiveTimeout(w[3:]) {
			return false
		}
	}
	return seen
}

// positiveTimeout: go test's arguments hold -timeout <d> or -timeout=<d>
// (one dash or two) with d above 0.
func positiveTimeout(args []string) bool {
	for i, a := range args {
		v, found := "", false
		for _, f := range []string{"-timeout", "--timeout"} {
			if a == f && i+1 < len(args) {
				v, found = args[i+1], true
			} else if rest, ok := strings.CutPrefix(a, f+"="); ok {
				v, found = rest, true
			}
		}
		if found {
			d, err := time.ParseDuration(v)
			return err == nil && d > 0
		}
	}
	return false
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
		// reviewer-codex: a go test after a separator runs outside the cap,
		// and -timeout 0 turns Go's own deadline off.
		"agentbus run --cap -- true; go test ./... -timeout 25m":                        false,
		"agentbus run --cap -- true && go test ./... -timeout 25m":                      false,
		"agentbus run --cap -- true || go test ./... -timeout 25m":                      false,
		"agentbus run --cap -- true | go test ./... -timeout 25m":                       false,
		"agentbus run --cap -- true & go test ./... -timeout 25m":                       false,
		"agentbus run --cap -- go test ./... -timeout 0":                                false,
		"agentbus run --cap -- go test ./... -timeout=0s":                               false,
		"agentbus run --cap -- go test ./... -timeout -1m":                              false,
		"agentbus run --cap -- go test ./... -timeout x":                                false,
		"agentbus run --cap -- go test ./... -timeout":                                  false,
		"agentbus run --cap -- go test ./... -timeout=25m":                              true,
		"agentbus run --cap -- go test ./... -timeout 25m; go vet ./...":                true,
		"go build ./... && agentbus run --cap -- go test ./... -timeout 25m":            true,
		"agentbus run --cap -- go test ./a -timeout 5m && go test ./b -timeout 5m":      false,
		"agentbus run --cap -- sh -c 'go test ./... -timeout 5m'":                       false,
		"myagentbus run --cap -- go test ./x -timeout 5m":                               false,
		"echo agentbus run --cap -- go test ./x -timeout 5m":                            false,
		"agentbus run --cap --mem 4G -- go test ./x -timeout 5m":                        true,
		"agentbus run --cap --mem -- go test ./x -timeout 5m":                           false,
		"A=1 B='x y' agentbus run --cap -- go test ./x -run '^T(a|b)$' -timeout 15m -v": true,
		"agentbus run --cap -- go test ./x -run '^T(a|b)$; go test' -timeout 15m":       true,
		"agentbus run --cap -- go test ./x -run 'unterminated -timeout 15m":             false,
	} {
		if cappedGoTest(cmd) != want {
			t.Errorf("cappedGoTest(%q) = %v", cmd, !want)
		}
	}
}
