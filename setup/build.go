package setup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Installer is one install run: the runner, the output, the flags, the
// checkout, and a temp dir for the builds (setup.sh's TMP_DIR).
type Installer struct {
	R    Runner
	O    *Output
	Opt  Options
	Repo string
	Tmp  string
	Rev  string // git describe of the checkout, read once (RepoRev)
	// Getenv reads the environment (HOME, XDG dirs, SKILLS_REPO, SKILLS_REF).
	Getenv func(string) string
	// Now is the clock for backup names (setup.sh's date +%Y%m%d%H%M%S).
	Now func() time.Time
	// Setenv changes setup's own environment (the PATH after installing herdr).
	Setenv func(k, v string) error
}

// RepoRev is setup.sh's REPO_REV: git describe --tags --always --dirty.
func (in *Installer) RepoRev(ctx context.Context) string {
	out, err := in.R.Run(ctx, Cmd{Name: "git", Args: []string{"-C", in.Repo, "describe", "--tags", "--always", "--dirty"}})
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "unknown revision"
	}
	return strings.TrimSpace(string(out))
}

// BuildVersion is the release stamped into both binaries: a v<digit> tag
// description, else dev.
func BuildVersion(rev string) string {
	if len(rev) > 1 && rev[0] == 'v' && rev[1] >= '0' && rev[1] <= '9' {
		return rev
	}
	return "dev"
}

// Ldflags are setup.sh's three -X flags, written as it writes them.
func Ldflags(version, repo, binDir string) string {
	return "-X github.com/netbja/agent-bus-monitor/buildinfo.Version=" + version +
		" -X 'github.com/netbja/agent-bus-monitor/buildinfo.Repo=" + repo + "'" +
		" -X 'github.com/netbja/agent-bus-monitor/buildinfo.Bin=" + binDir + "/agentbus'"
}

// BuildAndInstall is setup.sh's build_and_install_binary: a stamped build,
// installed atomically into the repository (where the herdr tabs run it) and
// into the bin dir (unless that is the repository).
func (in *Installer) BuildAndInstall(ctx context.Context, name, pkg string) error {
	target := filepath.Join(in.Opt.BinDir, name)
	if in.Opt.DryRun {
		in.O.Info(fmt.Sprintf("[dry-run] would compile %s -> %s (and %s/%s)", pkg, target, in.Repo, name))
		return nil
	}
	in.O.Info(fmt.Sprintf("Compiling %s (%s)...", name, in.Rev))
	built := filepath.Join(in.Tmp, name)
	ld := Ldflags(BuildVersion(in.Rev), in.Repo, in.Opt.BinDir)
	out, err := in.R.Run(ctx, Cmd{Name: "go", Args: []string{"build", "-ldflags", ld, "-o", built, pkg}, Dir: in.Repo})
	if err != nil {
		showTail(in.O.err, out)
		in.O.Fatal(fmt.Sprintf("Failed to build %s from %s", name, pkg))
		return fmt.Errorf("build %s: %w", name, err)
	}
	dests := []string{filepath.Join(in.Repo, name)}
	if !sameDir(in.Opt.BinDir, in.Repo) {
		dests = append(dests, target)
	}
	for _, d := range dests {
		if err := InstallFile(built, d); err != nil {
			in.O.Fatal(fmt.Sprintf("could not install %s to %s: %v", name, d, err))
			return err
		}
	}
	in.O.OK(fmt.Sprintf("%s: installed to %s and %s/%s", name, target, in.Repo, name))
	return nil
}

// InstallFile is setup.sh's install_file: copy beside dst, then rename over
// it, so a running binary is replaced atomically, never rewritten in place.
func InstallFile(src, dst string) error {
	tmp := dst + ".tmp." + strconv.Itoa(os.Getpid())
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// RoleScriptLinks is setup.sh's install_role_script_links: agent-launch and
// agent-spawn linked into the bin dir; only links to these exact files are
// ours, anything else there is left with a warning.
func (in *Installer) RoleScriptLinks() {
	for _, script := range []string{"agent-launch", "agent-spawn"} {
		target := filepath.Join(in.Opt.BinDir, script)
		source := filepath.Join(in.Repo, "scripts", script)
		_, lerr := os.Lstat(target)
		switch {
		case OurScriptLink(target, in.Repo):
			in.O.OK(script + ": linked to " + source)
		case lerr == nil:
			in.O.Warn(target + " exists and is not this repository's " + script + " link; left as it is")
		case in.Opt.DryRun:
			in.O.Info("[dry-run] would link " + target + " -> " + source)
		case os.Symlink(source, target) == nil:
			in.O.OK(script + ": linked to " + source)
		default:
			in.O.Warn("could not link " + target + " -> " + source)
		}
	}
}

// OurScriptLink is setup.sh's our_role_script_link: a symlink to exactly
// this checkout's script of the same name.
func OurScriptLink(link, repo string) bool {
	got, err := os.Readlink(link)
	return err == nil && got == filepath.Join(repo, "scripts", filepath.Base(link))
}

// sameDir: both resolve to one directory (setup.sh's cd && pwd comparison).
func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// showTail is setup.sh's show_log: the last 15 lines, indented by six.
func showTail(w io.Writer, log []byte) {
	lines := strings.Split(strings.TrimRight(string(log), "\n"), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	for _, l := range lines {
		fmt.Fprintln(w, "      "+l)
	}
}
