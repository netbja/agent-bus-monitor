package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildWorld: a repository and a bin dir in temp, a fake runner whose go
// build writes the binary it was asked for.
func buildWorld(t *testing.T, rev string, buildErr error) (*Installer, *bytes.Buffer, *bytes.Buffer, string, string) {
	t.Helper()
	repo, bin := t.TempDir(), t.TempDir()
	for _, d := range []string{"scripts"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tmp := t.TempDir()
	version := BuildVersion(rev)
	ld := Ldflags(version, repo, bin)
	write := func(c Cmd) {
		for i, a := range c.Args {
			if a == "-o" {
				_ = os.WriteFile(c.Args[i+1], []byte("binary "+filepath.Base(c.Args[i+1])), 0o644)
			}
		}
	}
	f := &fakeRunner{t: t, cmds: map[string]fakeCmd{
		"git -C " + repo + " describe --tags --always --dirty": {out: rev + "\n"},
		"go build -ldflags " + ld + " -o " + tmp + "/agentbus ./cmd/agentbus": {do: write, err: buildErr,
			out: strings.Repeat("compile error line\n", 20)},
		"go build -ldflags " + ld + " -o " + tmp + "/busmon ./cmd/busmon": {do: write},
	}}
	var out, errw bytes.Buffer
	in := &Installer{R: f, O: NewOutput(&out, &errw, false), Opt: Options{BinDir: bin}, Repo: repo, Tmp: tmp}
	return in, &out, &errw, repo, bin
}

// The release stamp: a v* tag, else dev; the three ldflags setup.sh sets.
func TestBuildVersionAndLdflags(t *testing.T) {
	if BuildVersion("v0.23.1") != "v0.23.1" || BuildVersion("v0.23.1-4-gabc-dirty") != "v0.23.1-4-gabc-dirty" ||
		BuildVersion("abc123") != "dev" || BuildVersion("unknown revision") != "dev" {
		t.Fatal("BuildVersion")
	}
	want := "-X github.com/netbja/agent-bus-monitor/buildinfo.Version=v1 -X 'github.com/netbja/agent-bus-monitor/buildinfo.Repo=/r' -X 'github.com/netbja/agent-bus-monitor/buildinfo.Bin=/b/agentbus'"
	if got := Ldflags("v1", "/r", "/b"); got != want {
		t.Fatalf("Ldflags\n got %s\nwant %s", got, want)
	}
}

// Both binaries built stamped, installed atomically into the repository and
// the bin dir, with setup.sh's lines.
func TestBuildAndInstallBoth(t *testing.T) {
	in, out, _, repo, bin := buildWorld(t, "v9.9.9", nil)
	ctx := context.Background()
	in.Rev = in.RepoRev(ctx)
	for _, b := range [][2]string{{"agentbus", "./cmd/agentbus"}, {"busmon", "./cmd/busmon"}} {
		if err := in.BuildAndInstall(ctx, b[0], b[1]); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{filepath.Join(repo, b[0]), filepath.Join(bin, b[0])} {
			st, err := os.Stat(p)
			if err != nil || st.Mode().Perm() != 0o755 {
				t.Fatalf("%s: %v %v", p, st, err)
			}
		}
	}
	want := "    • Compiling agentbus (v9.9.9)...\n" +
		"    ✓ agentbus: installed to " + bin + "/agentbus and " + repo + "/agentbus\n" +
		"    • Compiling busmon (v9.9.9)...\n" +
		"    ✓ busmon: installed to " + bin + "/busmon and " + repo + "/busmon\n"
	if out.String() != want {
		t.Fatalf("out:\n%s\nwant:\n%s", out.String(), want)
	}
	if left, _ := filepath.Glob(filepath.Join(bin, "*.tmp.*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

// A failed build: the log's last 15 lines, indented, then the fatal line.
func TestBuildFailureShowsTheTail(t *testing.T) {
	in, _, errw, _, _ := buildWorld(t, "abc", errors.New("exit 1"))
	if err := in.BuildAndInstall(context.Background(), "agentbus", "./cmd/agentbus"); err == nil {
		t.Fatal("a failed build succeeded")
	}
	want := strings.Repeat("      compile error line\n", 15) + "\nerror: Failed to build agentbus from ./cmd/agentbus\n"
	if errw.String() != want {
		t.Fatalf("stderr %q", errw.String())
	}
}

// A dry run compiles nothing and says so.
func TestBuildDryRun(t *testing.T) {
	in, out, _, repo, bin := buildWorld(t, "abc", nil)
	in.Opt.DryRun = true
	if err := in.BuildAndInstall(context.Background(), "busmon", "./cmd/busmon"); err != nil {
		t.Fatal(err)
	}
	if want := "    • [dry-run] would compile ./cmd/busmon -> " + bin + "/busmon (and " + repo + "/busmon)\n"; out.String() != want {
		t.Fatalf("%q", out.String())
	}
}

// The launcher links: ours reported, a foreign file left with a warning,
// a missing one linked (a dry run only says so).
func TestRoleScriptLinks(t *testing.T) {
	in, out, _, repo, bin := buildWorld(t, "abc", nil)
	if err := os.Symlink(filepath.Join(repo, "scripts", "agent-launch"), filepath.Join(bin, "agent-launch")); err != nil {
		t.Fatal(err)
	}
	in.RoleScriptLinks()
	if got, _ := os.Readlink(filepath.Join(bin, "agent-spawn")); got != filepath.Join(repo, "scripts", "agent-spawn") {
		t.Fatalf("agent-spawn link %q", got)
	}
	want := "    ✓ agent-launch: linked to " + repo + "/scripts/agent-launch\n" +
		"    ✓ agent-spawn: linked to " + repo + "/scripts/agent-spawn\n"
	if out.String() != want {
		t.Fatalf("%q", out.String())
	}
	out.Reset()
	in2, out2, _, _, bin2 := buildWorld(t, "abc", nil)
	_ = os.WriteFile(filepath.Join(bin2, "agent-launch"), []byte("mine"), 0o755)
	in2.Opt.DryRun = true
	in2.RoleScriptLinks()
	want2 := "    ! " + bin2 + "/agent-launch exists and is not this repository's agent-launch link; left as it is\n" +
		"    • [dry-run] would link " + bin2 + "/agent-spawn -> " + in2.Repo + "/scripts/agent-spawn\n"
	if out2.String() != want2 {
		t.Fatalf("%q", out2.String())
	}
}

// A failed install of a built binary says where and why (reviewer-codex's
// review of installer-2), as setup.sh's cp or mv would before set -e stops it.
func TestInstallFailureNamesTheDestination(t *testing.T) {
	in, _, errw, _, bin := buildWorld(t, "v9.9.9", nil)
	in.Rev = "v9.9.9"
	if err := os.Chmod(bin, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(bin, 0o755)
	if err := in.BuildAndInstall(context.Background(), "agentbus", "./cmd/agentbus"); err == nil {
		t.Fatal("an unwritable bin dir installed")
	}
	if !strings.Contains(errw.String(), "error: could not install agentbus to "+bin+"/agentbus: ") {
		t.Fatalf("stderr %q", errw.String())
	}
}
