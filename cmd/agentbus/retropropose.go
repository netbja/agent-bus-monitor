package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/forge"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/history"
	"github.com/netbja/agent-bus-monitor/service"
)

// retro step 2 (self-improving loop §4-§7): propose, show, rejected,
// route, withdraw and close. The bus holds the record and the checks that
// need it (bus/retros.go); git, the archive and the file's text are read
// here.

// gitFile is a target file on the delivery base: its blob id and line
// count, or absent.
type gitFile struct {
	Blob   string
	Lines  int
	Exists bool
}

// retroPlusBus is what step 2's verbs need of the bus.
type retroPlusBus interface {
	RetroPropose(ctx context.Context, r bus.RetroRecord, cap int, dayStart int64) (bus.RetroRecord, error)
	Retro(ctx context.Context, task string) (bus.RetroRecord, error)
	RetroOutcomes(ctx context.Context, task string) ([]bus.RetroOutcome, error)
	RetroRoute(ctx context.Context, task string) (string, error)
	RetroWithdraw(ctx context.Context, task string) error
	RetroClose(ctx context.Context, task string, pr int) error
	RetroRejected(ctx context.Context, file string) ([]bus.RejectedRow, error)
	EntryExists(ctx context.Context, id string) (bool, error)
}

// retroPlusDeps are step 2's dependencies: Git reads a file on the base,
// Archived says whether the history archive holds an id, Setting reads a
// project int setting, Now is the clock (the cap's day).
type retroPlusDeps struct {
	Git      func(file string) (gitFile, error)
	Archived func(id string) bool
	Setting  func(name string) int
	Now      func() time.Time
	ReadFile func(name string) ([]byte, error)
}

var (
	skillFileRE  = regexp.MustCompile(`^skills/[a-z0-9][a-z0-9_-]*/SKILL\.md$`)
	roleFileRE   = regexp.MustCompile(`^roles/[a-z0-9][a-z0-9_-]*\.md$`)
	lessonFileRE = regexp.MustCompile(`^docs/memory/lessons/[A-Za-z0-9][A-Za-z0-9._-]*\.md$`)
)

// retroDestination says whether file fits destination (§4 point 4): an
// existing skill (skills/<name>/SKILL.md, roles/<name>.md), a context file
// (AGENTS.md, CLAUDE.md, docs/memory/lessons/<note>.md), or a new skill
// (skills/<name>/SKILL.md, absent).
func retroDestination(dest, file string) bool {
	clean := path.Clean(file) == file && !strings.HasPrefix(file, "/") && !strings.Contains(file, "..")
	if !clean {
		return false
	}
	skill := skillFileRE.MatchString(file)
	switch dest {
	case "skill":
		return skill || roleFileRE.MatchString(file)
	case "context":
		return file == "AGENTS.md" || file == "CLAUDE.md" || lessonFileRE.MatchString(file)
	case "new skill":
		return skill
	}
	return false
}

// parseRetro reads a retro file (§4): a "Root cause:" line, then the
// table n | lesson | evidence | destination | file | change (a header row,
// a separator, one row a proposal; evidence ids separated by commas or
// spaces).
func parseRetro(text string) (root string, rows []bus.RetroRow, err error) {
	cols := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		if r, ok := cutFold(l, "root cause:"); ok && root == "" {
			root = strings.TrimSpace(r)
			continue
		}
		if !strings.HasPrefix(l, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(l, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cols) == 0 {
			for i, c := range cells {
				cols[strings.ToLower(c)] = i
			}
			for _, need := range []string{"n", "lesson", "evidence", "destination", "file", "change"} {
				if _, ok := cols[need]; !ok {
					return "", nil, fmt.Errorf("retro: the table has no %q column (n | lesson | evidence | destination | file | change)", need)
				}
			}
			continue
		}
		if strings.Trim(strings.Join(cells, ""), "-: ") == "" { // the separator
			continue
		}
		get := func(c string) string {
			if i := cols[c]; i < len(cells) {
				return cells[i]
			}
			return ""
		}
		n, err := strconv.Atoi(get("n"))
		if err != nil {
			return "", nil, fmt.Errorf("retro: row %d: n %q is not a number", len(rows)+1, get("n"))
		}
		rows = append(rows, bus.RetroRow{N: n, Lesson: get("lesson"), Evidence: strings.FieldsFunc(get("evidence"), func(r rune) bool { return r == ',' || r == ' ' }),
			Destination: strings.ToLower(get("destination")), File: strings.Trim(get("file"), "`"), Change: get("change")})
	}
	if root == "" {
		return "", nil, errors.New("retro: no \"Root cause:\" line")
	}
	return root, rows, nil
}

func cutFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// retroPropose is agentbus retro propose <task> --file <retro.md> (§4),
// run by the retro's owner: the file parsed and checked (rows, evidence
// that exists, destinations on the base, nothing sensitive, the rejected
// list, the cap), then filed with each target's blob id and its ask opened
// or queued.
func retroPropose(ctx context.Context, d retroDeps, p retroPlusDeps, task, file string, out io.Writer) error {
	if d.Self == "" {
		return errors.New("retro propose: run it as the retro's owner (AGENT_BUS_AGENT)")
	}
	_, owner, err := d.Bus.FindRetro(ctx, task)
	if err != nil {
		return err
	}
	if owner == "" {
		return fmt.Errorf("retro propose: %s has no open retro", task)
	}
	if owner != d.Self {
		return fmt.Errorf("retro propose: %s's retro is %s's, not %s's", task, owner, d.Self)
	}
	raw, err := p.ReadFile(file)
	if err != nil {
		return fmt.Errorf("retro propose: %w", err)
	}
	root, rows, err := parseRetro(string(raw))
	if err != nil {
		return err
	}
	if len(rows) == 0 || len(rows) > bus.RetroMaxRows {
		return fmt.Errorf("retro: 1 to %d proposals, not %d", bus.RetroMaxRows, len(rows))
	}
	if health.Sensitive(root) {
		return errors.New("retro propose: the root cause matches a sensitive pattern (its value is not shown)")
	}
	var notes []string
	prune := p.Setting("retro.prune_lines")
	for i := range rows {
		row := &rows[i]
		for name, v := range map[string]string{"lesson": row.Lesson, "destination": row.Destination, "file": row.File, "change": row.Change, "evidence": strings.Join(row.Evidence, " ")} {
			if health.Sensitive(v) {
				return fmt.Errorf("retro propose: row %d's %s matches a sensitive pattern (its value is not shown)", i+1, name)
			}
		}
		for _, id := range row.Evidence {
			ok, err := d.Bus.(retroPlusBus).EntryExists(ctx, id)
			if err != nil {
				return err
			}
			if !ok && (p.Archived == nil || !p.Archived(id)) {
				return fmt.Errorf("retro propose: row %d: evidence %s is in no stream, ledger or archive", i+1, id)
			}
		}
		if !retroDestination(row.Destination, row.File) {
			return fmt.Errorf("retro propose: row %d: %s is not a %s destination (skills/<name>/SKILL.md, roles/<name>.md, AGENTS.md, CLAUDE.md, docs/memory/lessons/<note>.md)", i+1, row.File, row.Destination)
		}
		f, err := p.Git(row.File)
		if err != nil {
			return fmt.Errorf("retro propose: row %d: %w", i+1, err)
		}
		switch {
		case row.Destination == "new skill" && f.Exists:
			return fmt.Errorf("retro propose: row %d: %s exists on the base: a new skill's file must be absent", i+1, row.File)
		case row.Destination == "new skill":
			row.Blob = bus.RetroBlobAbsent
		case !f.Exists:
			return fmt.Errorf("retro propose: row %d: %s is not on the base", i+1, row.File)
		default:
			row.Blob = f.Blob
			if prune > 0 && f.Lines > prune {
				notes = append(notes, fmt.Sprintf("%s has %d lines, past retro.prune_lines (%d): a pruning pass may be due", row.File, f.Lines, prune))
			}
		}
	}
	now := p.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	rec, err := d.Bus.(retroPlusBus).RetroPropose(ctx, bus.RetroRecord{Task: task, Owner: d.Self, Root: root, Rows: rows, Notes: notes}, p.Setting("retro.max_per_day"), day.UnixMilli())
	if err != nil {
		return err
	}
	name, _, _ := d.Bus.FindRetro(ctx, task)
	if rec.State == bus.RetroQueued {
		_, err = fmt.Fprintf(out, "retro %s filed, queued: another retro's ask is open; it opens when that one is routed\nnow close the request: agentbus request done %s \"proposed\"\n", task, name)
		return err
	}
	_, err = fmt.Fprintf(out, "retro %s filed, ask %s open for the person\nnow close the request: agentbus request done %s \"proposed\"\n", task, rec.AskID, name)
	return err
}

