package setup

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Preflight is setup.sh's step 1: platform, git, curl (only for
// --install-herdr), Go against goWant (go.mod's version), Docker and compose.
// It returns how many required tools are missing; the caller stops a real run
// after it, while a dry run goes on and lists every gap.
func Preflight(ctx context.Context, r Runner, o *Output, opt Options, goWant, goos, goarch string) int {
	missing := 0
	need := func(s string) { missing++; o.Error(s) }
	first := func(out []byte) string {
		s, _, _ := strings.Cut(strings.TrimRight(string(out), "\n"), "\n")
		return s
	}
	o.Step("1/5 Checking system platform & toolchain")
	o.OK(fmt.Sprintf("Platform: %s / %s", goos, goarch))

	if _, err := r.LookPath("git"); err == nil {
		v := "installed"
		if out, err := r.Run(ctx, Cmd{Name: "git", Args: []string{"--version"}}); err == nil {
			v = first(out)
		}
		o.OK("git: " + v)
	} else {
		need("git is required but not installed.")
	}

	if opt.InstallHerdr {
		if _, err := r.LookPath("curl"); err == nil {
			v := "installed"
			if out, err := r.Run(ctx, Cmd{Name: "curl", Args: []string{"--version"}}); err == nil {
				v = first(out)
			}
			o.OK("curl: " + v)
		} else {
			need("curl is required by --install-herdr but not installed.")
		}
	}

	if _, err := r.LookPath("go"); err == nil {
		// GOTOOLCHAIN=local: report the installed Go, never download a toolchain during preflight.
		have := ""
		if out, err := r.Run(ctx, Cmd{Name: "go", Args: []string{"env", "GOVERSION"}, Env: []string{"GOTOOLCHAIN=local"}}); err == nil {
			have, _, _ = strings.Cut(strings.TrimPrefix(first(out), "go"), " ")
		}
		switch {
		case have != "" && versionGE(have, goWant):
			o.OK(fmt.Sprintf("go: %s (go.mod requires %s)", have, goWant))
		case toolchain(ctx, r) == "local":
			need(fmt.Sprintf("Go %s or newer is required (found %s, GOTOOLCHAIN=local): https://go.dev/dl/", goWant, orUnknown(have)))
		default:
			o.Warn(fmt.Sprintf("go %s is older than %s; the build will download the go%s toolchain.", orUnknown(have), goWant, goWant))
		}
	} else {
		need(fmt.Sprintf("Go %s or newer is required to build busmon and agentbus: https://go.dev/dl/", goWant))
	}

	if _, err := r.LookPath("docker"); err == nil {
		if _, err := r.Run(ctx, Cmd{Name: "docker", Args: []string{"info"}}); err == nil {
			o.OK("docker: daemon is running")
		} else {
			o.Warn("docker is installed but daemon is not running (needed for Redis broker).")
		}
		switch ComposeCmd(ctx, r) {
		case "docker compose":
			o.OK("docker compose: plugin available")
		case "docker-compose":
			o.OK("docker-compose: standalone command available")
		default:
			o.Warn("docker compose plugin is not available.")
		}
	} else {
		o.Warn("docker is not installed. Redis broker container requires Docker: https://docs.docker.com/engine/install/")
	}
	return missing
}

// ComposeCmd is setup.sh's compose_cmd: the plugin first, then the
// standalone command, else "".
func ComposeCmd(ctx context.Context, r Runner) string {
	if _, err := r.Run(ctx, Cmd{Name: "docker", Args: []string{"compose", "version"}}); err == nil {
		return "docker compose"
	}
	if _, err := r.LookPath("docker-compose"); err == nil {
		return "docker-compose"
	}
	return ""
}

func toolchain(ctx context.Context, r Runner) string {
	out, err := r.Run(ctx, Cmd{Name: "go", Args: []string{"env", "GOTOOLCHAIN"}})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// versionGE is setup.sh's version_ge: dotted a >= b, a non-numeric suffix
// of a part ignored (1.26rc1 reads 1.26).
func versionGE(a, b string) bool {
	for a != "" || b != "" {
		var xa, xb string
		xa, a, _ = strings.Cut(a, ".")
		xb, b, _ = strings.Cut(b, ".")
		na, nb := leadingInt(xa), leadingInt(xb)
		if na != nb {
			return na > nb
		}
	}
	return true
}

func leadingInt(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}
