package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/trust"
)

// startPlan is what agentbus start deduces before it shows the assistant
// (agentbus-start spec, "What is deduced, and from where"). A field the
// deduction could not settle has its Ask set: the assistant asks it, --yes
// refuses with it.
type startPlan struct {
	Folder string // the Git root (a linked worktree's main checkout), else the folder

	Project    string
	ProjectWhy string
	ProjectAsk string // why the name must be asked; Project is then ""
	Template   string // the project's template path
	Existing   bool   // a template on this folder names the project

	Name         string
	NameWhy      string
	NameRecorded bool   // identity.as is in a file: start writes no name
	NameAsk      string // why the name must be asked; Name is then ""

	Team    []rolecfg.TeamRole
	TeamWhy string
	TeamAsk string // why the team must be asked again (a profile no longer allowed)

	Notes []string // what the screen says of a choice start made for the person
}

// startProjectName makes a project name of a folder's base name:
// lower-cased, every run of characters outside [a-z0-9_-] one -, a leading
// non-letter dropped, cut at 32; "" when nothing valid is left.
func startProjectName(base string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(base) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimLeftFunc(b.String(), func(r rune) bool { return r < 'a' || r > 'z' })
	if len(s) > 32 {
		s = s[:32]
	}
	if !bus.ValidName(s) {
		return ""
	}
	return s
}

// deduceStart deduces start's values for the folder at path, with m the
// role manifest. Only a folder that does not exist (or is no directory) is
// an error: anything else unsettled is asked.
func deduceStart(path string, getenv func(string) string, m *rolecfg.Manifest) (startPlan, error) {
	var p startPlan
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return p, fmt.Errorf("%s is not a directory", path)
	}
	folder, ok := canonDir(path)
	if !ok {
		return p, fmt.Errorf("the folder %s cannot be resolved", path)
	}
	p.Folder = folder // the Git root; a linked worktree's main checkout
	rows, _ := projlist.Templates(rolecfg.ProjectsDir(getenv))

	// The project: a template on this folder, else the folder's base name.
	var here []projlist.Row
	taken := map[string]bool{}
	for _, r := range rows {
		taken[r.Name] = true
		if dir, ok := canonDir(expandHome(r.WorkingDir, getenv("HOME"))); ok && dir == p.Folder {
			here = append(here, r)
		}
	}
	switch len(here) {
	case 1:
		p.Project, p.ProjectWhy, p.Existing, p.Template = here[0].Name, "its template", true, here[0].Path
	case 0:
		name := startProjectName(filepath.Base(p.Folder))
		if name == "" {
			p.ProjectAsk = fmt.Sprintf("%q gives no valid project name", filepath.Base(p.Folder))
			break
		}
		p.Project, p.ProjectWhy = name, "from the folder's name"
		if taken[name] {
			for n := 2; ; n++ {
				suffix := fmt.Sprintf("-%d", n)
				cand := name
				if len(cand)+len(suffix) > 32 {
					cand = cand[:32-len(suffix)]
				}
				if cand += suffix; !taken[cand] {
					p.Notes = append(p.Notes, fmt.Sprintf("%s is another folder's project: %s instead", name, cand))
					p.Project = cand
					break
				}
			}
		}
		p.Template = filepath.Join(rolecfg.ProjectsDir(getenv), p.Project+".toml")
		// projlist skips a template it cannot read: never write over one.
		if _, err := os.Stat(p.Template); err == nil {
			var tpl upTemplate
			if _, err := toml.DecodeFile(p.Template, &tpl); err != nil {
				return p, fmt.Errorf("reading %s: %v", p.Template, err)
			}
		}
	default:
		var names []string
		for _, r := range here {
			names = append(names, r.Name)
		}
		p.ProjectAsk = "two templates name this folder: " + strings.Join(names, " and ")
	}

	// The person's name: recorded, else $USER made a name.
	if name, from := personName(getenv); name != "" {
		p.Name, p.NameWhy, p.NameRecorded = name, "recorded ("+from+")", true
	} else if name := startProjectName(getenv("USER")); name != "" {
		p.Name, p.NameWhy = name, "from $USER (not recorded yet)"
	} else {
		p.NameAsk = "$USER gives no valid name"
	}

	// The team: the template's agent tabs, else start.team, else every boot
	// role on its own profile.
	switch {
	case p.Existing:
		team, stale, err := templateTeam(p.Template, m)
		if err != nil {
			return p, err
		}
		p.Team, p.TeamWhy = team, "its template"
		if len(stale) > 0 {
			p.TeamAsk = strings.Join(stale, ", ") + ": no longer allowed by roles.toml"
		}
	default:
		r := config.Resolver{Files: []*config.File{config.Load(config.Path(getenv), "config.toml")}}
		if spec, _ := r.Get("start.team").Value.(string); strings.TrimSpace(spec) != "" {
			p.Team, p.Notes = defaultTeam(m, spec, p.Notes)
			p.TeamWhy = "your default team (start.team)"
		} else {
			for _, role := range m.RolesByTier("boot") {
				p.Team = append(p.Team, rolecfg.TeamRole{Role: role})
			}
			p.TeamWhy = "every boot role"
		}
	}
	return p, nil
}

