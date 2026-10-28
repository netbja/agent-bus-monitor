package setup

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner answers only the commands it was told to expect; any other
// command fails the test (spec: Safety, layer 1). Keys are "name arg...".
type fakeRunner struct {
	t     *testing.T
	paths map[string]string // LookPath
	cmds  map[string]fakeCmd
	outs  map[string][]string // successive outputs of one command, overriding its cmds out
	ran   []string
}

type fakeCmd struct {
	out string
	err error
	do  func(c Cmd) // a side effect, such as the file a build writes
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func (f *fakeRunner) Run(_ context.Context, c Cmd) ([]byte, error) {
	key := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.ran = append(f.ran, key)
	r, ok := f.cmds[key]
	if !ok {
		f.t.Fatalf("unexpected command: %s", key)
	}
	if r.do != nil {
		r.do(c)
	}
	if q := f.outs[key]; len(q) > 0 {
		r.out, f.outs[key] = q[0], q[1:]
	}
	return []byte(r.out), r.err
}
