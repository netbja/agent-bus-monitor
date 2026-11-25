package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/setup"
)

// Options are the command's flags.
type Options struct {
	Project string // whose dispatch and missions restart; "" restarts nothing
	Check   bool   // the plan only
	Yes     bool   // the person's yes, given ahead
}

// Deps is what an update reaches: setup's Runner for every build and every
// agentbus or busmon it runs, and the running build's facts.
type Deps struct {
	R       setup.Runner
	Getenv  func(string) string
	Environ []string // the process environment (skill destinations read it)
	Now     func() time.Time
	Sleep   func(time.Duration)
	Out     io.Writer
	Err     io.Writer
	TTY     io.ReadWriter // the terminal to ask on; nil when there is none

	Version   string // buildinfo.Version
	Repo      string // buildinfo.Repo
	BinDir    string
	FromBuild string // the running build's service.BuildKey
	// Running are this host's active dispatch and missions units, each with
	// the build it recorded.
	Running func(ctx context.Context) ([]Service, error)
	// Holders are the projects with an active holder of any build on this
	// host (quiet-update-auto §4.3 step 4); Judge reads a project's agents
	// and missions on its broker, at its Redis TIME. Either failing defers
	// an automatic pass (it fails closed).
	Holders func(ctx context.Context) ([]string, error)
	Judge   func(ctx context.Context, project string) (Judged, error)
	// AutoSetting is updates.auto from config.toml, read at the start of a
	// pass: "install" is the person's yes given ahead (§2).
	AutoSetting func() string
	// Settle is how long a restarted service has to record its build (60 s).
	Settle time.Duration
	// HolderKeys are the holder builds on disk, every project's: a unit's or
	// a binary copy's key (service.BuildKey). nil or an error keeps every
	// tree (quiet-update-auto §5, rule 3).
	HolderKeys func(ctx context.Context) (map[string]bool, error)
	// HolderGC collects the old holders that neither run nor answer, with
	// current the new build's key (service.GCHolders, GCHolderBinaries), before
	// the trees are judged. nil: none.
	HolderGC func(ctx context.Context, current string) error
	// Acted, when set, is told once how an update Run was asked to install
	// ended (audit step 2b: agentbus writes its update line); --check, up to
	// date and a refusal before the plan are no act.
	Acted func(Act)
}

// Act is an update's end, for the audit trail: the versions, the projects
// whose services run on this host (they run the replaced binaries; the one
// named first), and the outcome, bus.Audit's words, with a detail of fixed
// parts.
type Act struct {
	From, To, Outcome, Detail string
	Projects                  []string
}

