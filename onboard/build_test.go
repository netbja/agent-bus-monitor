package onboard

import (
	"strings"
	"testing"
)

const sheet = `# notes: build sheet (v0)

## Goal
A notes app the person uses on the phone.
It keeps notes across restarts.

## Users
The person.

## Constraints
None.

## Stack
Go, SQLite.

## Definition of done
- [dod-1] a note survives a restart — check: go test ./store/...
- [dod-2] the app works on the phone — check: person: open the app on the phone, add a note, see it after a restart

## Out of scope
Sync.

## How to run
go run .

## How to verify
scripts/check.sh

## Risks
None known.

## Decisions
SQLite over files.
`

func TestParseBuildReadsGoalAndItems(t *testing.T) {
	b, err := ParseBuild([]byte(sheet))
	if err != nil {
		t.Fatal(err)
	}
	if b.Project != "notes" || b.Version != "v0" {
		t.Fatalf("project %q version %q", b.Project, b.Version)
	}
	if b.Goal != "A notes app the person uses on the phone.\nIt keeps notes across restarts." {
		t.Fatalf("goal %q", b.Goal)
	}
	want := []DoDItem{
		{ID: "dod-1", Item: "a note survives a restart", Check: "go test ./store/..."},
		{ID: "dod-2", Item: "the app works on the phone", Check: "person: open the app on the phone, add a note, see it after a restart"},
	}
	if len(b.DoD) != len(want) {
		t.Fatalf("items %+v", b.DoD)
	}
	for i := range want {
		if b.DoD[i] != want[i] {
			t.Fatalf("item %d: %+v, want %+v", i, b.DoD[i], want[i])
		}
	}
}

// A heading the grade adds later (§11.1), or one the architect adds, is
// allowed anywhere: only the fixed ones are required, in their order.
func TestParseBuildAllowsExtraHeadings(t *testing.T) {
	s := strings.Replace(sheet, "## Decisions\n", "## Done before v1\n- api\n\n## Decisions\n", 1) + "\n## Grade v0\nPASS\n"
	if _, err := ParseBuild([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

func TestParseBuildRefuses(t *testing.T) {
	cases := map[string]string{
		"no title":          strings.Replace(sheet, "# notes: build sheet (v0)\n", "", 1),
		"another title":     strings.Replace(sheet, "# notes: build sheet (v0)", "# notes", 1),
		"a heading missing": strings.Replace(sheet, "## Risks\nNone known.\n\n", "", 1),
		// the last one: only the count sees it, no later heading is out of order
		"the last heading missing": strings.Replace(sheet, "## Decisions\nSQLite over files.\n", "", 1),
		"headings out of order":    strings.NewReplacer("## Users\n", "## Stack\n", "## Stack\n", "## Users\n").Replace(sheet),
		"a heading twice":          sheet + "\n## Goal\nagain\n",
		"an item without check":    strings.Replace(sheet, " — check: go test ./store/...", "", 1),
		"a line not an item":       strings.Replace(sheet, "## Definition of done\n", "## Definition of done\nsome words\n", 1),
		"an item with no id":       strings.Replace(sheet, "- [dod-1] ", "- ", 1),
		"an id twice":              strings.Replace(sheet, "[dod-2]", "[dod-1]", 1),
		"an empty check":           strings.Replace(sheet, "go test ./store/...", " ", 1),
		"a check too long":         strings.Replace(sheet, "go test ./store/...", strings.Repeat("x", 201), 1),
		"a control character":      strings.Replace(sheet, "go test ./store/...", "go test\u202e ./store/...", 1),
		"no item":                  strings.Replace(strings.Replace(sheet, "- [dod-1] a note survives a restart — check: go test ./store/...\n", "", 1), "- [dod-2] the app works on the phone — check: person: open the app on the phone, add a note, see it after a restart\n", "", 1),
		"an empty goal":            strings.Replace(sheet, "A notes app the person uses on the phone.\nIt keeps notes across restarts.\n", "", 1),
	}
	for name, s := range cases {
		if _, err := ParseBuild([]byte(s)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
