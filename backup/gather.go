package backup

import (
	"errors"
	"fmt"
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

// notifyName is the notification targets' file (notifications spec): its
// topics, tokens and webhooks are secrets, kept only in secrets.age.
const notifyName = "notify.toml"

// notifyFile is notify.toml beside config.toml ("" without a config dir).
func (loc Locations) notifyFile() string {
	if loc.ConfigDir == "" {
		return ""
	}
	return filepath.Join(loc.ConfigDir, notifyName)
}

// configFiles are the person's settings kept in an archive.
var configFiles = []string{"config.toml", "busmon.toml", "quota.toml", "models.toml"}

const secretWhy = "secret (use --secrets)"

// Gather lists the files a run archives for projects, and the secrets it
// leaves out (spec "What an archive holds", "Secrets"). A secret is only
// stat'ed, never opened. Logs, restart budgets and the cache are never
// listed. A source missing when the run reads it is skipped. A history
// folder that cannot be walked is an error (reviewer-codex): an archive never
// silently lacks part of a history.
func Gather(projects []string, loc Locations) (files []Source, left []LeftOut, notes []string, err error) {
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
		if len(manifests) == 0 && exists(tpl) {
			notes = append(notes, p+": its template names no ROLES_TOML: agent-launch reads the checkout's roles.toml, which is in Git, not in this archive")
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
		hist, err := historyFiles(p, loc.StateDir(p))
		if err != nil {
			return nil, nil, nil, err
		}
		files = append(files, hist...)
	}
	if exists(loc.PasswordFile) {
		left = append(left, LeftOut{Path: "redis.env", Why: secretWhy})
	}
	if exists(loc.notifyFile()) {
		left = append(left, LeftOut{Path: notifyName, Why: secretWhy})
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
	return present, left, notes, nil
}

// templateManifests reads the ROLES_TOML each tab's command names, in tab
// order, once each. An unreadable template names none.
func templateManifests(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return manifestsOf(b)
}

// manifestsOf is templateManifests of a template's text.
func manifestsOf(template []byte) []string {
	var t struct {
		Tabs []struct {
			Command string `toml:"command"`
		} `toml:"tabs"`
	}
	if _, err := toml.Decode(string(template), &t); err != nil {
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
// gaps.jsonl, cursor.json), its lock and other dot files left out. No
// history folder is none; any other walk error is returned.
func historyFiles(project, stateDir string) ([]Source, error) {
	root := filepath.Join(stateDir, "history")
	var out []Source
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return err
		}
		if path == root {
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
			return err
		}
		out = append(out, Source{Member: "state/" + project + "/history/" + filepath.ToSlash(rel), Path: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s's history: %w", project, err)
	}
	return out, nil
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