// retroShow prints the retro and each proposal's outcome (§6): routed ones
// as recorded, others as they stand; each target compared with the base
// (changed since propose, or a new skill's file now there: a conflict); a
// field matching a sensitive pattern is withheld.
func retroShow(ctx context.Context, b retroPlusBus, p retroPlusDeps, task string, out io.Writer) error {
	rec, err := b.Retro(ctx, task)
	if err != nil {
		return err
	}
	outs := rec.Outcomes
	if rec.State != bus.RetroRouted && rec.State != bus.RetroClosed {
		if outs, err = b.RetroOutcomes(ctx, task); err != nil {
			return err
		}
	}
	safe := func(s string) string {
		if health.Sensitive(s) {
			return "[withheld: matches a sensitive pattern]"
		}
		return s
	}
	fmt.Fprintf(out, "retro %s · %s · owner %s", task, rec.State, rec.Owner)
	if rec.AskID != "" {
		fmt.Fprintf(out, " · ask %s", rec.AskID)
	}
	if rec.PR > 0 {
		fmt.Fprintf(out, " · PR %d", rec.PR)
	}
	fmt.Fprintf(out, "\nRoot cause: %s\n", safe(rec.Root))
	for i, row := range rec.Rows {
		o := bus.RetroOutcome{N: row.N, Outcome: bus.OutcomeUnanswered}
		if i < len(outs) {
			o = outs[i]
		}
		fmt.Fprintf(out, "%d. %s", row.N, o.Outcome)
		if o.Entry != "" {
			fmt.Fprintf(out, " (%s)", o.Entry)
		}
		fmt.Fprintf(out, " · %s · %s · %s\n   lesson: %s\n   change: %s\n   evidence: %s\n", row.Destination, safe(row.File), row.Blob, safe(row.Lesson), safe(row.Change), strings.Join(row.Evidence, ", "))
		if p.Git == nil {
			continue
		}
		f, err := p.Git(row.File)
		switch {
		case err != nil:
			fmt.Fprintf(out, "   base: unread (%v)\n", err)
		case row.Blob == bus.RetroBlobAbsent && f.Exists:
			fmt.Fprintf(out, "   conflict: %s exists on the base now\n", row.File)
		case row.Blob != bus.RetroBlobAbsent && !f.Exists:
			fmt.Fprintf(out, "   conflict: %s is gone from the base\n", row.File)
		case row.Blob != bus.RetroBlobAbsent && f.Blob != row.Blob:
			fmt.Fprintf(out, "   changed since propose (%s now): re-read the changed part before applying\n", f.Blob)
		}
	}
	for _, n := range rec.Notes {
		fmt.Fprintf(out, "note: %s\n", safe(n))
	}
	return nil
}

