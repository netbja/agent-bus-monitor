package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Prompter is where questions go: Yes (--yes) takes every default; TTY is
// the terminal to ask on, nil when there is none.
type Prompter struct {
	Yes bool
	TTY io.ReadWriter
	rd  *bufio.Reader // one for the session: answers pasted ahead stay buffered for their questions
	// aborted: a question found no terminal; the run ends there.
	aborted bool
}

// Aborted reports whether a question found no terminal to ask on.
func (p *Prompter) Aborted() bool { return p.aborted }

// ErrNoTerminal: a question with no terminal and no --yes, so nothing is
// removed unattended by accident.
var ErrNoTerminal = errors.New("no terminal to ask on")

// Ask is setup.sh's ask: yes/no, def the default. An empty answer or EOF
// takes the default, which every caller makes the cautious choice.
func Ask(o *Output, p *Prompter, question string, def bool) (bool, error) {
	if p.Yes {
		return def, nil
	}
	if p.TTY == nil {
		if !p.aborted {
			o.Fatal("no terminal to ask on: re-run with --yes to accept the cautious defaults")
		}
		p.aborted = true
		return false, ErrNoTerminal
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(p.TTY, "    ? %s %s ", question, hint)
	if p.rd == nil {
		p.rd = bufio.NewReader(p.TTY)
	}
	line, _ := p.rd.ReadString('\n') // EOF: an empty answer
	switch strings.TrimSpace(line) {
	case "":
		return def, nil
	case "y", "Y", "yes", "YES":
		return true, nil
	}
	return false, nil
}

// OpenTTY opens the controlling terminal when there is one (a real isatty,
// IsTerminal), else nil: setup.sh asks on /dev/tty, not on stdin.
func OpenTTY() io.ReadWriteCloser {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	if !IsTerminal(f) {
		f.Close()
		return nil
	}
	return f
}
