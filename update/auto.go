package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/files"
)

// The automatic update's reads (quiet-update-auto spec
// docs/superpowers/specs/2026-11-21-quiet-update-auto-design.md, step 4a):
// the quiet test (§4.3 step 4), the state file (§4.5), the releases it must
// not install (§4.3 step 3), and the check pass, update --auto --check.

// FetchTimeout bounds an automatic pass's fetch: a remote that would ask
// for a password fails the pass instead of hanging it (§8).
const FetchTimeout = 2 * time.Minute

// RecentChange is how recent a status change keeps a project in work (§4.3).
const RecentChange = 10 * time.Minute

// AgentFact is an agent of {p}:agents as the quiet test reads it.
type AgentFact struct {
	Name, State string
	Changed     time.Time // its last status change
}

// MissionFact is a conductor's mission as the quiet test reads it.
type MissionFact struct{ Change, ID, State string }

// Judged is what a project's broker said, read at its Redis TIME.
type Judged struct {
	Now      time.Time
	Agents   []AgentFact
	Missions []MissionFact
}

// InWork is why a project is in work (§4.3 step 4), "" when it is quiet: an
// agent working, an agent's status changed less than RecentChange ago, or a
// mission checking or integrating (the conductor at work).
func InWork(j Judged) string {
	agents := slices.Clone(j.Agents)
	sort.Slice(agents, func(a, b int) bool { return agents[a].Name < agents[b].Name })
	for _, a := range agents {
		if a.State == "working" {
			return "agent " + a.Name + " is working"
		}
	}
	for _, a := range agents {
		if !a.Changed.IsZero() && j.Now.Sub(a.Changed) < RecentChange {
			return fmt.Sprintf("agent %s changed its status %s ago", a.Name, j.Now.Sub(a.Changed).Round(time.Second))
		}
	}
	for _, m := range j.Missions {
		if m.State == "checking" || m.State == "integrating" {
			return "mission " + m.Change + "/" + m.ID + " is " + m.State
		}
	}
	return ""
}

// ProjectWork is a judged project: why it is in work, "" quiet.
type ProjectWork struct {
	Project string
	Why     string
}

// Quiet judges every project with a running dispatch or missions, or an
// active holder, on this host (§4.3 step 4), sorted by name. It fails
// closed: when the services, a holder or a project's broker cannot be read,
// it returns the error, and the pass is deferred.
func Quiet(ctx context.Context, d Deps) ([]ProjectWork, error) {
	if d.Running == nil || d.Holders == nil || d.Judge == nil {
		return nil, errors.New("the quiet test cannot read this host")
	}
	running, err := d.Running(ctx)
	if err != nil {
		return nil, fmt.Errorf("the services cannot be read: %w", err)
	}
	held, err := d.Holders(ctx)
	if err != nil {
		return nil, fmt.Errorf("the holders cannot be read: %w", err)
	}
	seen := map[string]bool{}
	for _, s := range running {
		seen[s.Project] = true
	}
	for _, p := range held {
		seen[p] = true
	}
	var out []ProjectWork
	for p := range seen {
		j, err := d.Judge(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("project %s cannot be read: %w", p, err)
		}
		out = append(out, ProjectWork{Project: p, Why: InWork(j)})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Project < out[b].Project })
	return out, nil
}

// AutoState is <updates>/auto.json (§4.5), the host's record, its times in
// local wall-clock time with their zone.
type AutoState struct {
	LastPass  *AutoPass         `json:"last_pass,omitempty"`
	LastCheck time.Time         `json:"last_check,omitempty"` // the last fetch that succeeded
	Waiting   *AutoWaiting      `json:"waiting,omitempty"`    // a release found and not installed
	Told      map[string]string `json:"told,omitempty"`       // notice kind -> the tag or snapshot it was sent for
}

// AutoPass is when the last pass finished and its result: installed,
// current, deferred, skipped, failed or rolled_back.
type AutoPass struct {
	At     time.Time `json:"at"`
	Result string    `json:"result"`
}

// AutoWaiting is the release waiting and since when.
type AutoWaiting struct {
	Tag   string    `json:"tag"`
	Since time.Time `json:"since"`
}

func autoPath(updates string) string { return filepath.Join(updates, "auto.json") }