// templateTeam is a template's agent tabs, in its order, each on the
// --profile it names; a role roles.toml does not have is left out (up skips
// it too). stale names each tab whose profile roles.toml no longer allows
// (launchProfile): that role is in the team on its own profile, to be asked
// again. A template that cannot be read is an error, never an empty team.
func templateTeam(path string, m *rolecfg.Manifest) (team []rolecfg.TeamRole, stale []string, err error) {
	var tpl upTemplate
	if _, err := toml.DecodeFile(path, &tpl); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %v", path, err)
	}
	for _, t := range tpl.Tabs {
		_, role, profile := tabLaunch(t.Command)
		if _, known := m.Roles[role]; !known {
			continue
		}
		if _, err := launchProfile(m, role, profile); err != nil {
			stale = append(stale, fmt.Sprintf("%s's profile %s", role, profile))
			profile = ""
		}
		team = append(team, rolecfg.TeamRole{Role: role, Profile: profile})
	}
	return team, stale, nil
}

// canonDir is a folder as start compares folders: absolute, its symlinks
// resolved, then its Git root (a linked worktree's main checkout). bootstrap
// records the logical path (cd ... && pwd), which may be a link's (foureyes,
// reviewer-codex). false for a folder that no longer exists.
func canonDir(dir string) (string, bool) {
	abs, err := filepath.Abs(dir)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		return "", false
	}
	root, _ := trust.Key(abs)
	return root, true
}

// defaultTeam reads start.team item by item against roles.toml: a role that
// is no longer a boot role is dropped, a profile no longer allowed set back to
// the role's own, master added back when missing, each said.
func defaultTeam(m *rolecfg.Manifest, spec string, notes []string) ([]rolecfg.TeamRole, []string) {
	allowed := func(item string) bool { _, err := m.ParseTeam(item); return err == nil }
	var keep []string
	seen := map[string]bool{}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		role, _, _ := strings.Cut(item, ":")
		switch {
		case seen[role]:
			continue
		case allowed(item):
			keep = append(keep, item)
		case allowed(role):
			notes = append(notes, fmt.Sprintf("start.team: %s's profile is no longer allowed: its own instead", role))
			keep = append(keep, role)
		default:
			notes = append(notes, fmt.Sprintf("start.team: %s is no longer a boot role: dropped", role))
			continue
		}
		seen[role] = true
	}
	if !seen["master"] {
		keep = append(keep, "master")
		notes = append(notes, "start.team: master added back (the escalation target and the pilot)")
	}
	team, _ := m.ParseTeam(strings.Join(keep, ",")) // every item checked above
	return team, notes
}

// expandHome is a template's working_dir with its ~ expanded, as up reads it.
func expandHome(dir, home string) string {
	return os.ExpandEnv(strings.Replace(dir, "~", home, 1))
}
