package holder

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// fakeClient is a client's input box as agy draws it (`>` between two `─`
// rules), so agy's signature judges it: what is submitted is printed above
// the box as SUBMITTED: <line>. FAKE picks a behaviour:
//   - fake-no2004: never turns bracketed paste on;
//   - fake-noland: a paste never shows in the input;
//   - fake-dialog: a paste opens a menu (> 1. Yes) in place of the input;
//   - fake-return: after a submit, it puts text back into its input;
//   - fake-slow: a paste shows in the input only after 300 ms;
//   - fake-stale: a paste shows another wake line's token in the input.
func fakeClient() {
	mode := os.Getenv("FAKE")
	old, err := term.MakeRaw(0)
	if err != nil {
		fmt.Println("no raw:", err)
		return
	}
	defer term.Restore(0, old)
	if mode != "fake-no2004" {
		os.Stdout.WriteString("\x1b[?2004h")
	}
	var history []string
	input, dialog := "", false
	draw := func() {
		var b strings.Builder
		b.WriteString("\x1b[2J\x1b[H")
		for _, h := range history {
			b.WriteString("SUBMITTED: " + h + "\r\n")
		}
		b.WriteString("────────────\r\n")
		if dialog {
			b.WriteString("> 1. Yes\r\n  2. No\r\n")
		} else {
			b.WriteString("> " + input + "\r\n")
		}
		b.WriteString("────────────")
		os.Stdout.WriteString(b.String())
	}
	draw()
	buf := make([]byte, 4096)
	pasting := false
	var pasted string
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		s := string(buf[:n])
		for len(s) > 0 {
			switch {
			case strings.HasPrefix(s, "\x1b[200~"):
				pasting, pasted, s = true, "", s[6:]
				if mode == "fake-dialog" {
					dialog = true
				}
				continue
			case strings.HasPrefix(s, "\x1b[201~"):
				pasting, s = false, s[6:]
				switch mode {
				case "fake-noland", "fake-dialog":
				case "fake-stale": // the input shows another wake's token, never this one
					input += "[agentbus#000000] an older wake line"
				case "fake-slow":
					p := pasted
					go func() { time.Sleep(300 * time.Millisecond); input += p; draw() }()
				default:
					input += pasted
				}
				continue
			case pasting:
				pasted += s[:1]
			case s[0] == '\r':
				if dialog {
					dialog = false
				} else {
					history = append(history, input)
					input = ""
					if mode == "fake-return" {
						input = "a queued message"
					}
				}
			case s[0] == 0x04: // ctrl+d: a menu opens (a permission request)
				dialog = true
			case s[0] == 0x15: // ctrl+u
				input = ""
			case s[0] == 0x1b: // another sequence: skipped
				end := 1
				for end < len(s) && (s[end] < 0x40 || s[end] > 0x7e || end == 1) {
					end++
				}
				s = s[min(end+1, len(s)):]
				continue
			default:
				input += s[:1]
			}
			s = s[1:]
		}
		draw()
	}
}
