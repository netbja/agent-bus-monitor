package rolecfg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Herdr's native restore through the launcher (v0.21.1, spec
// docs/superpowers/specs/2026-10-21-launcher-resume-design.md): every Claude
// Code launch carries a SessionStart hook that reports the command resuming
// this session through agent-launch, so a restored pane comes back with its
// role, rules and identity instead of a bare `claude --resume`.

// resumeHookCommand is the SessionStart hook: the absolute agentbus
// (AGENTBUS_BIN, else this binary) and the repository's agent-launch, quoted
// for the shell Claude Code runs hooks in.
func resumeHookCommand(e *envMap) string {
	return ShellJoin([]string{hookBin(e), "roles", "report-resume", filepath.Join(repoRoot(e), "scripts", "agent-launch")})
}

// hookBin is the agentbus a hook runs: AGENTBUS_BIN, else this binary.
func hookBin(e *envMap) string {
	bin, ok := e.get("AGENTBUS_BIN")
	if !ok || bin == "" {
		bin = "agentbus"
		if exe, err := os.Executable(); err == nil {
			bin = absResolved(exe)
		}
	}
	return bin
}

// claudeHooks are the hooks every Claude Code launch carries: the resume
// report, and the turn hooks the state follows (auto-state spec): turn start
// on UserPromptSubmit, turn end on Stop and on a new session.
type claudeHooks struct{ Resume, TurnStart, TurnEnd string }

// launchHooks builds them with the same agentbus as the resume hook.
func launchHooks(e *envMap) claudeHooks {
	bin := hookBin(e)
	return claudeHooks{Resume: resumeHookCommand(e),
		TurnStart: ShellJoin([]string{bin, "roles", "turn", "start"}),
		TurnEnd:   ShellJoin([]string{bin, "roles", "turn", "end"})}
}

// ResumeArgv is the command herdr runs to resume a session: the template's
// own launch (env ROLES_TOML=… agent-launch <role> <project>), with
// --profile for a role running on another profile than its own, and
// --resume. `env` is the plain command name herdr requires, and the absolute
// script needs nothing on PATH.
func ResumeArgv(launcher, manifest, role, project, profile, sessionID string) ([]string, error) {
	switch {
	case !filepath.IsAbs(launcher) || !filepath.IsAbs(manifest):
		return nil, errors.New("launcher and manifest must be absolute paths")
	case !namePattern.MatchString(role) || !namePattern.MatchString(project):
		return nil, errors.New("invalid role or project name")
	case profile != "" && !namePattern.MatchString(profile):
		return nil, errors.New("invalid profile name")
	case !sessionIDPattern.MatchString(sessionID):
		return nil, errors.New("invalid session id")
	}
	argv := []string{"env", "ROLES_TOML=" + manifest, launcher, role, project}
	if profile != "" {
		argv = append(argv, "--profile", profile)
	}
	argv = append(argv, "--resume", sessionID)
	if err := checkResumeArgv(argv); err != nil {
		return nil, err
	}
	return argv, nil
}

// checkResumeArgv bounds what is handed to herdr: at most 64 arguments and
// 8 KiB, no apostrophe, no control character.
func checkResumeArgv(argv []string) error {
	if len(argv) > 64 {
		return fmt.Errorf("resume command has %d arguments (max 64)", len(argv))
	}
	size := 0
	for _, a := range argv {
		size += len(a) + 1
		if strings.ContainsFunc(a, func(r rune) bool { return r == '\'' || r < 0x20 || r == 0x7f }) {
			return errors.New("resume command holds an apostrophe or a control character")
		}
	}
	if size > 8<<10 {
		return errors.New("resume command over 8 KiB")
	}
	return nil
}
