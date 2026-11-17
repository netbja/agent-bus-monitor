package main

// agentbus fake-client claude <Claude Code's argv>: a Claude Code as the bus
// sees it, for the holder's live check (agentbus check holder-restart
// --clients=fake) and, later, its soak run (holder hardening spec §5). A
// test project puts it first on its PATH as `claude`, so everything else is
// the production path: the launcher (preflight, the bind, the exec), the
// holder, the dispatcher, self-repair, the drain.
//
// What it does of Claude Code: it answers --help with the flags the
// launcher's preflight asks for; it runs the hooks of its --settings as
// Claude Code does (each command through sh, the event's JSON on stdin, as
// the process's children, so the bus proves them by their ancestors); it
// starts on its --resume session or on a new one; its boot line is a turn
// whose steps publish its presence (`agentbus status <agent> idle`); it draws
// Claude's input box as the claude signature reads it (fakeClaudeUI); and a
// line submitted that runs `subscribe --drain <agent>` (the wake line) is a
// turn that runs that drain. Everything else typed is a turn that does
// nothing. It holds no model, no conversation and no network: its cost is
// the broker's.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/netbja/agent-bus-monitor/bus"
)

// fakeClaudeHelp is the --help the launcher's preflight reads.
const fakeClaudeHelp = `Usage: claude [options] [prompt]

agentbus fake-client: a fake Claude Code for the holder's live check.

Options:
  --model <model>
  --fallback-model <model>
  --resume <session>
  --permission-mode <mode>
  --settings <json>
  --effort <level>
  --name <name>
  --append-system-prompt <prompt>
  --append-system-prompt-file <file>
  -h, --help
`

// fakeClaudeFlags are Claude Code's flags that take a value.
var fakeClaudeFlags = map[string]bool{"--model": true, "--fallback-model": true, "--resume": true, "--permission-mode": true,
	"--settings": true, "--effort": true, "--name": true, "--append-system-prompt": true, "--append-system-prompt-file": true}

// fakeClaudeArgs is what the fake reads of a launch's argv.
type fakeClaudeArgs struct {
	Resume, Name, Prompt string
	Hooks                map[string][]string // event: its commands, in order
}