// actProjects are o's project and those of the running services, sorted,
// each once.
func actProjects(project string, running []Service) []string {
	var out []string
	if project != "" {
		out = append(out, project)
	}
	for _, s := range running {
		out = append(out, s.Project)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (d Deps) updates() string { return Dir(setup.StateDir(d.Getenv)) }

// treeFor is ${XDG_DATA_HOME:-~/.local/share}/agentbus/src/<tag> (§3.2).
func (d Deps) treeFor(tag string) string {
	data := d.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(d.Getenv("HOME"), ".local", "share")
	}
	return filepath.Join(data, "agentbus", "src", tag)
}

func (d Deps) env(extra ...string) []string {
	return append(append([]string{}, d.Environ...), extra...)
}

// say writes one line to the person.
func (d Deps) say(format string, a ...any) { fmt.Fprintf(d.Out, format+"\n", a...) }

// Run is agentbus update: exit 0 done (or up to date), 1 refused or failed
// (rolled back when it had changed anything).
func Run(ctx context.Context, d Deps, o Options) int {
	updates := d.updates()
	if !o.Check {
		unlock, err := Lock(updates)
		if err != nil {
			fmt.Fprintln(d.Err, "agentbus update: "+err.Error())
			return 1
		}
		defer unlock()
		if id := Interrupted(updates); id != "" {
			fmt.Fprintf(d.Err, "agentbus update: the update that took snapshot %s did not finish: run agentbus update --rollback %s first\n", id, id)
			return 1
		}
	}
	rel, err := Find(ctx, d.Repo, d.Version)
	switch {
	case errors.Is(err, ErrUpToDate):
		d.say("agentbus %s is up to date", d.Version)
		return 0
	case err != nil:
		fmt.Fprintln(d.Err, "agentbus update: "+err.Error())
		return 1
	}
	running, err := d.Running(ctx)
	if err != nil {
		fmt.Fprintln(d.Err, "agentbus update: the services cannot be read: "+err.Error())
		return 1
	}
	var mine, others []Service
	for _, s := range running {
		if o.Project != "" && s.Project == o.Project {
			mine = append(mine, s)
		} else {
			others = append(others, s)
		}
	}
	acted := func(outcome, detail string) {
		if d.Acted != nil {
			d.Acted(Act{From: d.Version, To: rel.Tag, Outcome: outcome, Detail: d.Version + " -> " + rel.Tag + ", " + detail,
				Projects: actProjects(o.Project, running)})
		}
	}
	tree := d.treeFor(rel.Tag)
	id := NewID(d.Now(), d.Version, rel.Tag)
	changed := skillsChanged(ctx, rel)
	d.plan(ctx, rel, tree, filepath.Join(updates, id), mine, others, changed, o.Project)
	if o.Check {
		return 0
	}
	if !o.Yes {
		if d.TTY == nil {
			fmt.Fprintln(d.Err, "agentbus update: no terminal to ask on: run it in a terminal, or with --yes")
			acted("refused", "not confirmed")
			return 1
		}
		ok, err := setup.Ask(setup.NewOutput(d.Out, d.Err, false), &setup.Prompter{TTY: d.TTY}, "Install "+rel.Tag+"?", false)
		if err != nil || !ok {
			d.say("nothing installed")
			acted("refused", "not confirmed")
			return 1
		}
	}
	u := &updater{d: d, rel: rel, tree: tree, id: id, dir: filepath.Join(updates, id), mine: mine}
	if err := u.stage(ctx); err != nil {
		fmt.Fprintln(d.Err, "agentbus update: "+err.Error()+" (nothing outside "+tree+" changed)")
		acted("failed", "not staged")
		return 1
	}
	defer os.RemoveAll(u.tmp)
	if err := u.snapshot(ctx); err != nil {
		fmt.Fprintln(d.Err, "agentbus update: snapshot: "+err.Error()+" (nothing changed)")
		acted("failed", "no snapshot")
		return 1
	}
	if err := u.apply(ctx); err != nil {
		fmt.Fprintln(d.Err, "agentbus update: "+err.Error()+": rolling back")
		rerr := Rollback(ctx, d, u.dir, u.m, u.restarted)
		if herr := AppendHistory(updates, HistoryLine{Kind: "update", From: d.Version, To: rel.Tag, At: d.Now().Format(time.RFC3339), OK: false, Snapshot: id}); herr != nil {
			fmt.Fprintln(d.Err, "agentbus update: the history line cannot be written ("+herr.Error()+"): the next agentbus update names snapshot "+id+" as unfinished")
		}
		if rerr != nil {
			fmt.Fprintln(d.Err, "agentbus update: the rollback failed too: "+rerr.Error())
			for _, c := range manualCommands(u.dir, u.m) {
				fmt.Fprintln(d.Err, "  "+c)
			}
			acted("failed", "the rollback failed, snapshot "+id)
			return 1
		}
		d.say("rolled back to %s", d.Version)
		acted("failed", "rolled back")
		return 1
	}
	if err := AppendHistory(updates, HistoryLine{Kind: "update", From: d.Version, To: rel.Tag, At: d.Now().Format(time.RFC3339), OK: true, Snapshot: id}); err != nil {
		// unrecorded, the snapshot would read as an interrupted update: no success
		fmt.Fprintln(d.Err, "agentbus update: the history line cannot be written ("+err.Error()+"): rolling back")
		if rerr := Rollback(ctx, d, u.dir, u.m, u.restarted); rerr != nil {
			fmt.Fprintln(d.Err, "agentbus update: the rollback failed too: "+rerr.Error())
			for _, c := range manualCommands(u.dir, u.m) {
				fmt.Fprintln(d.Err, "  "+c)
			}
			acted("failed", "the rollback failed, snapshot "+id)
			return 1
		}
		d.say("rolled back to %s; the next agentbus update names snapshot %s as unfinished until its history can be written", d.Version, id)
		acted("failed", "rolled back")
		return 1
	}
	d.say("agentbus %s -> %s: installed; snapshot %s (agentbus update --rollback undoes it)", d.Version, rel.Tag, id)
	acted("ok", "installed, snapshot "+id)
	if len(changed) > 0 {
		c := strings.Join(changed, " ")
		d.say("skills changed: %s  (agents to tell: agentbus roles skill-users %s)", c, c)
	}
	for _, s := range others {
		d.say("not restarted: %s %s (%s): agentbus --project %s service restart %s", s.Project, s.Kind, buildOr(s.Build), s.Project, s.Kind)
	}
	if err := Prune(updates); err != nil {
		fmt.Fprintln(d.Err, "agentbus update: an old snapshot could not be removed: "+err.Error())
	}
	PruneTrees(ctx, d, rel.Tag) // after the snapshots: a tree only they named is free now
	return 0
}

func buildOr(b string) string {
	if b == "" {
		return "no build recorded"
	}
	return b
}

// plan is §2's text.
func (d Deps) plan(ctx context.Context, rel Release, tree, snap string, mine, others []Service, changed []string, project string) {
	notes := ""
	if _, err := git(ctx, "--git-dir", rel.GitDir, "cat-file", "-e", rel.Tag+":docs/releases/"+rel.Tag+".md"); err == nil {
		notes = "  (release notes: " + filepath.Join(tree, "docs", "releases", rel.Tag+".md") + ")"
	}
	d.say("agentbus %s -> %s%s", d.Version, rel.Tag, notes)
	d.say("  build from   %s  (your clone is not touched)", tree)
	d.say("  snapshot     %s", snap)
	switch {
	case project == "":
		d.say("  restart      nothing (--project <p> names the project whose dispatch and missions restart)")
	case len(mine) == 0:
		d.say("  restart      nothing: no dispatch or missions of %s runs here", project)
	default:
		var names []string
		for _, s := range mine {
			names = append(names, s.Project+" "+s.Kind)
		}
		d.say("  restart      %s", strings.Join(names, ", "))
	}
	for _, s := range others {
		d.say("  not touched  %s %s (%s): agentbus --project %s service restart %s", s.Project, s.Kind, buildOr(s.Build), s.Project, s.Kind)
	}
	if len(changed) > 0 {
		c := strings.Join(changed, " ")
		d.say("  skills changed: %s  (agents to tell: agentbus roles skill-users %s)", c, c)
	}
}

// skillsChanged is setup update's skills diff: the skills/ folders that
// differ between the running build and the tag (setup.SkillsChanged).
func skillsChanged(ctx context.Context, rel Release) []string {
	out, err := git(ctx, "--git-dir", rel.GitDir, "diff", "--name-only", rel.FromRev, rel.Tag, "--", "skills/")
	if err != nil {
		return nil
	}
	return setup.SkillsChanged(out)
}

// updater is one update in progress.
type updater struct {
	d         Deps
	rel       Release
	tree, id  string
	dir       string // the snapshot
	tmp       string
	in        *setup.Installer
	mine      []Service
	m         Manifest
	restarted []Service
}

// stage is §3.4 step 3: the tree, then both builds into the temp dir, then
// each new binary's version. Nothing outside the tree changes.
func (u *updater) stage(ctx context.Context) error {
	clone := mission.Git{Repo: filepath.Dir(u.rel.GitDir)}
	want, err := git(ctx, "--git-dir", u.rel.GitDir, "rev-parse", u.rel.Tag+"^{commit}")
	if err != nil {
		return err
	}
	if _, err := os.Stat(u.tree); err == nil {
		if dirty, err := clone.Dirty(ctx, u.tree); err != nil || dirty {
			return fmt.Errorf("the release tree %s has changes (or cannot be read): clean it or remove it", u.tree)
		}
		if head, err := clone.Head(ctx, u.tree); err != nil || head != want {
			return fmt.Errorf("the release tree %s is not at %s: remove it, and run agentbus update again", u.tree, u.rel.Tag)
		}
	} else if err := clone.AddWorktree(ctx, u.tree, u.rel.Tag, true); err != nil {
		return fmt.Errorf("the release tree %s: %w", u.tree, err)
	}
	tmp, err := os.MkdirTemp("", "agentbus-update-")
	if err != nil {
		return err
	}
	u.tmp = tmp
	u.in = &setup.Installer{R: u.d.R, O: setup.NewOutput(u.d.Out, u.d.Err, false),
		Opt: setup.Options{BinDir: u.d.BinDir, InstallSkills: true}, Repo: u.tree, Tmp: tmp, Rev: u.rel.Tag, Getenv: u.d.Getenv,
		SkillsEnv: []string{"AGENTBUS_BIN=" + filepath.Join(u.tree, "agentbus"), "AGENTBUS_REPO=" + u.tree, "ROLES_TOML=" + filepath.Join(u.tree, "roles.toml")}}
	for _, b := range []struct{ name, pkg, flag string }{{"agentbus", "./cmd/agentbus", "version"}, {"busmon", "./cmd/busmon", "--version"}} {
		built, err := u.in.Build(ctx, b.name, b.pkg)
		if err != nil {
			return err
		}
		if out, err := u.d.R.Run(ctx, setup.Cmd{Name: built, Args: []string{b.flag}}); err != nil {
			return fmt.Errorf("the new %s does not run (%v): %s", b.name, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// skillPlan is the tree's role skill directories and links in env
// (update-old-binary): asked of the release's own agentbus, built in u.tmp,
// which knows its roles.toml (roles skill-plan --json, read leniently), so
// a key added by the release never stops the update. A new binary without
// that command (an older release) leaves this one to read the tree itself.
func (u *updater) skillPlan(ctx context.Context, env []string) (rolecfg.SkillPlan, error) {
	manifest := filepath.Join(u.tree, "roles.toml")
	out, err := u.d.R.Run(ctx, setup.Cmd{Name: filepath.Join(u.tmp, "agentbus"), Args: []string{"roles", "skill-plan", "--json"},
		Env: append(append([]string{}, env...), "ROLES_TOML="+manifest), Stdout: true})
	if err == nil {
		p, err := decodeSkillPlan(out)
		if err != nil {
			return rolecfg.SkillPlan{}, fmt.Errorf("the new agentbus's skill plan: %w", err)
		}
		return p, nil
	}
	m, lerr := rolecfg.Load(manifest)
	if lerr != nil {
		return rolecfg.SkillPlan{}, fmt.Errorf("%s: %w (the new agentbus could not read it either: %v)", manifest, lerr, err)
	}
	return rolecfg.PlanSkills(m, env)
}

// decodeSkillPlan reads roles skill-plan --json: an object whose dirs is an
// array and whose links is an array (or null with links_error set); a field
// added later is ignored. A null, an empty object or a missing field is an
// error, never read as no skills (reviewer-codex).
func decodeSkillPlan(b []byte) (rolecfg.SkillPlan, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil || raw == nil {
		return rolecfg.SkillPlan{}, fmt.Errorf("not a JSON object: %q", b)
	}
	var p rolecfg.SkillPlan
	if err := json.Unmarshal(b, &p); err != nil {
		return rolecfg.SkillPlan{}, err
	}
	if d, ok := raw["dirs"]; !ok || string(d) == "null" {
		return rolecfg.SkillPlan{}, errors.New("no dirs")
	}
	if l, ok := raw["links"]; !ok || (string(l) == "null" && p.LinksError == "") {
		return rolecfg.SkillPlan{}, errors.New("no links and no links_error")
	}
	return p, nil
}

// snapshot is §3.4 step 4: what the rollback needs, read now, written before
// any change.
func (u *updater) snapshot(ctx context.Context) error {
	d := u.d
	m := Manifest{From: d.Version, FromBuild: d.FromBuild, To: u.rel.Tag, BinDir: d.BinDir, FromTree: d.Repo, NewTree: u.tree,
		Project: "", Services: u.mine}
	if len(u.mine) > 0 {
		m.Project = u.mine[0].Project
	}
	if h, err := git(ctx, "-C", d.Repo, "rev-parse", "HEAD"); err == nil {
		m.FromHead = h
	}
	if t, err := os.Readlink(filepath.Join(d.BinDir, "agent-launch")); err == nil {
		m.AgentLaunch = t
	}
	home := setup.SkillsHome(d.Getenv)
	m.Skills = SkillsClone{Home: home}
	if _, err := os.Stat(home); err != nil {
		m.Skills.Absent = true
	} else {
		m.Skills.Ours = setup.SkillsOurs(home)
		if h, err := git(ctx, "-C", home, "rev-parse", "HEAD"); err == nil {
			m.Skills.Head = h
		}
	}
	plan, err := u.skillPlan(ctx, d.env())
	if err != nil {
		return err
	}
	m.SkillDirs = plan.Dirs
	m.SkillLinks = linksIn(m.SkillDirs)
	u.m = m
	return WriteSnapshot(u.dir, m)
}

// linksIn are the symlinks in dirs, by path.
func linksIn(dirs []string) []Link {
	var out []Link
	for _, dir := range dirs {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			p := filepath.Join(dir, e.Name())
			if t, err := os.Readlink(p); err == nil {
				out = append(out, Link{Path: p, Target: t})
			}
		}
	}
	return out
}

// relink points link at target: a symlink beside it, then a rename.
func relink(link, target string) error {
	tmp := link + ".agentbus-update"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// apply is §3.4 steps 5 and 6: binaries, agent-launch, skills, services.
// Any error is rolled back by the caller.
func (u *updater) apply(ctx context.Context) error {
	d := u.d
	for _, name := range []string{"agentbus", "busmon"} { // agentbus first: an old busmon runs the agentbus beside it
		built := filepath.Join(u.tmp, name)
		for _, dst := range []string{filepath.Join(d.BinDir, name), filepath.Join(u.tree, name)} {
			if err := setup.InstallFile(built, dst); err != nil {
				return fmt.Errorf("install %s: %w", dst, err)
			}
		}
	}
	link := filepath.Join(d.BinDir, "agent-launch")
	if setup.OurScriptLink(link, d.Repo) {
		if err := relink(link, filepath.Join(u.tree, "scripts", "agent-launch")); err != nil {
			return fmt.Errorf("agent-launch: %w", err)
		}
	}
	if err := u.in.ExternalSkillsStrict(ctx); err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	if err := u.in.RoleSkillsStrict(ctx); err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	if err := u.checkSkills(ctx); err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	for _, s := range u.mine {
		u.restarted = append(u.restarted, s)
		if err := restartGreen(ctx, d, s); err != nil {
			return err
		}
	}
	return nil
}

// checkSkills: the external clone is at its pin (when setup made it), and
// every role skill link resolves to its source with the new tree as the
// repository.
func (u *updater) checkSkills(ctx context.Context) error {
	d := u.d
	home := setup.SkillsHome(d.Getenv)
	if setup.SkillsOurs(home) {
		head, err1 := git(ctx, "-C", home, "rev-parse", "HEAD")
		pin, err2 := git(ctx, "-C", home, "rev-parse", setup.SkillsRef(d.Getenv)+"^{commit}")
		if err1 != nil || err2 != nil || head != pin {
			return fmt.Errorf("the external skills in %s are not at %s", home, setup.SkillsRef(d.Getenv))
		}
	}
	plan, err := u.skillPlan(ctx, d.env("AGENTBUS_REPO="+u.tree))
	if err != nil {
		return err
	}
	if plan.LinksError != "" {
		return errors.New(plan.LinksError)
	}
	for _, l := range plan.Links {
		r, err := filepath.EvalSymlinks(l.Link)
		src, serr := filepath.EvalSymlinks(l.Source)
		if err != nil || serr != nil || r != src { // both resolved: a data dir behind a symlink is the same place
			return fmt.Errorf("%s does not resolve to %s", l.Link, l.Source)
		}
	}
	return nil
}

// restartGreen restarts one service with the bin dir's agentbus, and waits
// for it to be green (§3.4 step 6): the unit active, its record written by
// its own PID (Running lists it with a build), and that agentbus no longer
// listing it as stale (the build it recorded is that agentbus's). A unit
// that crashed, exited or failed is not active, so never green, although
// service stale drops it (foureyes).
func restartGreen(ctx context.Context, d Deps, s Service) error {
	ab := filepath.Join(d.BinDir, "agentbus")
	if out, err := d.R.Run(ctx, setup.Cmd{Name: ab, Args: []string{"--project", s.Project, "service", "restart", s.Kind}}); err != nil {
		return fmt.Errorf("%s %s: restart failed (%v): %s", s.Project, s.Kind, err, strings.TrimSpace(string(out)))
	}
	deadline := d.Now().Add(d.Settle)
	why := "is not running"
	for {
		if active, own := isRunning(ctx, d, s); !active {
			why = "is not running"
		} else if !own {
			why = "has not recorded its build from its own process"
		} else if out, err := d.R.Run(ctx, setup.Cmd{Name: ab, Args: []string{"service", "stale"}, Stdout: true}); err != nil {
			why = "cannot be judged (service stale: " + err.Error() + ")"
		} else if listsService(string(out), s) {
			why = "still runs another build"
		} else {
			return nil
		}
		if !d.Now().Before(deadline) {
			return fmt.Errorf("%s %s did not record the new build within %s: it %s", s.Project, s.Kind, d.Settle, why)
		}
		d.Sleep(2 * time.Second)
	}
}

// isRunning: s's unit is active here, and its record is its own process's
// (Running gives it a build).
func isRunning(ctx context.Context, d Deps, s Service) (active, own bool) {
	all, err := d.Running(ctx)
	if err != nil {
		return false, false
	}
	for _, r := range all {
		if r.Project == s.Project && r.Kind == s.Kind {
			return true, r.Build != ""
		}
	}
	return false, false
}

// listsService: service stale's "<project> <kind> <build>" lines name s.
func listsService(out string, s Service) bool {
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == s.Project && f[1] == s.Kind {
			return true
		}
	}
	return false
}

// Rollback restores the snapshot in dir (§3.5): busmon then agentbus, the
// agent-launch link, the skills link by link and the external clone, then
// the services the update restarted, each back on the from build. Holders,
// agents, trees and the broker are never touched.
func Rollback(ctx context.Context, d Deps, dir string, m Manifest, restarted []Service) error {
	for _, name := range []string{"busmon", "agentbus"} {
		if err := setup.InstallFile(filepath.Join(dir, "bin", name), filepath.Join(m.BinDir, name)); err != nil {
			return fmt.Errorf("restore %s: %w", name, err)
		}
	}
	link := filepath.Join(m.BinDir, "agent-launch")
	cur, curErr := os.Readlink(link)
	switch {
	case m.AgentLaunch != "" && cur != m.AgentLaunch:
		if err := relink(link, m.AgentLaunch); err != nil {
			return fmt.Errorf("agent-launch: %w", err)
		}
	case m.AgentLaunch == "" && curErr == nil && strings.HasPrefix(cur, m.NewTree+string(filepath.Separator)):
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("agent-launch: %w", err)
		}
	}
	if err := restoreSkills(ctx, m); err != nil {
		return err
	}
	for _, s := range restarted {
		if err := restartGreen(ctx, d, s); err != nil {
			return err
		}
	}
	return nil
}

// restoreSkills puts each recorded link back, removes a link the release
// added (one into the new tree), leaves any other link as the person's, then
// puts the external clone back, and checks.
func restoreSkills(ctx context.Context, m Manifest) error {
	recorded := map[string]string{}
	for _, l := range m.SkillLinks {
		recorded[l.Path] = l.Target
	}
	for _, l := range linksIn(m.SkillDirs) {
		want, ok := recorded[l.Path]
		switch {
		case ok && l.Target != want:
			if err := relink(l.Path, want); err != nil {
				return fmt.Errorf("skill %s: %w", l.Path, err)
			}
		case !ok && strings.HasPrefix(l.Target, m.NewTree+string(filepath.Separator)):
			if err := os.Remove(l.Path); err != nil {
				return fmt.Errorf("skill %s: %w", l.Path, err)
			}
		}
	}
	for _, l := range m.SkillLinks {
		if _, err := os.Lstat(l.Path); os.IsNotExist(err) {
			if err := relink(l.Path, l.Target); err != nil {
				return fmt.Errorf("skill %s: %w", l.Path, err)
			}
		}
	}
	home := m.Skills.Home
	switch {
	case m.Skills.Absent && setup.SkillsOurs(home):
		if err := os.RemoveAll(home); err != nil {
			return fmt.Errorf("external skills: %w", err)
		}
	case m.Skills.Ours && m.Skills.Head != "":
		if _, err := git(ctx, "-C", home, "checkout", "--quiet", m.Skills.Head); err != nil {
			return fmt.Errorf("external skills: %w", err)
		}
	}
	for _, l := range m.SkillLinks {
		if t, err := os.Readlink(l.Path); err != nil || t != l.Target {
			return fmt.Errorf("skill %s is not back on %s", l.Path, l.Target)
		}
	}
	if m.Skills.Ours && m.Skills.Head != "" {
		if h, err := git(ctx, "-C", home, "rev-parse", "HEAD"); err != nil || h != m.Skills.Head {
			return fmt.Errorf("external skills in %s are not back on %s", home, m.Skills.Head)
		}
	}
	return nil
}

// manualCommands are the rollback by hand, from the manifest (§3.5).
func manualCommands(dir string, m Manifest) []string {
	out := []string{
		"cp " + filepath.Join(dir, "bin", "busmon") + " " + filepath.Join(m.BinDir, "busmon"),
		"cp " + filepath.Join(dir, "bin", "agentbus") + " " + filepath.Join(m.BinDir, "agentbus"),
	}
	if m.AgentLaunch != "" {
		out = append(out, "ln -sfn "+m.AgentLaunch+" "+filepath.Join(m.BinDir, "agent-launch"))
	}
	for _, l := range m.SkillLinks {
		out = append(out, "ln -sfn "+l.Target+" "+l.Path)
	}
	if m.Skills.Ours && m.Skills.Head != "" {
		out = append(out, "git -C "+m.Skills.Home+" checkout "+m.Skills.Head)
	}
	for _, s := range m.Services {
		out = append(out, "agentbus --project "+s.Project+" service restart "+s.Kind)
	}
	return out
}

// heldEnv marks a restore --rollback started while it holds the update lock
// for the whole sequence (reviewer-codex): the restore then runs under that
// lock instead of taking its own.
const heldEnv = "AGENTBUS_UPDATE_LOCK_HELD"

// Restore is agentbus update --restore <id>, run by the binary that wrote
// the snapshot (--rollback starts it): the rollback of §3.5, then its
// history line. Started by --rollback (heldEnv), it runs only while that
// lock is held; on its own, it takes the lock.
func Restore(ctx context.Context, d Deps, id string) int {
	updates := d.updates()
	unlock, err := Lock(updates)
	switch {
	case d.Getenv(heldEnv) != "" && errors.Is(err, ErrLocked): // --rollback holds it
	case d.Getenv(heldEnv) != "" && err == nil:
		unlock()
		fmt.Fprintln(d.Err, "agentbus update: a restore for --rollback, but the update lock is not held: run agentbus update --rollback")
		return 1
	case err != nil:
		fmt.Fprintln(d.Err, "agentbus update: "+err.Error())
		return 1
	default:
		defer unlock()
	}
	dir := filepath.Join(updates, id)
	m, err := ReadManifest(dir)
	if err != nil {
		fmt.Fprintln(d.Err, "agentbus update: "+err.Error())
		return 1
	}
	err = Rollback(ctx, d, dir, m, m.Services)
	if herr := AppendHistory(updates, HistoryLine{Kind: "rollback", From: m.To, To: m.From, At: d.Now().Format(time.RFC3339), OK: err == nil, Snapshot: id}); herr != nil {
		fmt.Fprintln(d.Err, "agentbus update: the history line cannot be written: "+herr.Error())
	}
	if err != nil {
		fmt.Fprintln(d.Err, "agentbus update: the rollback failed: "+err.Error())
		for _, c := range manualCommands(dir, m) {
			fmt.Fprintln(d.Err, "  "+c)
		}
		return 1
	}
	d.say("rolled back %s -> %s (snapshot %s)", m.To, m.From, id)
	return 0
}

// RollbackTo is agentbus update --rollback [<id>]: the person's yes, then
// each snapshot from the last down to id, newest first, restored by its own
// bin/agentbus (update --restore), stopping at the first that fails.
func RollbackTo(ctx context.Context, d Deps, id string, yes bool) int {
	updates := d.updates()
	unlock, err := Lock(updates) // held across every restore: no update runs between two
	if err != nil {
		fmt.Fprintln(d.Err, "agentbus update: "+err.Error())
		return 1
	}
	defer unlock()
	ids := Live(updates)
	if len(ids) == 0 {
		fmt.Fprintln(d.Err, "agentbus update: no installed update to roll back (agentbus update --list)")
		return 1
	}
	if id == "" {
		id = ids[len(ids)-1]
	}
	from := -1
	for i, s := range ids {
		if s == id {
			from = i
		}
	}
	if from < 0 {
		fmt.Fprintln(d.Err, "agentbus update: "+id+" is no installed update (a failed one rolled itself back; agentbus update --list)")
		return 1
	}
	todo := append([]string{}, ids[from:]...)
	for i, j := 0, len(todo)-1; i < j; i, j = i+1, j-1 {
		todo[i], todo[j] = todo[j], todo[i]
	}
	for _, s := range todo {
		m, err := ReadManifest(filepath.Join(updates, s))
		if err != nil {
			fmt.Fprintln(d.Err, "agentbus update: "+err.Error())
			return 1
		}
		d.say("roll back %s -> %s (snapshot %s)", m.To, m.From, s)
	}
	if !yes {
		if d.TTY == nil {
			fmt.Fprintln(d.Err, "agentbus update: no terminal to ask on: run it in a terminal, or with --yes")
			return 1
		}
		ok, err := setup.Ask(setup.NewOutput(d.Out, d.Err, false), &setup.Prompter{TTY: d.TTY}, "Roll back?", false)
		if err != nil || !ok {
			d.say("nothing rolled back")
			return 1
		}
	}
	for _, s := range todo {
		out, err := d.R.Run(ctx, setup.Cmd{Name: filepath.Join(updates, s, "bin", "agentbus"), Args: []string{"update", "--restore", s}, Env: []string{heldEnv + "=1"}})
		if len(out) > 0 {
			fmt.Fprint(d.Out, string(out))
		}
		if err != nil {
			fmt.Fprintf(d.Err, "agentbus update: the rollback of %s failed (%v); the later ones were rolled back\n", s, err)
			return 1
		}
	}
	return 0
}

// List is agentbus update --list: the snapshots, oldest first, with their
// versions and how each ended.
func List(d Deps) int {
	updates := d.updates()
	hist := History(updates)
	ids := Snapshots(updates)
	if len(ids) == 0 {
		d.say("no snapshot (%s)", updates)
		listTrees(context.Background(), d)
		return 0
	}
	for _, id := range ids {
		m, err := ReadManifest(filepath.Join(updates, id))
		state := "unfinished"
		for _, h := range hist {
			if h.Snapshot == id {
				state = h.Kind + map[bool]string{true: " ok", false: " failed"}[h.OK]
			}
		}
		if err != nil {
			d.say("%s  %v", id, err)
			continue
		}
		d.say("%s  %s -> %s  %s", id, m.From, m.To, state)
	}
	listTrees(context.Background(), d)
	return 0
}

// listTrees is List's release trees, each with why it is kept.
func listTrees(ctx context.Context, d Deps) {
	for _, t := range Trees(ctx, d, installedTag(ctx, d)) {
		why := t.Why
		if why == "" {
			why = "nothing needs it: the next update removes it"
		}
		d.say("tree %s  %s", t.Path, why)
	}
}
