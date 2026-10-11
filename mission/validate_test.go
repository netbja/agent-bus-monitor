package mission

import (
	"strings"
	"testing"
)

const validJSON = `{
  "change": "busmon-state", "reviewer": "codex", "test": "go test ./...",
  "missions": [
    {"id": "t1", "title": "Characterisation tests", "agent": "kimi",
     "allowed_paths": ["cmd/busmon/*_test.go"], "test": "go test ./cmd/busmon/", "brief": "plan.md#task-1"},
    {"id": "t3", "title": "Extract the state model", "agent": "coder", "reviewer": "kimi",
     "depends_on": ["t1"], "allowed_paths": ["cmd/busmon/state*.go", "cmd/busmon/*_test.go"],
     "test": "go test ./cmd/busmon/", "timeout": "4h", "brief": "plan.md#task-3"}
  ]}`

var tree = []string{"cmd/busmon/main.go", "cmd/busmon/ui_test.go", "README.md"}

func allBriefs(p string) (string, bool) { return p, true }

func TestParseDefaults(t *testing.T) {
	f, err := Parse([]byte(validJSON))
	if err != nil {
		t.Fatal(err)
	}
	if f.Base != "main" || f.Branch != "change/busmon-state" || f.MaxAttempts != 3 {
		t.Fatalf("defaults: base %q branch %q max %d", f.Base, f.Branch, f.MaxAttempts)
	}
	if err := Validate(f, tree, allBriefs); err != nil {
		t.Fatalf("valid file refused: %v", err)
	}
	if f.ReviewerOf(f.Missions[0]) != "codex" || f.ReviewerOf(f.Missions[1]) != "kimi" {
		t.Fatal("ReviewerOf")
	}
}

// edit applies a textual change to validJSON; the result must be refused with
// an error naming want.
func TestValidateRefusals(t *testing.T) {
	cases := []struct{ name, from, to, want string }{
		{"unknown field", `"test": "go test ./...",`, `"test": "go test ./...", "bogus": 1,`, "bogus"},
		{"duplicate id", `"id": "t3"`, `"id": "t1"`, "t1"},
		{"unknown dependency", `"depends_on": ["t1"]`, `"depends_on": ["t9"]`, "t9"},
		{"cycle", `"agent": "kimi",`, `"agent": "kimi", "depends_on": ["t3"],`, "cycle"},
		{"bad agent", `"agent": "coder"`, `"agent": "Coder X"`, "t3"},
		{"reviewer is agent", `"reviewer": "kimi",`, `"reviewer": "coder",`, "t3"},
		{"upper-case id", `"id": "t3"`, `"id": "T3"`, "T3"},
		{"long id", `"id": "t3"`, `"id": "t3456789a"`, "t3456789a"},
		{"long change", `"change": "busmon-state"`, `"change": "busmon-state-long1"`, "busmon-state-long1"},
		{"empty allowed_paths", `"allowed_paths": ["cmd/busmon/*_test.go"], "test": "go test ./cmd/busmon/", "brief": "plan.md#task-1"`, `"allowed_paths": [], "test": "go test ./cmd/busmon/", "brief": "plan.md#task-1"`, "t1"},
		{"no test", `"test": "go test ./cmd/busmon/", "brief": "plan.md#task-1"`, `"brief": "plan.md#task-1"`, "t1"},
		{"overlap without dependency", `"depends_on": ["t1"], `, ``, "overlap"},
		{"foreign branch", `"test": "go test ./...",`, `"test": "go test ./...", "branch": "main",`, "branch"},
		{"other change branch", `"test": "go test ./...",`, `"test": "go test ./...", "branch": "change/other",`, "branch"},
		{"base is branch", `"test": "go test ./...",`, `"test": "go test ./...", "base": "change/busmon-state",`, "base"},
		{"no reviewer", `"reviewer": "codex", `, ``, "reviewer"},
		{"bad timeout", `"timeout": "4h"`, `"timeout": "soon"`, "t3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := strings.Replace(validJSON, c.from, c.to, 1)
			if src == validJSON {
				t.Fatalf("fixture edit %q did not apply", c.from)
			}
			f, err := Parse([]byte(src))
			if err == nil {
				err = Validate(f, tree, allBriefs)
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one naming %q", err, c.want)
			}
		})
	}
	f, _ := Parse([]byte(validJSON))
	if err := Validate(f, tree, func(p string) (string, bool) { return p, p != "plan.md" }); err == nil || !strings.Contains(err.Error(), "plan.md") {
		t.Fatalf("missing brief: %v", err)
	}
}

func TestOverlapOrderedIsAllowed(t *testing.T) {
	f, _ := Parse([]byte(validJSON)) // t3 overlaps t1 on *_test.go but depends on it
	if err := Validate(f, tree, allBriefs); err != nil {
		t.Fatal(err)
	}
}

func TestTaskName(t *testing.T) {
	if got := TaskName("busmon-state", "t3", 2); got != "busmon-state-t3-2" {
		t.Fatal(got)
	}
}

func TestHashes(t *testing.T) {
	f, _ := Parse([]byte(validJSON))
	g, _ := Parse([]byte(validJSON))
	if f.Missions[0].Hash() != g.Missions[0].Hash() || f.Missions[0].Hash() == f.Missions[1].Hash() {
		t.Fatal("mission hash not stable or not distinct")
	}
	if FileHash([]byte("a")) == FileHash([]byte("b")) || len(FileHash([]byte("a"))) != 64 {
		t.Fatal("FileHash")
	}
}

func TestParseRefusesTrailingData(t *testing.T) {
	for _, tail := range []string{`{"change": "other"}`, "garbage", "]"} {
		if _, err := Parse([]byte(validJSON + "\n" + tail)); err == nil {
			t.Errorf("trailing %q accepted", tail)
		}
	}
	if _, err := Parse([]byte(validJSON + "\n\n")); err != nil {
		t.Errorf("trailing whitespace refused: %v", err)
	}
}