// parseFakeClaude reads a launch's argv (after `claude`).
func parseFakeClaude(argv []string) (fakeClaudeArgs, error) {
	a := fakeClaudeArgs{Hooks: map[string][]string{}}
	for i := 0; i < len(argv); i++ {
		f := argv[i]
		if !fakeClaudeFlags[f] {
			if strings.HasPrefix(f, "-") {
				return a, fmt.Errorf("fake-client: unknown flag %s", f)
			}
			a.Prompt = f
			continue
		}
		if i+1 >= len(argv) {
			return a, fmt.Errorf("fake-client: %s needs a value", f)
		}
		i++
		switch v := argv[i]; f {
		case "--resume":
			if !bus.ValidSessionID(v) {
				return a, fmt.Errorf("fake-client: invalid session %q", v)
			}
			a.Resume = v
		case "--name":
			a.Name = v
		case "--settings":
			var s struct {
				Hooks map[string][]struct {
					Hooks []struct {
						Type, Command string
					} `json:"hooks"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(v), &s); err != nil {
				return a, fmt.Errorf("fake-client: --settings: %w", err)
			}
			for ev, ms := range s.Hooks {
				for _, m := range ms {
					for _, h := range m.Hooks {
						if h.Type == "command" {
							a.Hooks[ev] = append(a.Hooks[ev], h.Command)
						}
					}
				}
			}
		}
	}
	return a, nil
}

// newFakeSession is a new session id (a UUID v4, as Claude Code's).
func newFakeSession() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// runFakeClient is `agentbus fake-client <client> <argv>`; only claude is
// faked.
func runFakeClient(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "claude" {
		fmt.Fprintln(stderr, "usage: agentbus fake-client claude [Claude Code's options] [prompt]")
		return 2
	}
	for _, a := range args[1:] {
		if a == "--help" || a == "-h" {
			fmt.Fprint(stdout, fakeClaudeHelp)
			return 0
		}
	}
	a, err := parseFakeClaude(args[1:])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	project, agent := os.Getenv("AGENT_BUS_PROJECT"), os.Getenv("AGENT_BUS_AGENT")
	if !bus.ValidName(project) || !bus.ValidName(agent) {
		fmt.Fprintln(stderr, "fake-client: AGENT_BUS_PROJECT and AGENT_BUS_AGENT must name a launched agent")
		return 2
	}
	bin := os.Getenv("AGENTBUS_BIN")
	if bin == "" {
		if bin, err = os.Executable(); err != nil {
			bin = "agentbus"
		}
	}
	f := &fakeClaude{args: a, project: project, agent: agent, bin: bin, session: a.Resume, log: stderr}
	if f.session == "" {
		f.session = newFakeSession()
	}
	in, _ := stdin.(*os.File)
	if in == nil || !term.IsTerminal(int(in.Fd())) {
		fmt.Fprintln(stderr, "fake-client: not on a terminal")
		return 2
	}
	old, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		fmt.Fprintln(stderr, "fake-client:", err)
		return 1
	}
	defer term.Restore(int(in.Fd()), old)
	cols := 100
	if out, ok := stdout.(*os.File); ok {
		if w, _, err := term.GetSize(int(out.Fd())); err == nil && w > 20 {
			cols = w
		}
	}
	ui := newFakeClaudeUI(project, agent, cols, stdout)
	if a.Resume != "" {
		ui.history = append(ui.history, "resumed "+a.Resume)
	}
	f.hooks("SessionStart", "")
	if a.Prompt != "" { // the boot line: its steps publish the presence
		ui.history = append(ui.history, "SUBMITTED "+a.Prompt)
		f.hooks("UserPromptSubmit", a.Prompt)
		ui.draw("", true, false)
		f.agentbus("status", agent, "idle", agent+" online")
		f.hooks("Stop", "")
	}
	ui.draw("", false, true)
	ui.loop(stdin, func(line string) { f.hooks("UserPromptSubmit", line) }, func(line string) {
		time.Sleep(300 * time.Millisecond)
		if strings.Contains(line, "subscribe --drain "+agent) {
			out := f.agentbus("subscribe", "--drain", agent)
			ui.history = append(ui.history, "drained "+strconv.Itoa(strings.Count(out, `"event":"cmd"`)))
		}
		f.hooks("Stop", "")
	})
	return 0
}

// fakeClaude is one fake's run.
type fakeClaude struct {
	args                         fakeClaudeArgs
	project, agent, bin, session string
	log                          io.Writer
}

// hooks runs event's commands as Claude Code does: each through sh, the
// event's JSON on stdin, as this process's children; a failure is logged,
// never fatal.
func (f *fakeClaude) hooks(event, prompt string) {
	in := map[string]string{"hook_event_name": event, "session_id": f.session}
	if prompt != "" {
		in["prompt"] = prompt
	}
	b, _ := json.Marshal(in)
	for _, c := range f.args.Hooks[event] {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", c)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(b), io.Discard, io.Discard
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(f.log, "fake-client: hook %s: %v\n", event, err)
		}
		cancel()
	}
}

// agentbus runs an agentbus command of the agent's turn (its child) and
// returns its output.
func (f *fakeClaude) agentbus(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.bin, append([]string{"--project", f.project}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(f.log, "fake-client: agentbus %s: %v\n", strings.Join(args, " "), err)
	}
	return string(out)
}

// fakeClaudeUI is Claude Code's screen as the claude signature reads it (a
// rule, `❯` + NBSP + a dim placeholder, a rule) and its input: bracketed
// paste, Enter submits. Shared by the fake client and the release test's.
type fakeClaudeUI struct {
	project, agent string
	rule           string
	history        []string
	out            io.Writer
}

func newFakeClaudeUI(project, agent string, cols int, out io.Writer) *fakeClaudeUI {
	io.WriteString(out, "\x1b[?2004h")
	return &fakeClaudeUI{project: project, agent: agent, out: out,
		rule: "\x1b[38;2;136;136;136m" + strings.Repeat("─", cols-2) + "\x1b[0m"}
}

// draw is the screen; titled writes Claude's turn-end title where it was in
// the field, right after `❯` (vt-osc-title).
func (u *fakeClaudeUI) draw(input string, busy, titled bool) {
	var w strings.Builder
	// Redrawn in place, as Claude's renderer does: a full-screen erase (ED 2)
	// would push the turn's spinner line into the scrollback the dispatcher
	// reads, and the agent would read busy for good.
	w.WriteString("\x1b[H\x1b[J\x1b[1mClaude Code\x1b[0m (e2e fake)\r\n\r\n")
	for _, h := range u.history {
		w.WriteString("● " + h + "\r\n")
	}
	if busy {
		w.WriteString("✶ Kneading… (1s)\r\n")
	}
	w.WriteString(u.rule + "\r\n❯ ")
	if titled {
		w.WriteString("\x1b]0;✳ " + u.project + ":" + u.agent + "\x07")
	}
	if input == "" {
		w.WriteString("\x1b[2mTry \"fix the tests\"\x1b[0m\r\n")
	} else {
		w.WriteString(input + "\r\n")
	}
	w.WriteString(u.rule + "\r\n  \x1b[38;5;30mModel: fake\x1b[0m")
	io.WriteString(u.out, w.String())
}

// loop reads the input until it ends: a line submitted is a turn, start
// before the busy screen, work during it, then the screen idle and titled.
func (u *fakeClaudeUI) loop(in io.Reader, start, work func(line string)) {
	var input []byte
	pasting := false
	var seq []byte // an escape sequence being read
	buf := make([]byte, 4096)
	for {
		n, err := in.Read(buf)
		if err != nil || n == 0 {
			return
		}
		for _, c := range buf[:n] {
			if len(seq) > 0 || c == 0x1b {
				seq = append(seq, c)
				switch s := string(seq); {
				case s == "\x1b[200~":
					pasting, seq = true, nil
				case s == "\x1b[201~":
					pasting, seq = false, nil
					u.draw(string(input), false, false)
				case len(seq) > 6 || len(seq) > 1 && !strings.HasPrefix("\x1b[200~", s) && !strings.HasPrefix("\x1b[201~", s):
					seq = nil // not ours: dropped
				}
				continue
			}
			if c == '\r' && !pasting {
				line := string(input)
				input = input[:0]
				if line == "" {
					continue
				}
				u.history = append(u.history, "SUBMITTED "+line)
				start(line)
				u.draw("", true, false)
				work(line)
				u.draw("", false, true)
				continue
			}
			if c >= 0x20 || pasting {
				input = append(input, c)
				if !pasting {
					u.draw(string(input), false, false)
				}
			}
		}
	}
}
