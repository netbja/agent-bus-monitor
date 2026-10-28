package setup

import "context"

// Cmd is one external command: name, arguments, extra environment
// (KEY=value, added to the inherited one) and working directory. Stdout
// drops stderr from the result (setup.sh's 2>/dev/null); Attach gives the
// command setup's own terminal instead and returns nothing.
type Cmd struct {
	Name   string
	Args   []string
	Env    []string
	Dir    string
	Stdout bool
	Attach bool
}

// Runner is the one way setup reaches the outside world (spec: Safety,
// layer 1): every docker, herdr, git, go, systemctl, launchctl and curl call,
// and the client CLIs of the model refresh. The real one lives in
// cmd/agentbus; tests pass one that fails on any command they did not expect.
type Runner interface {
	Run(ctx context.Context, c Cmd) (combined []byte, err error)
	LookPath(name string) (string, error)
}
