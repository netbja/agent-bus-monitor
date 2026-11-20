package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The installer's helpers that outlived the herdr plugin (herdr exit 10b):
// its state directory, file backups and atomic writes, and the project
// services it names after an install.

// state is setup.sh's XDG_STATE_HOME, else ~/.local/state.
func (in *Installer) state() string { return StateDir(in.Getenv) }

// StateDir is XDG_STATE_HOME, else ~/.local/state, on Linux and macOS alike
// (setup's state; agentbus update keeps its snapshots under it).
func StateDir(getenv func(string) string) string {
	if s := getenv("XDG_STATE_HOME"); s != "" {
		return s
	}
	return filepath.Join(getenv("HOME"), ".local", "state")
}

// backup copies path to path.bak.<stamp> keeping its mode and times (cp -p).
func (in *Installer) backup(path string) (string, error) {
	dst := path + ".bak." + in.Now().Format("20060102150405")
	b, err := os.ReadFile(path)
	if err != nil {
		return dst, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return dst, err
	}
	if err := os.WriteFile(dst, b, fi.Mode().Perm()); err != nil {
		return dst, err
	}
	_ = os.Chmod(dst, fi.Mode().Perm())
	_ = os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	return dst, nil
}

// writeAtomic replaces path's content whole: a temp file beside the file
// path resolves to, renamed over it, so a crash leaves the old content or the
// new one, a symlinked config stays a link, and the mode is kept. A file the
// person cannot write (made read-only) is refused, as a direct write would be.
func writeAtomic(path string, data []byte) error {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return err
	}
	w, err := os.OpenFile(real, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_ = w.Close()
	f, err := os.CreateTemp(filepath.Dir(real), "."+filepath.Base(real)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, fi.Mode().Perm())
	}
	if err == nil {
		err = os.Rename(tmp, real)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// serviceLines is setup.sh's project_services: one "<project> <kind>
// <state>" line per agentbus user service, read through the agentbus
// installed, else the checkout's; none when there is no service.
func (in *Installer) serviceLines(ctx context.Context) []string {
	for _, ab := range []string{filepath.Join(in.Opt.BinDir, "agentbus"), filepath.Join(in.Repo, "agentbus")} {
		if !isExec(ab) {
			continue
		}
		out, _ := in.R.Run(ctx, Cmd{Name: ab, Args: []string{"service", "list"}, Stdout: true})
		var lines []string
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if !strings.HasPrefix(l, "no ") {
				lines = append(lines, l)
			}
		}
		if strings.TrimSpace(strings.Join(lines, "")) == "" {
			return nil
		}
		return lines
	}
	return nil
}

// isHolderLine: a holder instance, by kind (herdr exit spec 4.4a). It makes
// no broker connection and a restart kills every agent it holds, so neither
// a password change nor an upgrade restarts it, nor suggests it.
func isHolderLine(l string) bool {
	f := strings.Fields(l)
	return len(f) >= 2 && (f[1] == "holder" || strings.HasPrefix(f[1], "holder."))
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// ServicesHint names the running project services, which keep the binary
// they started with, and how to move each on.
func (in *Installer) ServicesHint(ctx context.Context) {
	if in.Opt.DryRun {
		return
	}
	lines := slices.DeleteFunc(in.serviceLines(ctx), isHolderLine)
	if len(lines) == 0 {
		return
	}
	in.O.Info("project services keep running the previous agentbus until restarted:")
	for _, l := range lines {
		f := strings.Fields(l)
		for len(f) < 2 {
			f = append(f, "")
		}
		p, k := f[0], f[1]
		rest := strings.Join(f[2:], " ")
		in.O.Info("  " + p + " " + k + " (" + rest + "): agentbus --project " + p + " service restart " + k)
	}
}

// RestartStale (P2-13): the project services still running an older build
// than the one just installed, asked of the installed agentbus (service
// stale), each with its restart; then one question, restarted only on the
// person's yes. --yes takes the cautious default (no) and no terminal asks
// nothing: an install never restarts a service unattended. Only dispatch and
// missions: a holder and its agents are never touched. A stale list that
// cannot be read falls back to naming every running service (ServicesHint).
func (in *Installer) RestartStale(ctx context.Context, p *Prompter) {
	if in.Opt.DryRun {
		return
	}
	ab := filepath.Join(in.Opt.BinDir, "agentbus")
	if !isExec(ab) {
		return
	}
	out, err := in.R.Run(ctx, Cmd{Name: ab, Args: []string{"service", "stale"}, Stdout: true})
	if err != nil {
		in.ServicesHint(ctx)
		return
	}
	var stale [][2]string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(l)
		if len(f) != 3 { // service stale never names a holder (they are not units)
			continue
		}
		stale = append(stale, [2]string{f[0], f[1]})
		build := f[2]
		if build == "-" {
			build = "no build recorded"
		}
		if len(stale) == 1 {
			in.O.Info("project services still running an older agentbus build (restart them to run this one; their holders and agents are not touched):")
		}
		in.O.Info("  " + f[0] + " " + f[1] + " (" + build + "): agentbus --project " + f[0] + " service restart " + f[1])
	}
	if len(stale) == 0 {
		in.O.Info("every project service runs this build")
		return
	}
	if p == nil || p.Yes || p.TTY == nil { // never unattended: the commands above are the way
		return
	}
	ok, err := Ask(in.O, p, fmt.Sprintf("Restart these %d services now?", len(stale)), false)
	if err != nil || !ok {
		return
	}
	for _, s := range stale {
		if _, err := in.R.Run(ctx, Cmd{Name: ab, Args: []string{"--project", s[0], "service", "restart", s[1]}}); err != nil {
			in.O.Warn(s[0] + " " + s[1] + ": restart failed: " + err.Error())
			continue
		}
		in.O.Info("  restarted " + s[0] + " " + s[1])
	}
}
