package backup

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// Locations are where a person's files live, resolved by the caller from its
// environment.
type Locations struct {
	ConfigDir    string                      // <config>/agentbus: config.toml, busmon.toml, quota.toml, models.toml
	ProjectsDir  string                      // the project templates (rolecfg.ProjectsDir)
	PasswordFile string                      // the broker's password file (bus.PasswordFile): a secret
	EnvFile      func(project string) string // a project's services env file: a secret
	StateDir     func(project string) string // a project's state directory
}

// configFiles are the person's settings kept in an archive.
var configFiles = []string{"config.toml", "busmon.toml", "quota.toml", "models.toml"}

const secretWhy = "secret (use --secrets)"

// Gather lists the files a run archives for projects, and the secrets it
// leaves out (spec "What an archive holds", "Secrets"). A secret is only
// stat'ed, never opened. Logs, restart budgets and the cache are never
// listed. A source missing when the run reads it is skipped.
func Gather(projects []string, loc Locations) (files []Source, left []LeftOut, notes []string) {
	for _, name := range configFiles {
		files = append(files, Source{Member: "config/" + name, Path: filepath.Join(loc.ConfigDir, name)})
	}
	for _, p := range projects {
		tpl := filepath.Join(loc.ProjectsDir, p+".toml")
		files = append(files, Source{Member: "config/projects/" + p + ".toml", Path: tpl})
		manifests := templateManifests(tpl)
		if len(manifests) > 1 {
			notes = append(notes, p+": its template names "+strings.Join(manifests, ", ")+"; only the first is archived")
		}
		if len(manifests) > 0 {
			m := manifests[0]
			files = append(files, Source{Member: "roles/" + p + "/roles.toml", Path: m})
			prompts, _ := filepath.Glob(filepath.Join(filepath.Dir(m), "roles", "*.md"))
			sort.Strings(prompts)
			for _, md := range prompts {
				files = append(files, Source{Member: "roles/" + p + "/roles/" + filepath.Base(md), Path: md})
			}
		}
		files = append(files, historyFiles(p, loc.StateDir(p))...)
	}
	if exists(loc.PasswordFile) {
		left = append(left, LeftOut{Path: "redis.env", Why: secretWhy})
	}
	for _, p := range projects {
		if exists(loc.EnvFile(p)) {
			left = append(left, LeftOut{Path: "services/" + p + ".env", Why: secretWhy})
		}
	}
	present := files[:0]
	for _, f := range files {
		if exists(f.Path) {
			present = append(present, f)
		}
	}
	return present, left, notes
}

// templateManifests reads the ROLES_TOML each tab's command names, in tab
// order, once each. An unreadable template names none.
func templateManifests(path string) []string {
	var t struct {
		Tabs []struct {
			Command string `toml:"command"`
		} `toml:"tabs"`
	}
	if _, err := toml.DecodeFile(path, &t); err != nil {
		return nil
	}
	var out []string
	for _, tab := range t.Tabs {
		words, err := rolecfg.ShellSplit(tab.Command)
		if err != nil {
			continue
		}
		for _, w := range words {
			if m, ok := strings.CutPrefix(w, "ROLES_TOML="); ok && m != "" && !contains(out, m) {
				out = append(out, m)
			}
		}
	}
	return out
}

// historyFiles are the history archive's regular files (day files,
// gaps.jsonl, cursor.json), its lock and other dot files left out.
func historyFiles(project, stateDir string) []Source {
	root := filepath.Join(stateDir, "history")
	var out []Source
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, Source{Member: "state/" + project + "/history/" + filepath.ToSlash(rel), Path: path})
		return nil
	})
	return out
}

func exists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Lstat(path)
	return err == nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
