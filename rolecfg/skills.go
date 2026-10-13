package rolecfg

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type skillLink struct {
	target string
	source string
}

// InstallSkills installs role skills as symlinks in each provider's standard
// skill directory, or in SKILLS_DEST when supplied. A missing skill is an error.
func InstallSkills(m *Manifest, env []string, stdout io.Writer) error {
	return installSkills(m, env, stdout, nil)
}

// InstallSkillsMissingOK links what exists and warns on stderr for a skill that
// is not installed: the agent starts without it, as the launcher allows
// (bootstrap, so a failed external-skills download never stops a first run).
func InstallSkillsMissingOK(m *Manifest, env []string, stdout, stderr io.Writer) error {
	return installSkills(m, env, stdout, stderr)
}

func installSkills(m *Manifest, env []string, stdout, missing io.Writer) error {
	if m == nil {
		return fmt.Errorf("manifest is required")
	}
	var links []skillLink
	indices := make(map[string]int)
	for _, role := range m.roleOrder {
		profile, err := m.Resolve(role)
		if err != nil {
			return err
		}
		dest, err := skillDestination(profile.Client, env)
		if err != nil {
			return err
		}
		names := profile.Skills
		if missing != nil {
			names = presentSkills(role, names, env, missing)
		}
		sources, err := linkSkillSources(names, env)
		if err != nil {
			return err
		}
		for i, source := range sources {
			// Named after the manifest's skill, never after what its directory resolves
			// to: a symlinked skill must not take another skill's name.
			target := filepath.Join(dest, names[i])
			info, err := os.Lstat(target)
			if err == nil && info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("refusing to overwrite non-symlink %s", target)
			}
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			link := skillLink{target: target, source: source}
			if index, exists := indices[target]; exists {
				// Preserve the first insertion position while the last source wins.
				links[index] = link
			} else {
				indices[target] = len(links)
				links = append(links, link)
			}
		}
	}

	// All sources and existing destinations are checked before the first write.
	for _, link := range links {
		if current, err := filepath.EvalSymlinks(link.target); err == nil && current == link.source {
			continue
		}
		parent := filepath.Dir(link.target)
		if err := os.MkdirAll(parent, 0o777); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp(parent, ".role-skills-*")
		if err != nil {
			return err
		}
		tempLink := filepath.Join(tmp, "link")
		if err := os.Symlink(link.source, tempLink); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
		if err := os.Rename(tempLink, link.target); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
		if err := os.RemoveAll(tmp); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "linked %s -> %s\n", filepath.Base(link.target), link.source); err != nil {
			return err
		}
	}
	return nil
}

func skillDestination(client string, env []string) (string, error) {
	if dest, ok := skillEnvLookup(env, "SKILLS_DEST"); ok {
		return dest, nil
	}
	home, ok := skillEnvLookup(env, "HOME")
	if !ok || home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	dir := ".agents/skills"
	if client == "claude-code" {
		dir = ".claude/skills"
	}
	return filepath.Join(home, dir), nil
}

// presentSkills keeps the names that resolve and warns for the others.
func presentSkills(role string, names, env []string, warn io.Writer) []string {
	e := newEnv(env)
	roots := skillRoots(repoRoot(e), e)
	var kept []string
	for _, name := range names {
		if findSkill(name, roots) == "" {
			fmt.Fprintf(warn, "warning: skill %s not found (run ./setup.sh install); %s starts without it\n", name, role)
			continue
		}
		kept = append(kept, name)
	}
	return kept
}

func linkSkillSources(names, env []string) ([]string, error) {
	e := newEnv(env)
	return skillSources(names, repoRoot(e), e)
}

func skillEnvLookup(env []string, name string) (string, bool) {
	prefix := name + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix), true
		}
	}
	return "", false
}
