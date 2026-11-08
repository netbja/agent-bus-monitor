package notify

import (
	"crypto/rand"
	"fmt"
	"strings"

	"rsc.io/qr"
)

// DrawTopic is a new ntfy topic: agentbus- and 20 letters or digits from the
// system's random source (on a public server its name is its only lock;
// notify-guided-setup §3).
func DrawTopic() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 0, 20)
	buf := make([]byte, 32)
	for len(out) < 20 {
		if _, err := rand.Read(buf); err != nil {
			panic(err) // crypto/rand does not fail on a supported system
		}
		for _, c := range buf {
			if c < 252 && len(out) < 20 { // 252 = 7*36: no letter drawn more often
				out = append(out, chars[int(c)%len(chars)])
			}
		}
	}
	return "agentbus-" + string(out)
}

// qrQuiet is the quiet zone around a code, in modules.
const qrQuiet = 4

// qrMaxModules is version 6 (41 modules): the largest code §5.3 draws.
const qrMaxModules = 41

// QR is link as a QR code (L correction) in half blocks: each row of text
// is two rows of modules, a dark module drawn ink (▀ ▄ █), a light one a
// space, with a 4-module quiet zone. The caller draws it dark on light
// (§5.3: black on white whatever the theme, so a camera reads it).
func QR(link string) ([]string, error) {
	code, err := qr.Encode(link, qr.L)
	if err != nil {
		return nil, fmt.Errorf("the link cannot be drawn as a code")
	}
	if code.Size > qrMaxModules {
		return nil, fmt.Errorf("the link is too long for a code a phone reads from a terminal")
	}
	n := code.Size + 2*qrQuiet
	dark := func(x, y int) bool {
		x, y = x-qrQuiet, y-qrQuiet
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	var rows []string
	for y := 0; y < n; y += 2 {
		var b strings.Builder
		for x := 0; x < n; x++ {
			switch top, bottom := dark(x, y), dark(x, y+1); {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		rows = append(rows, b.String())
	}
	return rows, nil
}
