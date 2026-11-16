package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// agentbus attach (herdr exit spec 4.6): puts the person's terminal on an
// agent's terminal in the project's holder. Raw bytes both ways (the holder
// filters what reaches the person, S3); the prefix key (attach.prefix) is the
// way out. --observe never types or resizes.

// attachGate is the prefix rule per client: the key is free for the
// terminal's client → attach; bound → refused, naming the keys free for it;
// unknown (not probed, or no client) → refused unless the person chose the
// key with --prefix, then one warning line. An observer is never gated: it
// sends no input, so the prefix takes nothing from the program.
func attachGate(prefix string, explicit, observe bool, p agentstate.Profile) (warn string, err error) {
	if observe {
		return "", nil
	}
	client := p.Client
	if client == "" {
		client = "this program"
	}
	var free []string
	for k, s := range p.Keys {
		if s == "free" {
			free = append(free, k)
		}
	}
	sort.Strings(free)
	choices := "no key is verified free for it yet"
	if len(free) > 0 {
		choices = "free for it: " + strings.Join(free, ", ")
	}
	switch p.KeyStatus(prefix) {
	case "free":
		return "", nil
	case "bound":
		return "", fmt.Errorf("%s is bound by %s (%s); attach with --prefix <key>", prefix, client, choices)
	}
	if !explicit {
		return "", fmt.Errorf("%s is not verified for %s (%s); choose the key yourself with --prefix <key>", prefix, client, choices)
	}
	return fmt.Sprintf("%s is not verified for %s: press it twice to send it to the program", prefix, client), nil
}

// findAgentTerm is agent's live terminal among the project's holders (a
// terminal's role is the agent it runs): the current build's first, then any
// other's, the newest terminal of a build first.
func findAgentTerm(getenv func(string) string, project, agent string) (string, holder.TermInfo, error) {
	dir, err := holder.ProjectDir(getenv, project)
	if err != nil {
		return "", holder.TermInfo{}, err
	}
	byKey, err := holder.ProjectSockets(dir) // both names (P0-2 B)
	if err != nil {
		return "", holder.TermInfo{}, err
	}
	current := service.BuildKey(holder.BuildID())
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.SortStableFunc(keys, func(a, b string) int { // the current build first
		switch {
		case a == current && b != current:
			return -1
		case b == current && a != current:
			return 1
		}
		return strings.Compare(a, b)
	})
	for _, key := range keys {
		sock := byKey[key]
		cl, err := holder.Dial(sock, holder.BuildID())
		if err != nil {
			continue
		}
		r, err := cl.Call(holder.List{})
		cl.Close()
		if err != nil {
			continue
		}
		for i := len(r.Terms) - 1; i >= 0; i-- {
			if t := r.Terms[i]; t.Role == agent && t.Alive {
				return sock, t, nil
			}
		}
	}
	return "", holder.TermInfo{}, fmt.Errorf("no live terminal of %s in project %s's holders", agent, project)
}

// attachSession is one attach: the terminal, the prefix, the person's
// terminal as streams.
type attachSession struct {
	Socket  string
	Term    holder.TermInfo
	Prefix  string
	Observe bool
	Warn    string // the gate's warning line, written first
	In      io.Reader
	Out     io.Writer
	Size    func() (cols, rows int)
	Resized <-chan os.Signal // SIGWINCH (nil: none)
	// Banner is how long the way out is shown on screen before the agent's
	// screen (P1-8); a key ends it at once. 0: no banner (the title still
	// names it).
	Banner time.Duration
}

// attachBanner is how long agentbus attach shows the way out before the
// agent's screen.
const attachBanner = 2 * time.Second

// keyLabel is a prefix as the person reads it: F12, Ctrl-].
func keyLabel(k string) string { return config.AttachKeyLabel(k) }

// bannerText is the screen an attach starts on: which agent, and the way
// out, on a cleared screen, before the agent's screen replaces it.
func bannerText(agent, prefix string, observe bool) string {
	k := keyLabel(prefix)
	what := "Attached to " + agent + "."
	if observe {
		what = "Observing " + agent + " (read-only)."
	}
	more := "  " + k + " twice sends " + k + " to " + agent + ". Ctrl-C goes to " + agent + ", it does not leave.\r\n"
	if observe {
		more = "" // an observer sends nothing
	}
	return "\x1b[2J\x1b[H\r\n  " + what + "\r\n\r\n  To leave: press " + k + " then d.\r\n" + more + "\r\n  (any key continues)\r\n"
}

