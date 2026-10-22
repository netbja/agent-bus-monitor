// Package plan is a batch's plan as the person reads it in busmon: a versioned
// docs/plans/<name>.json published to Redis at a commit, whose task states are
// derived from the board, the verdict ledger, asks and missions, plus the few
// facts master records (spec
// docs/superpowers/specs/2026-10-22-plan-view-design.md).
package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// MaxTasks bounds a plan: it is a batch the person reads, not a backlog.
const MaxTasks = 50

// Person is the owner of the person's own steps.
const Person = "person"

const maxTitle = 120

var (
	planName    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	subjectForm = regexp.MustCompile(`^[a-z][a-z0-9_-]*:[A-Za-z0-9_./@-]+$`)
)

// File is docs/plans/<name>.json.
type File struct {
	Plan  string `json:"plan"`
	Goal  string `json:"goal"`
	Tasks []Task `json:"tasks"`
}

// Task is one line of the plan.
type Task struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Owner   string   `json:"owner"`
	Board   string   `json:"board,omitempty"`
	Review  string   `json:"review,omitempty"`
	Merge   *bool    `json:"merge,omitempty"`
	After   []string `json:"after,omitempty"`
	Mission string   `json:"mission,omitempty"`
}

// MergeTask reports whether the task ends with a merge by the person: an
// agent's task, unless it says merge:false; a mission is integrated by the
// conductor instead.
func (t Task) MergeTask() bool {
	if t.Owner == Person || t.Mission != "" {
		return false
	}
	return t.Merge == nil || *t.Merge
}

// ReviewSubject is the verdict subject of the task's review: its review
// field, else its board task (how this team names subjects).
func (t Task) ReviewSubject() string {
	if t.Review != "" {
		return t.Review
	}
	return t.Board
}

// Parse reads a plan strictly (unknown fields refused) and validates it.
func Parse(data []byte) (File, error) {
	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("plan: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return File{}, fmt.Errorf("plan: data after the top-level object")
	}
	return f, Validate(f)
}

// Validate checks names, sizes, references and the absence of cycles.
func Validate(f File) error {
	if !planName.MatchString(f.Plan) {
		return fmt.Errorf("plan: invalid plan name %q", f.Plan)
	}
	if !oneLine(f.Goal, 200) {
		return fmt.Errorf("plan: the goal must be one line of at most 200 characters")
	}
	if len(f.Tasks) == 0 || len(f.Tasks) > MaxTasks {
		return fmt.Errorf("plan: 1 to %d tasks, got %d", MaxTasks, len(f.Tasks))
	}
	ids := map[string]bool{}
	for _, t := range f.Tasks {
		if !bus.ValidName(t.ID) {
			return fmt.Errorf("plan: invalid task id %q", t.ID)
		}
		if ids[t.ID] {
			return fmt.Errorf("plan: duplicate task id %q", t.ID)
		}
		ids[t.ID] = true
		if t.Title == "" || !oneLine(t.Title, maxTitle) {
			return fmt.Errorf("plan: task %s: the title must be one line of 1 to %d characters", t.ID, maxTitle)
		}
		if t.Owner != Person && !bus.ValidName(t.Owner) {
			return fmt.Errorf("plan: task %s: invalid owner %q", t.ID, t.Owner)
		}
		for _, n := range []string{t.Board, t.Review} {
			if n != "" && !validSubject(n) {
				return fmt.Errorf("plan: task %s: invalid name %q", t.ID, n)
			}
		}
		if t.Mission != "" {
			c, id, ok := strings.Cut(t.Mission, "/")
			if !ok || !bus.ValidName(c) || !bus.ValidName(id) {
				return fmt.Errorf("plan: task %s: mission must be <change>/<id>", t.ID)
			}
			if t.Board != "" || t.Owner == Person {
				return fmt.Errorf("plan: task %s: a mission task has no board task and is not the person's", t.ID)
			}
		}
	}
	for _, t := range f.Tasks {
		for _, a := range t.After {
			if !ids[a] {
				return fmt.Errorf("plan: task %s waits for unknown task %q", t.ID, a)
			}
		}
	}
	if c := cycle(f.Tasks); c != "" {
		return fmt.Errorf("plan: a cycle through %s", c)
	}
	return nil
}

// validSubject accepts a board task or verdict subject: a ValidName, or the
// subject forms the ledger uses (pr:<n>, mission:<c>/<id>@<sha>).
func validSubject(s string) bool {
	if bus.ValidName(s) {
		return true
	}
	return len(s) <= 128 && subjectForm.MatchString(s)
}

func oneLine(s string, max int) bool {
	return len([]rune(s)) <= max && !strings.ContainsAny(s, "\r\n\t")
}

// cycle returns a task on a cycle of after edges, or "".
func cycle(tasks []Task) string {
	after := map[string][]string{}
	for _, t := range tasks {
		after[t.ID] = t.After
	}
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return true
		case 2:
			return false
		}
		state[id] = 1
		for _, a := range after[id] {
			if visit(a) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for _, t := range tasks {
		if visit(t.ID) {
			return t.ID
		}
	}
	return ""
}
