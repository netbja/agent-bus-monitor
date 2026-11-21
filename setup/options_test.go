package setup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Flags land where the bash puts them; the subcommand defaults to install.
func TestParseArgsFlags(t *testing.T) {
	o := NewOutput(&bytes.Buffer{}, &bytes.Buffer{}, false)
	env := func(k string) string { return map[string]string{"HOME": "/h"}[k] }
	opt, code := ParseArgs([]string{"--dry-run", "--bin-dir", "/b", "--no-docker", "--no-skills", "--force"}, env, o)
	if code != -1 || opt.Sub != "install" || !opt.DryRun || opt.BinDir != "/b" || opt.StartDocker || opt.InstallSkills || !opt.Force {
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

// ./setup.sh acl on|off: the state is required, and only --dry-run and
// --yes go with it.
func TestParseArgsACL(t *testing.T) {
	getenv := func(string) string { return "" }
	for _, c := range []struct {
		args []string
		code int
		acl  string
	}{
		{[]string{"acl", "on"}, -1, "on"},
		{[]string{"acl", "off", "--yes", "--dry-run"}, -1, "off"},
		{[]string{"acl"}, 1, ""},
		{[]string{"acl", "maybe"}, 1, ""},
		{[]string{"acl", "on", "--purge"}, 1, ""},
		{[]string{"acl", "on", "--no-docker"}, 1, ""},
	} {
		var out bytes.Buffer
		opt, code := ParseArgs(c.args, getenv, NewOutput(&out, &out, false))
		if code != c.code || (code < 0 && (opt.Sub != "acl" || opt.ACL != c.acl)) {
			t.Errorf("%q: code %d, sub %q, acl %q: %s", c.args, code, opt.Sub, opt.ACL, out.String())
		}
	}
	if !strings.Contains(Usage(getenv), "./setup.sh acl on|off") {
		t.Error("the help lacks acl on|off")
	}
}
