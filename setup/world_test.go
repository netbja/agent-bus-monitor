package setup

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// installWorld is an installer on a fake runner, its checkout and home in
// temporary folders.
func installWorld(t *testing.T) (*Installer, *bytes.Buffer, *fakeRunner, string) {
	t.Helper()
	repo, home := t.TempDir(), t.TempDir()
	f := &fakeRunner{t: t, paths: map[string]string{}, cmds: map[string]fakeCmd{}}
	var out bytes.Buffer
	env := map[string]string{"HOME": home}
	in := &Installer{R: f, O: NewOutput(&out, &bytes.Buffer{}, false), Opt: Options{BinDir: home + "/bin"}, Repo: repo, Tmp: t.TempDir(),
		Getenv: func(k string) string { return env[k] }, Now: func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.Local) }}
	return in, &out, f, home
}

func TestServicesHint(t *testing.T) {
	ctx := context.Background()
	in, out, f, _ := installWorld(t)
	ab := in.Opt.BinDir + "/agentbus"
	_ = os.MkdirAll(in.Opt.BinDir, 0o755)
	_ = os.WriteFile(ab, []byte("#!/bin/sh\n"), 0o755)
	f.cmds[ab+" service list"] = fakeCmd{out: "no agentbus services installed\n"}
	in.ServicesHint(ctx)
	if out.String() != "" {
		t.Fatalf("%q", out.String())
	}
	f.cmds[ab+" service list"] = fakeCmd{out: "demo dispatch running since 2h\ndemo missions stopped\n"}
	in.ServicesHint(ctx)
	if want := "    • project services keep running the previous agentbus until restarted:\n" +
		"    •   demo dispatch (running since 2h): agentbus --project demo service restart dispatch\n" +
		"    •   demo missions (stopped): agentbus --project demo service restart missions\n"; out.String() != want {
		t.Fatalf("%q", out.String())
	}
	in.Opt.DryRun = true
	out.Reset()
	in.ServicesHint(ctx)
	if out.String() != "" {
		t.Fatalf("%q", out.String())
	}
}

// An upgrade never suggests restarting the holder: a new build starts its
// own instance and the old one drains (herdr exit spec 4.4a).
func TestServicesHintSkipsTheHolder(t *testing.T) {
	in, out, f, _ := installWorld(t)
	ab := in.Opt.BinDir + "/agentbus"
	_ = os.MkdirAll(in.Opt.BinDir, 0o755)
	_ = os.WriteFile(ab, []byte("#!/bin/sh\n"), 0o755)
	f.cmds[ab+" service list"] = fakeCmd{out: "demo                     holder    active, build v1\n"}
	in.ServicesHint(context.Background())
	if out.String() != "" {
		t.Fatalf("%q", out.String())
	}
	f.cmds[ab+" service list"] = fakeCmd{out: "demo dispatch running since 2h\ndemo                     holder    active, build v1\n"}
	in.ServicesHint(context.Background())
	if strings.Contains(out.String(), "holder") || !strings.Contains(out.String(), "demo dispatch") {
		t.Fatalf("%q", out.String())
	}
}

// P2-13: after an install, the services still running an older build are
// listed with their restart, and restarted only on the person's yes; --yes
// (the cautious defaults) and no terminal restart nothing. A holder is never
// listed (service stale never names one).
func TestRestartStaleServices(t *testing.T) {
	ctx := context.Background()
	setupWorld := func(t *testing.T) (*Installer, *bytes.Buffer, *fakeRunner, string) {
		in, out, f, _ := installWorld(t)
		ab := in.Opt.BinDir + "/agentbus"
		_ = os.MkdirAll(in.Opt.BinDir, 0o755)
		_ = os.WriteFile(ab, []byte("#!/bin/sh\n"), 0o755)
		f.cmds[ab+" service stale"] = fakeCmd{out: "beta dispatch old\nbeta missions -\n"}
		return in, out, f, ab
	}
	// no: listed, nothing restarted (the fake fails on any restart)
	in, out, _, _ := setupWorld(t)
	in.RestartStale(ctx, &Prompter{TTY: fakeTTY{strings.NewReader("n\n"), &bytes.Buffer{}}})
	for _, want := range []string{"older agentbus build", "beta dispatch (old): agentbus --project beta service restart dispatch",
		"beta missions (no build recorded): agentbus --project beta service restart missions"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("%q lacks %q", out.String(), want)
		}
	}
	// --yes takes the cautious default: nothing restarted
	in, _, _, _ = setupWorld(t)
	in.RestartStale(ctx, &Prompter{Yes: true})
	// no terminal: listed, not asked, nothing restarted, the install not failed
	in, out, _, _ = setupWorld(t)
	p := &Prompter{}
	in.RestartStale(ctx, p)
	if p.Aborted() || !strings.Contains(out.String(), "beta dispatch") {
		t.Fatalf("no terminal: aborted %v, %q", p.Aborted(), out.String())
	}
	// yes: each listed service restarted, through the installed agentbus
	in, out, f, ab := setupWorld(t)
	f.cmds[ab+" --project beta service restart dispatch"] = fakeCmd{}
	f.cmds[ab+" --project beta service restart missions"] = fakeCmd{}
	in.RestartStale(ctx, &Prompter{TTY: fakeTTY{strings.NewReader("y\n"), &bytes.Buffer{}}})
	n := 0
	for _, r := range f.ran {
		if strings.Contains(r, "service restart") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("restarted %d: %q", n, f.ran)
	}
	// nothing stale: one line, no question
	in, out, f, ab = setupWorld(t)
	f.cmds[ab+" service stale"] = fakeCmd{out: ""}
	in.RestartStale(ctx, &Prompter{TTY: fakeTTY{strings.NewReader(""), &bytes.Buffer{}}})
	if !strings.Contains(out.String(), "every project service runs this build") {
		t.Fatalf("%q", out.String())
	}
}
