package setup

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bashSetup runs the bash installer, scripts/setup-legacy.sh since the switch
// (the reference while both exist, spec rollout), with a throwaway HOME, and returns its stdout,
// stderr and exit code. Only paths that stop before any work are run.
func bashSetup(t *testing.T, home string, args ...string) (string, string, int) {
	t.Helper()
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	// Failing fakes first on PATH (CLAUDE.md: setup's tests stub docker and
	// herdr): a copy of the repository shares the live broker's compose
	// project, so the real docker must never answer a test.
	fakes := t.TempDir()
	for _, name := range []string{"docker", "docker-compose", "herdr", "systemctl", "launchctl"} {
		if err := os.WriteFile(filepath.Join(fakes, name), []byte("#!/bin/sh\necho \"fake $0 refuses: $*\" >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", append([]string{filepath.Join(repo, "scripts", "setup-legacy.sh")}, args...)...)
	cmd.Env = []string{"HOME=" + home, "PATH=" + fakes + ":/usr/bin:/bin", "NO_COLOR=1"}
	var out, errw bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errw
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	// The fallback names itself where the Go installer says ./setup.sh
	// (reviewer-codex); everything else is the reference, byte for byte.
	self := func(s string) string { return strings.ReplaceAll(s, "scripts/setup-legacy.sh", "./setup.sh") }
	return self(out.String()), self(errw.String()), code
}

// goSetup parses args as agentbus setup does, with the same throwaway HOME
// layout, printing what the bash prints for help and refusals.
func goSetup(t *testing.T, home string, args ...string) (string, string, int) {
	t.Helper()
	var out, errw bytes.Buffer
	o := NewOutput(&out, &errw, false)
	env := map[string]string{"HOME": home}
	_, code := ParseArgs(args, func(k string) string { return env[k] }, o)
	return out.String(), errw.String(), code
}

// Help and every refusal print exactly what setup.sh prints (spec: Outputs).
func TestParseArgsMatchesTheBashByteForByte(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"install", "--help"},
		{"--bogus"}, {"install", "--bin-dir"}, {"uninstall", "--dry-run"},
	} {
		home := t.TempDir() // the help names it: one HOME for both
		wantOut, wantErr, wantCode := bashSetup(t, home, args...)
		gotOut, gotErr, gotCode := goSetup(t, home, args...)
		gotOut = strings.Replace(gotOut, RotateHelp, "", 1) // the Go installer's alone (redis-password)
		if gotCode != wantCode || gotOut != wantOut || gotErr != wantErr {
			t.Errorf("%q:\n go   (%d) out=%q err=%q\n bash (%d) out=%q err=%q", args, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
		}
	}
}

// Flags land where the bash puts them; the subcommand defaults to install.
func TestParseArgsFlags(t *testing.T) {
	o := NewOutput(&bytes.Buffer{}, &bytes.Buffer{}, false)
	env := func(k string) string { return map[string]string{"HOME": "/h"}[k] }
	opt, code := ParseArgs([]string{"--dry-run", "--bin-dir", "/b", "--no-docker", "--no-skills", "--install-herdr", "--force"}, env, o)
	if code != -1 || opt.Sub != "install" || !opt.DryRun || opt.BinDir != "/b" || opt.StartDocker || opt.InstallSkills || !opt.InstallHerdr || !opt.Force {
		t.Fatalf("%d %+v", code, opt)
	}
	opt, code = ParseArgs([]string{"uninstall", "-y", "--purge"}, env, o)
	if code != -1 || opt.Sub != "uninstall" || !opt.Yes || !opt.Purge || opt.BinDir != "/h/.local/bin" {
		t.Fatalf("%d %+v", code, opt)
	}
	// --force has done nothing since v0.20: the switch's release says so, and
	// the next one drops it (spec: Settled in review, 2).
	var warned bytes.Buffer
	if opt, code := ParseArgs([]string{"install", "--force"}, env, NewOutput(&warned, &bytes.Buffer{}, false)); code != -1 || !opt.Force ||
		!strings.Contains(warned.String(), "--force has had no effect since v0.20 and is removed in the next release") {
		t.Fatalf("--force: %d %q", code, warned.String())
	}
	var once bytes.Buffer // update hands --force to the install it runs, which warns: not twice
	if _, code := ParseArgs([]string{"update", "--force"}, env, NewOutput(&once, &bytes.Buffer{}, false)); code != -1 || once.Len() != 0 {
		t.Fatalf("update --force: %d %q", code, once.String())
	}
	var quiet bytes.Buffer
	if _, code := ParseArgs([]string{"install", "--dry-run"}, env, NewOutput(&quiet, &bytes.Buffer{}, false)); code != -1 || quiet.Len() != 0 {
		t.Fatalf("without --force: %d %q", code, quiet.String())
	}
	env2 := func(k string) string { return map[string]string{"HOME": "/h", "BIN_DIR": "/x"}[k] }
	if opt, _ := ParseArgs([]string{"status"}, env2, o); opt.BinDir != "/x" {
		t.Fatalf("BIN_DIR from the environment: %q", opt.BinDir)
	}
}

// The thin script refuses an old Go under GOTOOLCHAIN=local before it
// builds, with setup.sh's words (reviewer-codex): the Go preflight runs only
// after that build.
func TestThinScriptRefusesAnOldLocalGoBeforeBuilding(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	fakes := t.TempDir()
	built := filepath.Join(fakes, "built")
	goFake := `#!/bin/sh
case "$*" in
  "env GOVERSION") echo go1.20.1 ;;
  "env GOTOOLCHAIN") echo local ;;
  build*) touch "` + built + `" ;;
esac
`
	for name, body := range map[string]string{"go": goFake, "git": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(fakes, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", filepath.Join(repo, "setup.sh"), "install", "--dry-run")
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + fakes + ":/usr/bin:/bin", "NO_COLOR=1"}
	var errw bytes.Buffer
	cmd.Stderr = &errw
	if err := cmd.Run(); err == nil {
		t.Fatal("an old local Go went on")
	}
	want := "Go " + goModWant(t, repo) + " or newer is required (found 1.20.1, GOTOOLCHAIN=local): https://go.dev/dl/"
	if !strings.Contains(errw.String(), want) {
		t.Fatalf("stderr %q, want %q", errw.String(), want)
	}
	if _, err := os.Stat(built); err == nil {
		t.Fatal("it built before refusing")
	}
}

func goModWant(t *testing.T, repo string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "go "); ok {
			return strings.TrimSpace(v)
		}
	}
	t.Fatal("no go line")
	return ""
}
