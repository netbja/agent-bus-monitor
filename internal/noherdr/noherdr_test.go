// Package noherdr holds the herdr exit's last test (spec
// docs/superpowers/specs/2026-11-10-herdr-exit-design.md §7, step 10): no
// tracked file runs herdr. No allowlist: the holder hosts every agent's
// terminal, and nothing here may fall back to herdr.
package noherdr

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// goExec is a Go exec of herdr: the name as the command of exec.Command or
// exec.CommandContext, or a read of the variables that located it.
var goExec = regexp.MustCompile(`exec\.Command(Context)?\([^)]*"herdr"|"HERDR_BIN(_PATH)?"|"HERDR_SOCKET_PATH"`)

// shellExec is herdr in a shell command's position: first on a line, or
// after a pipe, a list operator or a command substitution.
var shellExec = regexp.MustCompile(`(^|[|;&]|\$\(|\bexec |\bcommand |\bsudo )\s*herdr(\s|$)`)

// isShell: a script by its extension or its first line.
func isShell(path string, data []byte) bool {
	if strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".bash") {
		return true
	}
	first, _, _ := bytes.Cut(data, []byte("\n"))
	return bytes.HasPrefix(first, []byte("#!")) && (bytes.Contains(first, []byte("sh")) || bytes.Contains(first, []byte("bash")))
}

func TestNothingRunsHerdr(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not a git checkout: nothing tracked to check")
	}
	top := strings.TrimSpace(string(root))
	out, err := exec.Command("git", "-C", top, "ls-files", "-z").Output()
	if err != nil {
		t.Fatal(err)
	}
	self := "internal/noherdr/noherdr_test.go"
	checked := 0
	for _, f := range bytes.Split(out, []byte{0}) {
		path := string(f)
		if path == "" || path == self {
			continue
		}
		data, err := os.ReadFile(filepath.Join(top, path))
		if err != nil || bytes.IndexByte(data, 0) >= 0 { // gone from the worktree, or binary
			continue
		}
		var rule *regexp.Regexp
		switch {
		case strings.HasSuffix(path, ".go"):
			rule = goExec
		case isShell(path, data):
			rule = shellExec
		default:
			continue
		}
		checked++
		for i, line := range strings.Split(string(data), "\n") {
			code := line
			if rule == shellExec {
				code, _, _ = strings.Cut(line, "#") // a comment runs nothing
			}
			if rule.MatchString(code) {
				t.Errorf("%s:%d: runs herdr (herdr exit: the holder hosts every terminal): %s", path, i+1, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Go or shell file checked: the walk is broken")
	}
}

// The rules catch what they are for, and leave a fake named herdr alone.
func TestRulesCatchAnExec(t *testing.T) {
	for _, l := range []string{`exec.Command("herdr", "pane", "run")`, `exec.CommandContext(ctx, "herdr", args...)`, `os.Getenv("HERDR_BIN_PATH")`} {
		if !goExec.MatchString(l) {
			t.Errorf("go rule misses %s", l)
		}
	}
	for _, l := range []string{`herdr pane send-text "$pane" "$MSG"`, `  herdr tab create`, `tab="$(herdr tab create --label x)"`, `true && herdr pane run p x`, `exec herdr --session s`} {
		if !shellExec.MatchString(l) {
			t.Errorf("shell rule misses %s", l)
		}
	}
	for _, l := range []string{`make_stub "$localbin" herdr 0`, `printf 'x' > "$T/fakebin/herdr"`, `echo "herdr exit"`, `chmod +x "$T/herdr"`, `assert_eq "" "" "setup never calls herdr (herdr exit)"`} {
		if shellExec.MatchString(l) {
			t.Errorf("shell rule flags %s, which runs no herdr", l)
		}
	}
}
