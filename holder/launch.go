package holder

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// Command is what a terminal runs: its argv, the environment added to the
// holder's own, and its directory.
type Command struct {
	Argv []string
	Env  []string
	Dir  string
}

// TemplateLaunch is how the holder turns a Start frame into a command (spec
// 4.2, security S2a): the role's launch, exactly as agent-launch runs today
// (env ROLES_TOML=<manifest> <launcher> <role> <project> [--profile p]
// [--resume id]). The frame names a role, a profile and a session; the
// launcher, the manifest, the project and the directory are the holder's
// own. So Start can only start one of the project's roles.
type TemplateLaunch struct {
	Project  string
	Launcher string // agent-launch, absolute
	Manifest string // roles.toml, absolute
	Dir      string // the project's working directory
}

// Command is s's command: the role must be in the manifest, the profile its
// own or one of its fallback_profiles, and a resume needs a client whose
// resume is verified (rolecfg.ResumeVerified) and a session id.
func (l TemplateLaunch) Command(s Start) (Command, error) {
	if err := validate(s); err != nil {
		return Command{}, err
	}
	if !filepath.IsAbs(l.Launcher) || !filepath.IsAbs(l.Manifest) {
		return Command{}, errors.New("holder: the launcher and the manifest must be absolute paths")
	}
	m, err := rolecfg.Load(l.Manifest)
	if err != nil {
		return Command{}, fmt.Errorf("holder: the role manifest: %w", err)
	}
	if !slices.Contains(m.RoleNames(), s.Role) {
		return Command{}, fmt.Errorf("holder: role %s is not in the manifest", s.Role)
	}
	own, err := m.Resolve(s.Role)
	if err != nil {
		return Command{}, fmt.Errorf("holder: role %s: %w", s.Role, err)
	}
	p, profile := own, ""
	if s.Profile != "" && s.Profile != own.Profile {
		if !slices.Contains(m.Fallbacks(s.Role), s.Profile) {
			return Command{}, fmt.Errorf("holder: profile %s is neither %s's own nor one of its fallback_profiles", s.Profile, s.Role)
		}
		if p, err = m.ResolveWith(s.Role, s.Profile); err != nil {
			return Command{}, fmt.Errorf("holder: profile %s: %w", s.Profile, err)
		}
		profile = s.Profile
	}
	if s.ResumeSession != "" {
		if !rolecfg.ResumeVerified(p.Client) {
			return Command{}, fmt.Errorf("holder: %s's client %s has no verified resume", s.Role, p.Client)
		}
		argv, err := rolecfg.ResumeArgv(l.Launcher, l.Manifest, s.Role, l.Project, profile, s.ResumeSession)
		if err != nil {
			return Command{}, fmt.Errorf("holder: %w", err)
		}
		return Command{Argv: argv, Dir: l.Dir}, nil
	}
	argv := []string{"env", "ROLES_TOML=" + l.Manifest, l.Launcher, s.Role, l.Project}
	if profile != "" {
		argv = append(argv, "--profile", profile)
	}
	return Command{Argv: argv, Dir: l.Dir}, nil
}
