package setup

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// ask is setup.sh's: --yes takes the default; no terminal stops; an answer
// decides; an empty line or EOF takes the default, which every caller makes
// the cautious one (spec: Linux and macOS).
func TestAsk(t *testing.T) {
	var errw bytes.Buffer
	o := NewOutput(&bytes.Buffer{}, &errw, false)
	if ok, err := Ask(o, &Prompter{Yes: true}, "stop Redis?", true); !ok || err != nil {
		t.Fatalf("--yes, default y: %v %v", ok, err)
	}
	if ok, _ := Ask(o, &Prompter{Yes: true}, "purge?", false); ok {
		t.Fatal("--yes took the non-default")
	}
	if _, err := Ask(o, &Prompter{}, "q?", true); err == nil || !strings.Contains(errw.String(), "no terminal to ask on: re-run with --yes to accept the cautious defaults") {
		t.Fatalf("no terminal: %v %q", err, errw.String())
	}
	for in, want := range map[string]bool{"y\n": true, "yes\n": true, "Y\n": true, "n\n": false, "\n": false, "": false, "maybe\n": false} {
		var shown bytes.Buffer
		tty := fakeTTY{strings.NewReader(in), &shown}
		ok, err := Ask(o, &Prompter{TTY: tty}, "purge the data?", false)
		if err != nil || ok != want {
			t.Errorf("answer %q (default n): %v %v", in, ok, err)
		}
		if shown.String() != "    ? purge the data? [y/N] " {
			t.Errorf("prompt %q", shown.String())
		}
	}
	tty := fakeTTY{strings.NewReader(""), &bytes.Buffer{}}
	if ok, _ := Ask(o, &Prompter{TTY: tty}, "stop?", true); !ok {
		t.Fatal("EOF did not take the default y")
	}
}

// A character device is not a terminal: /dev/null is one, and cron, CI or a
// unit would read as interactive (spec).
func TestIsTerminalIsARealIsatty(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Fatal("/dev/null read as a terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if IsTerminal(r) {
		t.Fatal("a pipe read as a terminal")
	}
}

// fakeTTY reads the answer from r and records the prompt in w.
type fakeTTY struct {
	r io.Reader
	w io.Writer
}

func (f fakeTTY) Read(p []byte) (int, error)  { return f.r.Read(p) }
func (f fakeTTY) Write(p []byte) (int, error) { return f.w.Write(p) }

// One reader for the whole session (reviewer-codex): answers pasted for
// successive questions are each read by their own question.
func TestAskReadsPastedAnswersInTurn(t *testing.T) {
	o := NewOutput(&bytes.Buffer{}, &bytes.Buffer{}, false)
	p := &Prompter{TTY: fakeTTY{strings.NewReader("y\nn\ny\n"), &bytes.Buffer{}}}
	for i, want := range []bool{true, false, true} {
		if got, err := Ask(o, p, "q?", false); err != nil || got != want {
			t.Fatalf("question %d: %v %v, want %v", i, got, err, want)
		}
	}
}