// attachRestore leaves the outer terminal as a shell expects it, whatever
// the program set through the stream: synchronized output off, kitty key
// flags popped, mouse and focus and paste reports off, the normal screen,
// the whole scroll region, the cursor shown, the colors reset, the title.
const attachRestore = "\x1b[?2026l\x1b[<u\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1004l\x1b[?2004l" +
	"\x1b[?1l\x1b>\x1b[?1049l\x1b[r\x1b[?25h\x1b[0m\x1b]2;\x07"

// escFlush is how long a lone ESC (or a cut sequence) waits for the rest
// before it goes as it is: the Escape key itself.
const escFlush = 30 * time.Millisecond

// runAttach runs one attach until the person detaches or the program ends.
func runAttach(s attachSession) error {
	scan, err := newKeyScanner(s.Prefix)
	if err != nil {
		return err
	}
	cl, err := holder.Dial(s.Socket, holder.BuildID())
	if err != nil {
		return err
	}
	defer cl.Close()
	var outMu sync.Mutex
	write := func(b []byte) {
		outMu.Lock()
		defer outMu.Unlock()
		_, _ = s.Out.Write(b)
	}
	title := func(t string) { write([]byte("\x1b]2;" + t + "\x07")) }
	// The title always names the way out (P1-8); a notice replaces it while
	// it lasts, then it comes back.
	way := "agentbus attach " + s.Term.Role + ": " + keyLabel(s.Prefix) + " then d detaches"
	if s.Warn != "" {
		way += "; " + s.Warn
	}
	idle := func() { title(way) }
	defer write([]byte(attachRestore))
	// Focus reports from the outer terminal (attachRestore turns them off):
	// the holder's person-active judgement needs to know when the person
	// looks away (reviewer-codex).
	write([]byte("\x1b[?1004h"))
	idle()

	chunks := make(chan []byte)
	go func() { // the person's keys
		buf := make([]byte, 4096)
		for {
			n, err := s.In.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()
	// The banner: the way out on screen before the agent's (P1-8). A key
	// ends it and is kept for the agent; a closed input ends the attach.
	var first []byte
	if s.Banner > 0 {
		write([]byte(bannerText(s.Term.Role, s.Prefix, s.Observe)))
		select {
		case <-time.After(s.Banner):
		case b, ok := <-chunks:
			if !ok {
				return nil
			}
			first = b
		}
		write([]byte("\x1b[2J\x1b[H"))
	}
	cols, rows := s.Size()
	if _, err := cl.Call(holder.Attach{Term: s.Term.ID, Observe: s.Observe, Cols: cols, Rows: rows}); err != nil {
		return err
	}

	var heldMu sync.Mutex
	ended := make(chan error, 1)
	var focus focusWatch // the program's focus-reporting resets, across frames
	go func() {          // the program's output, and the holder's notices
		for {
			typ, b, err := cl.ReadFrame()
			if err != nil {
				ended <- nil // the program ended, or the holder closed the stream
				return
			}
			if typ == holder.FrameData {
				write(b)
				if focus.Feed(b) {
					write([]byte("\x1b[?1004h"))
				}
				continue
			}
			if v, err := holder.DecodeControl(b); err == nil {
				if n, ok := v.(holder.HoldNotice); ok {
					heldMu.Lock()
					scan.held = n.Active
					heldMu.Unlock()
					if n.Active {
						title(fmt.Sprintf("agentbus attach: a %s may be left in the input; %d keys held: %s r sends them, %s x drops them", n.Kind, n.Held, keyLabel(s.Prefix), keyLabel(s.Prefix)))
					} else {
						idle()
					}
				}
			}
		}
	}()

	// apply runs the scanner's events; true when the attach ends.
	apply := func(evs []keyEvent) (bool, error) {
		for _, e := range evs {
			var err error
			switch e.Kind {
			case keySend:
				if !s.Observe {
					err = cl.SendData(e.Data)
				}
			case keyPending:
				title("agentbus attach: " + keyLabel(s.Prefix) + " … (d detach, " + keyLabel(s.Prefix) + " sends it)")
			case keyDone:
				idle()
			case keyDetach:
				_ = cl.SendControl(holder.Detach{})
				return true, nil
			case keyRelease:
				err = cl.SendControl(holder.Release{})
			case keyDiscard:
				err = cl.SendControl(holder.Discard{})
			case keyBell:
				idle()
				write([]byte("\a"))
			case keyFocusIn, keyFocusOut:
				err = cl.SendControl(holder.Focus{In: e.Kind == keyFocusIn})
			}
			if err != nil {
				return true, err
			}
		}
		return false, nil
	}

	flush := time.NewTimer(time.Hour)
	flush.Stop()
	feed := func(b []byte) (bool, error) {
		heldMu.Lock()
		evs := scan.Feed(b)
		pending := len(scan.buf) > 0
		heldMu.Unlock()
		if done, err := apply(evs); done {
			return true, err
		}
		if pending {
			flush.Reset(escFlush)
		}
		return false, nil
	}
	if first != nil { // the key that ended the banner
		if done, err := feed(first); done {
			return err
		}
	}
	for {
		select {
		case err := <-ended:
			return err
		case <-s.Resized:
			if !s.Observe {
				c, r := s.Size()
				_ = cl.SendControl(holder.Resize{Cols: c, Rows: r})
			}
		case b, ok := <-chunks:
			if !ok {
				return nil // the person's terminal closed
			}
			if done, err := feed(b); done {
				return err
			}
		case <-flush.C:
			heldMu.Lock()
			evs := scan.Flush()
			heldMu.Unlock()
			if done, err := apply(evs); done {
				return err
			}
		}
	}
}

// runAttachCmd is `agentbus --project <p> attach <agent> [--observe]
// [--prefix <key>]`.
func runAttachCmd(project string, args []string, stdout, stderr io.Writer) int {
	var agent, prefix string
	observe := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--observe":
			observe = true
		case a == "--prefix" && i+1 < len(args):
			prefix, i = args[i+1], i+1
		case !strings.HasPrefix(a, "-") && agent == "":
			agent = a
		default:
			fmt.Fprintln(stderr, "usage: agentbus --project <p> attach <agent> [--observe] [--prefix <key>]")
			return 2
		}
	}
	if agent == "" {
		fmt.Fprintln(stderr, "usage: agentbus --project <p> attach <agent> [--observe] [--prefix <key>]")
		return 2
	}
	explicit := prefix != ""
	if !explicit {
		r := config.Resolver{Getenv: os.Getenv, Files: []*config.File{config.Load(config.Path(os.Getenv), "config.toml")}}
		got := r.Get("attach.prefix")
		if got.Invalid {
			fmt.Fprintf(stderr, "agentbus attach: %s is not an attach prefix\n", config.EnvName("attach.prefix"))
			return 2
		}
		prefix = got.Value.(string)
	}
	if !config.AttachPrefixAllowed(prefix) {
		fmt.Fprintf(stderr, "agentbus attach: %q is not an attach prefix (f1-f12, ctrl+<letter>, ctrl+\\, ctrl+]; not ctrl+c/d/h/i/j/m/q/s/z)\n", prefix)
		return 2
	}
	sock, t, err := findAgentTerm(os.Getenv, project, agent)
	if err != nil {
		fmt.Fprintf(stderr, "agentbus attach: %v\n", err)
		return 1
	}
	warn, err := attachGate(prefix, explicit, observe, agentstate.Profiles()[t.Profile])
	if err != nil {
		fmt.Fprintf(stderr, "agentbus attach: refused: %v\n", err)
		return 1
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fmt.Fprintln(stderr, "agentbus attach: the standard input is not a terminal")
		return 1
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(stderr, "agentbus attach: %v\n", err)
		return 1
	}
	defer term.Restore(fd, old)
	size := func() (int, int) {
		c, r, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || c <= 0 || r <= 0 {
			return 0, 0 // the holder keeps its size
		}
		return c, r
	}
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	err = runAttach(attachSession{Socket: sock, Term: t, Prefix: prefix, Observe: observe, Warn: warn,
		In: os.Stdin, Out: os.Stdout, Size: size, Resized: winch, Banner: attachBanner})
	term.Restore(fd, old)
	var re *holder.RemoteError
	switch {
	case errors.As(err, &re):
		fmt.Fprintf(stderr, "agentbus attach: %v\n", err)
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "agentbus attach: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "detached from %s\n", agent)
	return 0
}