// ReadAuto is the state file; a missing or unreadable one is empty (§4.5:
// the worst case is one repeated notice).
func ReadAuto(updates string) AutoState {
	var s AutoState
	if b, err := os.ReadFile(autoPath(updates)); err == nil && json.Unmarshal(b, &s) == nil {
		return s
	}
	return AutoState{}
}

// WriteAuto replaces the state file atomically.
func WriteAuto(updates string, s AutoState) error {
	if err := os.MkdirAll(updates, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return files.WriteAtomic(autoPath(updates), append(b, '\n'))
}

// waitingFor keeps since when tag has waited; another tag starts again.
func (s *AutoState) waitingFor(tag string, now time.Time) {
	if s.Waiting == nil || s.Waiting.Tag != tag {
		s.Waiting = &AutoWaiting{Tag: tag, Since: now}
	}
}

// HistoryAuto is the history kind of an automatic install.
const HistoryAuto = "auto"

// Skip is why a pass must not install rel (§4.3 step 3), "" none: the tag
// carries docs/releases/<tag>.migrate (a broker migration, the person's
// update), or its last automatic install failed (never retried; a manual
// update of it still runs). A tag or a history that cannot be read is an
// error: the guard is unknown, so the pass is deferred (reviewer-codex).
func Skip(ctx context.Context, updates string, rel Release) (string, error) {
	migrate := "docs/releases/" + rel.Tag + ".migrate"
	out, err := git(ctx, "--git-dir", rel.GitDir, "ls-tree", "--name-only", rel.Tag+"^{commit}", "--", migrate)
	if err != nil { // the tag itself cannot be read; a missing path is an empty answer
		return "", fmt.Errorf("whether %s needs a migration cannot be read: %w", rel.Tag, err)
	}
	if out == migrate {
		return rel.Tag + " needs a broker migration: update it by hand (./setup.sh, with its notes)", nil
	}
	hs, err := ReadHistory(updates)
	if err != nil {
		return "", fmt.Errorf("the update history cannot be read: %w", err)
	}
	for i := len(hs) - 1; i >= 0; i-- {
		if h := hs[i]; h.Kind == HistoryAuto && h.To == rel.Tag {
			if !h.OK {
				return "the automatic install of " + rel.Tag + " failed: it is not retried (agentbus update installs it by hand)", nil
			}
			break
		}
	}
	return "", nil
}

// CheckPass is agentbus update --auto --check (§4.2 step 4, §4.3 steps 2-4):
// it finds the release with a fetch that never prompts, says why it would
// not be installed, judges each project, and records the check in
// auto.json. It reads only: nothing is staged or installed. Exit 0, or 1
// when the fetch or the quiet test failed.
func CheckPass(ctx context.Context, d Deps) int {
	updates := d.updates()
	st := ReadAuto(updates)
	now := d.Now()
	save := func() {
		if err := WriteAuto(updates, st); err != nil {
			fmt.Fprintln(d.Err, "agentbus update: auto.json cannot be written: "+err.Error())
		}
	}
	fctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	rel, err := Find(fctx, d.Repo, d.Version)
	cancel()
	switch {
	case errors.Is(err, ErrUpToDate):
		st.LastCheck, st.Waiting = now, nil
		save()
		d.say("agentbus %s is up to date", d.Version)
		return 0
	case err != nil:
		fmt.Fprintln(d.Err, "agentbus update: "+err.Error())
		return 1
	}
	st.LastCheck = now
	st.waitingFor(rel.Tag, now)
	save()
	d.say("release %s waiting since %s", rel.Tag, st.Waiting.Since.Format(time.RFC3339))
	why, err := Skip(ctx, updates, rel)
	if err != nil {
		fmt.Fprintln(d.Err, "agentbus update: "+err.Error()+": a pass would be deferred")
		return 1
	}
	if why != "" {
		d.say("not installed automatically: %s", why)
		return 0
	}
	if d.AutoSetting == nil || d.AutoSetting() != "install" {
		d.say("updates.auto is not install: agentbus update installs it")
	}
	ps, err := Quiet(ctx, d)
	if err != nil {
		fmt.Fprintln(d.Err, "agentbus update: the quiet test cannot judge this host, a pass would be deferred: "+err.Error())
		return 1
	}
	if len(ps) == 0 {
		d.say("no project runs services or a holder on this host")
	}
	for _, p := range ps {
		if p.Why == "" {
			d.say("project %s: quiet", p.Project)
		} else {
			d.say("project %s: in work (%s): a pass would be deferred", p.Project, p.Why)
		}
	}
	return 0
}