// runRetroPlus is step 2's verbs; ok false when verb is not one of them.
func runRetroPlus(ctx context.Context, d retroDeps, p retroPlusDeps, verb string, args []string, out io.Writer) (bool, error) {
	b, has := d.Bus.(retroPlusBus)
	switch verb {
	case "propose", "show", "route", "withdraw", "close", "rejected":
	default:
		return false, nil
	}
	if !has {
		return true, errors.New("retro: this bus has no retros")
	}
	if verb == "rejected" {
		rest, file := extractFlag(args, "--file")
		if len(rest) != 0 {
			return true, errors.New("usage: retro rejected [--file <file>]")
		}
		rows, err := b.RetroRejected(ctx, file)
		if err != nil {
			return true, err
		}
		for _, r := range rows {
			fmt.Fprintf(out, "%s · %s · from %s\n", r.File, r.Lesson, r.From)
		}
		if len(rows) == 0 {
			fmt.Fprintln(out, "no rejected proposal")
		}
		return true, nil
	}
	rest, file := extractFlag(args, "--file")
	rest, pr := extractFlag(rest, "--pr")
	if len(rest) != 1 || !bus.ValidName(rest[0]) {
		return true, fmt.Errorf("usage: retro %s <task>%s", verb, map[string]string{"propose": " --file <retro.md>", "close": " --pr <n>"}[verb])
	}
	task := rest[0]
	switch verb {
	case "propose":
		if file == "" {
			return true, errors.New("usage: retro propose <task> --file <retro.md>")
		}
		return true, retroPropose(ctx, d, p, task, file, out)
	case "show":
		return true, retroShow(ctx, b, p, task, out)
	case "route", "withdraw":
		if d.Self != d.Master {
			return true, fmt.Errorf("retro %s is %s's act, not %q's", verb, d.Master, d.Self)
		}
		if verb == "withdraw" {
			if err := b.RetroWithdraw(ctx, task); err != nil {
				return true, err
			}
			_, err := fmt.Fprintf(out, "retro %s: its ask withdrawn; now agentbus retro route %s\n", task, task)
			return true, err
		}
		next, err := b.RetroRoute(ctx, task)
		if err != nil {
			return true, err
		}
		fmt.Fprintf(out, "retro %s routed to %s (%s)\n", task, bus.RetroDocs, bus.RetroApplyName(task))
		if next != "" {
			fmt.Fprintf(out, "next retro asked: %s\n", next)
		}
		return true, nil
	case "close":
		n, err := strconv.Atoi(pr)
		if err != nil {
			return true, errors.New("usage: retro close <task> --pr <n>")
		}
		if d.Self == "" {
			return true, errors.New("retro close: run it as the agent that delivered it (AGENT_BUS_AGENT)")
		}
		if err := b.RetroClose(ctx, task, n); err != nil {
			return true, err
		}
		_, err = fmt.Fprintf(out, "retro %s closed (PR %d)\n", task, n)
		return true, err
	}
	return true, nil
}

// gitOnBase reads file on origin/<base> in the current repository.
func gitOnBase(base string) func(file string) (gitFile, error) {
	return func(file string) (gitFile, error) {
		ref := "origin/" + base + ":" + file
		blob, err := exec.Command("git", "rev-parse", "--verify", "--quiet", ref).Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return gitFile{}, nil // absent on the base
			}
			return gitFile{}, fmt.Errorf("git: %w", err)
		}
		body, err := exec.Command("git", "show", ref).Output()
		if err != nil {
			return gitFile{}, fmt.Errorf("git show %s: %w", ref, err)
		}
		return gitFile{Blob: strings.TrimSpace(string(blob)), Lines: strings.Count(string(body), "\n"), Exists: true}, nil
	}
}

// retroPlusFor is step 2's real dependencies for project: git on the
// project's delivery base (origin/<base>), the history archive, the
// project's settings from config.toml, the clock, the file system.
func retroPlusFor(project string, getenv func(string) string) retroPlusDeps {
	base, _, err := forge.ProjectDeliveryBase(getenv, project)
	if err != nil || base == "" {
		base = "dev"
	}
	dir := ""
	if p, err := service.PathsFrom(getenv); err == nil {
		dir = history.Dir(p.StateDir(project))
	}
	return retroPlusDeps{
		Git: gitOnBase(base),
		Archived: func(id string) bool {
			if dir == "" {
				return false
			}
			ms, err := strconv.ParseInt(strings.SplitN(id, "-", 2)[0], 10, 64)
			if err != nil {
				return false
			}
			day := time.UnixMilli(ms)
			es, _, err := history.ReadDays(dir, day.Add(-24*time.Hour), day.Add(24*time.Hour), 256<<20)
			if err != nil {
				return false
			}
			for _, e := range es {
				if e.ID == id {
					return true
				}
			}
			return false
		},
		Setting: func(name string) int {
			f := config.Load(config.Path(getenv), "config.toml")
			n, _ := config.ProjectValue(f, nil, project, name).Value.(int)
			return n
		},
		Now:      time.Now,
		ReadFile: os.ReadFile,
	}
}
