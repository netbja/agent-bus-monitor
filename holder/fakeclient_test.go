package holder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
//   - fake-stale: a paste shows another wake line's token in the input;
//   - fake-ctl: driven by files in $FAKE_CTL/$AGENTBUS_TERM_ID, read every
//     20 ms: notready draws no input box, notempty puts text in the input,
//     noland makes a paste open a menu, reset closes the menu and empties
//     the input (once);
//   - fake-busy: a paste never shows, and a spinner redraws every 20 ms;
//   - fake-dialog-close: as fake-dialog, the menu closing by itself after
//     1.5 s (past the tests' submit_wait), the input empty again.
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
	var mu sync.Mutex
	notReady := false
	draw := func() {
		var b strings.Builder
		b.WriteString("\x1b[2J\x1b[H")
		for _, h := range history {
			b.WriteString("SUBMITTED: " + h + "\r\n")
		}
		if notReady {
			b.WriteString("working…")
			os.Stdout.WriteString(b.String())
			return
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
	if mode == "fake-ctl" {
		dir := filepath.Join(os.Getenv("FAKE_CTL"), os.Getenv("AGENTBUS_TERM_ID"))
		has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
		go func() {
			for range time.Tick(20 * time.Millisecond) {
				mu.Lock()
				nr, ne := has("notready"), has("notempty")
				changed := nr != notReady
				notReady = nr
				if has("reset") {
					os.Remove(filepath.Join(dir, "reset"))
					dialog, input, changed = false, "", true
				}
				if ne && input == "" {
					input, changed = "a draft the program put back", true
				}
				if changed {
					draw()
				}
				mu.Unlock()
			}
		}()
	}
	mu.Lock()
	draw()
	mu.Unlock()
	buf := make([]byte, 4096)
	pasting := false
	var pasted string
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		s := string(buf[:n])
		mu.Lock()
		noland := mode == "fake-ctl" && func() bool {
			_, err := os.Stat(filepath.Join(os.Getenv("FAKE_CTL"), os.Getenv("AGENTBUS_TERM_ID"), "noland"))
			return err == nil
		}()
		for len(s) > 0 {
			switch {
			case strings.HasPrefix(s, "\x1b[200~"):
				pasting, pasted, s = true, "", s[6:]
				if mode == "fake-dialog" || mode == "fake-dialog-close" || noland {
					dialog = true
				}
				if mode == "fake-dialog-close" {
					go func() { time.Sleep(1500 * time.Millisecond); dialog = false; draw() }()
				}
				continue
			case strings.HasPrefix(s, "\x1b[201~"):
				pasting, s = false, s[6:]
				switch mode {
				case "fake-busy":
					go func() {
						for i := 0; ; i++ {
							os.Stdout.WriteString(fmt.Sprintf("\x1b[1;1H%c", "|/-\\"[i%4]))
							time.Sleep(20 * time.Millisecond)
						}
					}()
				case "fake-noland", "fake-dialog", "fake-dialog-close":
				case "fake-ctl":
					if !noland {
						input += pasted
					}
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
			case strings.HasPrefix(s, "\x1b[13u"): // kitty's Enter: as a CR
				s = "\r" + s[5:]
				continue
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
		mu.Unlock()
	}
}
