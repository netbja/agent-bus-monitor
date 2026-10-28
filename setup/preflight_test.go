package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func preflightRun(t *testing.T, f *fakeRunner, opt Options) (string, string, int) {
	t.Helper()
	var out, errw bytes.Buffer
	o := NewOutput(&out, &errw, false)
	missing, _ := Preflight(context.Background(), f, o, opt, "1.26", "linux", "amd64")
	return out.String(), errw.String(), missing
}

// Everything present: the same lines as setup.sh's step 1.
func TestPreflightAllPresent(t *testing.T) {
	f := &fakeRunner{t: t, paths: map[string]string{"git": "/bin/git", "go": "/bin/go", "docker": "/bin/docker", "curl": "/bin/curl"},
		cmds: map[string]fakeCmd{
			"git --version":          {out: "git version 2.47.0\n"},
			"curl --version":         {out: "curl 8.9.1 (x86_64)\nRelease-Date\n"},
			"go env GOVERSION":       {out: "go1.26.2\n"},
			"docker info":            {},
			"docker compose version": {},
		}}
	out, errw, missing := preflightRun(t, f, Options{InstallHerdr: true})
	want := "\n==> 1/5 Checking system platform & toolchain\n" +
		"    ✓ Platform: linux / amd64\n" +
		"    ✓ git: git version 2.47.0\n" +
		"    ✓ curl: curl 8.9.1 (x86_64)\n" +
		"    ✓ go: 1.26.2 (go.mod requires 1.26)\n" +
		"    ✓ docker: daemon is running\n" +
		"    ✓ docker compose: plugin available\n"
	if out != want || errw != "" || missing != 0 {
		t.Fatalf("out:\n%s\nwant:\n%s\nerr %q missing %d", out, want, errw, missing)
	}
}

// Every gap is listed (a dry run goes on, a real run stops after them),
// with setup.sh's words.
func TestPreflightListsEveryGap(t *testing.T) {
	f := &fakeRunner{t: t, paths: map[string]string{}, cmds: map[string]fakeCmd{}}
	out, errw, missing := preflightRun(t, f, Options{InstallHerdr: true})
	for _, w := range []string{
		"    ✗ git is required but not installed.\n",
		"    ✗ curl is required by --install-herdr but not installed.\n",
		"    ✗ Go 1.26 or newer is required to build busmon and agentbus: https://go.dev/dl/\n",
	} {
		if !strings.Contains(errw, w) {
			t.Errorf("missing %q in\n%s", w, errw)
		}
	}
	if !strings.Contains(out, "    ! docker is not installed. Redis broker container requires Docker: https://docs.docker.com/engine/install/\n") || missing != 3 {
		t.Fatalf("out:\n%s\nmissing %d", out, missing)
	}
}

// An old Go: a warning when a toolchain can be downloaded, a gap when
// GOTOOLCHAIN=local forbids it; docker without its daemon, compose standalone.
func TestPreflightOldGoAndDockerVariants(t *testing.T) {
	base := func(toolchain string) *fakeRunner {
		return &fakeRunner{t: t, paths: map[string]string{"git": "/g", "go": "/go", "docker": "/d", "docker-compose": "/dc"},
			cmds: map[string]fakeCmd{
				"git --version":          {out: "git version 2\n"},
				"go env GOVERSION":       {out: "go1.25.3\n"},
				"go env GOTOOLCHAIN":     {out: toolchain + "\n"},
				"docker info":            {err: errors.New("no daemon")},
				"docker compose version": {err: errors.New("no plugin")},
			}}
	}
	out, _, missing := preflightRun(t, base("auto"), Options{})
	if !strings.Contains(out, "    ! go 1.25.3 is older than 1.26; the build will download the go1.26 toolchain.\n") || missing != 0 {
		t.Fatalf("auto toolchain:\n%s", out)
	}
	if !strings.Contains(out, "    ! docker is installed but daemon is not running (needed for Redis broker).\n") ||
		!strings.Contains(out, "    ✓ docker-compose: standalone command available\n") {
		t.Fatalf("docker variants:\n%s", out)
	}
	_, errw, missing := preflightRun(t, base("local"), Options{})
	if !strings.Contains(errw, "    ✗ Go 1.26 or newer is required (found 1.25.3, GOTOOLCHAIN=local): https://go.dev/dl/\n") || missing != 1 {
		t.Fatalf("local toolchain: %q %d", errw, missing)
	}
}

func TestVersionGE(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.26.2", "1.26", true}, {"1.26", "1.26.0", true}, {"1.25.9", "1.26", false}, {"1.26rc1", "1.26", true}, {"2", "1.99", true}} {
		if got := versionGE(c.a, c.b); got != c.want {
			t.Errorf("versionGE(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
