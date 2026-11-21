package setup

import (
	"context"
	"fmt"
	"github.com/netbja/agent-bus-monitor/internal/brokerrt"
	"strconv"
	"strings"
)

// Preflight is setup.sh's step 1: platform, git, curl,
// Go against goWant (go.mod's version), Docker and compose.
// It returns how many required tools are missing, and what it learned of docker; the caller stops a real run
// after it, while a dry run goes on and lists every gap.
func Preflight(ctx context.Context, r Runner, o *Output, opt Options, goWant, goos, goarch string, rt brokerrt.Runtime) (int, Docker) {
	missing := 0
	var d Docker
	need := func(s string) { missing++; o.Error(s) }
	first := func(out []byte) string {
		s, _, _ := strings.Cut(strings.TrimRight(string(out), "\n"), "\n")
		return s
	}
	o.Step("1/4 Checking system platform & toolchain")
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

	bin := rt.Bin() // broker.runtime (broker-hardening step 3)
	if _, err := r.LookPath(bin); err == nil {
		if _, err := r.Run(ctx, Cmd{Name: bin, Args: []string{"info"}}); err == nil {
			d.OK = true
			o.OK(bin + ": daemon is running")
		} else {
			o.Warn(bin + " is installed but daemon is not running (needed for Redis broker).")
		}
		d.Compose = ComposeCmd(ctx, r, rt)
		switch d.Compose {
		case rt.Compose():
			o.OK(rt.Compose() + ": plugin available")
		case rt.Standalone():
			o.OK(rt.Standalone() + ": standalone command available")
		default:
			o.Warn(rt.Compose() + " plugin is not available.")
		}
	} else if rt == brokerrt.Podman {
		o.Warn("podman is not installed (broker.runtime=podman). Redis broker container requires it: https://podman.io/docs/installation")
	} else {
		o.Warn(bin + " is not installed. Redis broker container requires Docker: https://docs.docker.com/engine/install/")
	}
	return missing, d
}

// ComposeCmd is setup.sh's compose_cmd for the runtime: the plugin first
// ("<runtime> compose"), then the standalone command, else "".
func ComposeCmd(ctx context.Context, r Runner, rt brokerrt.Runtime) string {
	if _, err := r.Run(ctx, Cmd{Name: rt.Bin(), Args: []string{"compose", "version"}}); err == nil {
		return rt.Compose()
	}
	if _, err := r.LookPath(rt.Standalone()); err == nil {
		return rt.Standalone()
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
