package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/termrec"
)

// agentbus term (herdr exit step 3, spec 2026-11-10-herdr-exit-design.md
// 6.4): record a client under our PTY to a raw fixture, and print one. No
// project, no broker. The recorder writes nothing to a client's own files;
// what the client writes when it runs (its trust or session files) is the
// client's, so a probe that would write one is the person's to run.

const termUsage = `usage: agentbus term record <client> <fixture> [--script <file>] [--cmd "<command line>"] [--size <cols>x<rows>] [--out <dir>] [--force]
       agentbus term dump <recording> [--between <mark> <mark>]`

// validFixture is a fixture's name: its file is <out>/<client>/<fixture>.jsonl.
var validFixture = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// builtinScripts are the probes the spec names, by fixture.
var builtinScripts = map[string]string{
	// f12-at-input (4.6): type abc at the input, F12 in the legacy then the
	// kitty encoding, 2 s each; F12 is free when nothing is written between
	// the marks (before-f12 to after-f12-kitty).
	"f12-at-input": `quiet 1500ms max 60s
type abc
quiet 800ms max 10s
mark before-f12
key f12
sleep 2s
mark after-f12-legacy
key f12-kitty
sleep 2s
mark after-f12-kitty
`,
	// empty: the input as the client starts, nothing typed.
	"empty": "quiet 1500ms max 60s\nmark settled\n",
	// typed: zz-probe typed, then cleared with ctrl+u.
	"typed": "quiet 1500ms max 60s\ntype zz-probe\nquiet 800ms max 10s\nmark typed\nkey ctrl+u\nquiet 800ms max 10s\nmark cleared\n",
}

// termDeps are what term reads from the machine.
type termDeps struct {
	record  func(ctx context.Context, w io.Writer, o termrec.Options) error
	version func(exe string) string
	look    func(exe string) (string, error)
}

func realTermDeps() termDeps {
	return termDeps{record: termrec.Record, version: clientVersion, look: exec.LookPath}
}

// clientVersion is the first line of `<exe> --version`, "" when it says
// nothing in 5 s.
func clientVersion(exe string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "--version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

func runTerm(args []string, d termDeps, stdout, stderr io.Writer) int {
	usage := func() int { fmt.Fprintln(stderr, termUsage); return 2 }
	fail := func(err error) int { fmt.Fprintln(stderr, "error: "+err.Error()); return 1 }
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "dump":
		return termDump(args[1:], stdout, usage, fail)
	case "record":
	default:
		return usage()
	}
	rest := args[1:]
	rest, scriptFile := extractFlag(rest, "--script")
	rest, cmdLine := extractFlag(rest, "--cmd")
	rest, size := extractFlag(rest, "--size")
	rest, out := extractFlag(rest, "--out")
	rest, force := extractBool(rest, "--force")
	if len(rest) != 2 {
		return usage()
	}
	client, fixture := rest[0], rest[1]
	if _, ok := agentstate.Profiles()[client]; !ok {
		return fail(fmt.Errorf("client %q has no profile (one of %s)", client, strings.Join(agentstate.Clients(), ", ")))
	}
	if !validFixture.MatchString(fixture) {
		return fail(fmt.Errorf("fixture %q: lowercase letters, digits and -", fixture))
	}
	cols, rows := 120, 40
	if size != "" {
		c, r, ok := strings.Cut(size, "x")
		var err1, err2 error
		cols, err1 = strconv.Atoi(c)
		rows, err2 = strconv.Atoi(r)
		if !ok || err1 != nil || err2 != nil || cols < 20 || rows < 5 || cols > 1000 || rows > 1000 {
			return fail(fmt.Errorf("size %q: <cols>x<rows>, at least 20x5", size))
		}
	}
	script, ok := builtinScripts[fixture]
	if scriptFile != "" {
		b, err := os.ReadFile(scriptFile)
		if err != nil {
			return fail(err)
		}
		script, ok = string(b), true
	}
	if !ok {
		return fail(fmt.Errorf("fixture %q has no built-in script: pass --script", fixture))
	}
	steps, err := termrec.ParseScript(script)
	if err != nil {
		return fail(err)
	}
	argv := []string{client} // the profile's client name is its binary (agentstate/clients)
	if cmdLine != "" {
		argv = strings.Fields(cmdLine)
	}
	exe, err := d.look(argv[0])
	if err != nil {
		return fail(fmt.Errorf("%s: %w", argv[0], err))
	}
	if out == "" {
		out = filepath.Join("agentstate", "testdata", "raw")
	}
	path := filepath.Join(out, client, fixture+".jsonl")
	if _, err := os.Stat(path); err == nil && !force {
		return fail(fmt.Errorf("%s exists: --force replaces it", path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fail(err)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	err = d.record(ctx, f, termrec.Options{Client: client, Fixture: fixture, ClientVersion: d.version(exe),
		Argv: append([]string{exe}, argv[1:]...), Cols: cols, Rows: rows, Steps: steps})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return fail(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // nothing left on failure (reviewer-codex)
		return fail(err)
	}
	fmt.Fprintln(stdout, "recorded "+path)
	return 0
}

// termDump prints a recording: its header, then one line an event, the
// bytes quoted; with --between, only what the client wrote between two
// marks.
func termDump(args []string, stdout io.Writer, usage func() int, fail func(error) int) int {
	var between []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--between" && i+2 < len(args) {
			between = args[i+1 : i+3]
			args = append(append([]string{}, args[:i]...), args[i+3:]...)
			break
		}
	}
	if len(args) != 1 {
		return usage()
	}
	f, err := os.Open(args[0])
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	rec, err := termrec.Read(f)
	if err != nil {
		return fail(err)
	}
	if between != nil {
		b, ok := rec.OutputBetween(between[0], between[1])
		if !ok {
			return fail(errors.New("no such pair of marks"))
		}
		fmt.Fprintf(stdout, "%d bytes between %s and %s: %q\n", len(b), between[0], between[1], b)
		return 0
	}
	h := rec.Header
	fmt.Fprintf(stdout, "%s %s (%s) %dx%d %s %q\n", h.Client, h.Fixture, h.ClientVersion, h.Cols, h.Rows, h.Start.Format(time.RFC3339), h.Argv)
	for _, e := range rec.Events {
		at := fmt.Sprintf("%8.3fs %-6s", float64(e.T)/1000, e.Kind)
		switch e.Kind {
		case termrec.KindOutput, termrec.KindInput:
			fmt.Fprintf(stdout, "%s %q\n", at, e.Bytes())
		case termrec.KindResize:
			fmt.Fprintf(stdout, "%s %dx%d\n", at, e.Cols, e.Rows)
		default:
			fmt.Fprintf(stdout, "%s %q\n", at, e.Text)
		}
	}
	return 0
}
