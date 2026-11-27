package plan

import (
	"encoding/json"
	"strings"
	"testing"
)

// onboarding §7.1 (spec docs/superpowers/specs/2026-11-27-project-onboarding-design.md):
// a task's check is one line of 1 to 200 characters with no control
// character; checks is "" or "required", and required makes every agent
// task (a mission too) carry one. Plans without them parse as before.
func TestParseCheck(t *testing.T) {
	plan := func(checks, tasks string) string {
		c := ""
		if checks != "" {
			c = `"checks":` + checks + `,`
		}
		return `{"plan":"p","goal":"g",` + c + `"tasks":[` + tasks + `]}`
	}
	f, err := Parse([]byte(plan(`"required"`, `{"id":"a","title":"x","owner":"coder","check":"go test ./plan -run TestX"},{"id":"b","title":"y","owner":"person"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if f.Checks != ChecksRequired || f.Tasks[0].Check != "go test ./plan -run TestX" || f.Tasks[1].Check != "" {
		t.Fatalf("parsed %+v", f)
	}
	if _, err := Parse([]byte(plan("", `{"id":"a","title":"x","owner":"coder","check":"`+strings.Repeat("é", 200)+`"}`))); err != nil {
		t.Fatalf("200 characters refused: %v", err)
	}
	if _, err := Parse([]byte(plan(`"required"`, `{"id":"a","title":"x","owner":"person","check":"open busmon, the tab shows it"}`))); err != nil {
		t.Fatalf("a person's check (a manual step): %v", err)
	}
	for name, in := range map[string]string{
		"201 characters":            plan("", `{"id":"a","title":"x","owner":"coder","check":"`+strings.Repeat("x", 201)+`"}`),
		"a newline":                 plan("", `{"id":"a","title":"x","owner":"coder","check":"go test\nrm -rf"}`),
		"a carriage return":         plan("", `{"id":"a","title":"x","owner":"coder","check":"go test\r"}`),
		"a control character":       plan("", `{"id":"a","title":"x","owner":"coder","check":"go test \u001b[2J"}`),
		"a tab":                     plan("", `{"id":"a","title":"x","owner":"coder","check":"go\ttest"}`),
		"a DEL":                     plan("", `{"id":"a","title":"x","owner":"coder","check":"go test \u007f"}`),
		"a C1 control":              plan("", `{"id":"a","title":"x","owner":"coder","check":"go test \u009b"}`),
		"a right-to-left override":  plan("", `{"id":"a","title":"x","owner":"coder","check":"go test \u202e hs.kcehc"}`),
		"a bidi isolate":            plan("", `{"id":"a","title":"x","owner":"coder","check":"go test \u2066x\u2069"}`),
		"a left-to-right mark":      plan("", `{"id":"a","title":"x","owner":"coder","check":"go test \u200e"}`),
		"an Arabic letter mark":     plan("", `{"id":"a","title":"x","owner":"coder","check":"go test \u061c"}`),
		"a zero-width space":        plan("", `{"id":"a","title":"x","owner":"coder","check":"go\u200btest"}`),
		"a line separator":          plan("", `{"id":"a","title":"x","owner":"coder","check":"go test\u2028rm"}`),
		"a private-use character":   plan("", `{"id":"a","title":"x","owner":"coder","check":"go test \ue000"}`),
		"whitespace only":           plan("", `{"id":"a","title":"x","owner":"coder","check":"   "}`),
		"checks unknown":            plan(`"always"`, `{"id":"a","title":"x","owner":"coder","check":"go test"}`),
		"required, a missing check": plan(`"required"`, `{"id":"a","title":"x","owner":"coder","check":"go test"},{"id":"b","title":"y","owner":"coder"}`),
		"required, a mission":       plan(`"required"`, `{"id":"a","title":"x","owner":"coder","mission":"c/a"}`),
	} {
		_, err := Parse([]byte(in))
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
		if name == "required, a missing check" && (err == nil || !strings.Contains(err.Error(), "plan: task b: a check is required (checks: required)")) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A plan without the fields marshals back without them: older readers
// (Parse refuses unknown fields) still read it.
func TestCheckFieldsOmitted(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"check`) {
		t.Fatalf("a plan without checks marshals them: %s", b)
	}
}
