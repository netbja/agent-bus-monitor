package notify

import (
	"regexp"
	"strings"
	"testing"

	"rsc.io/qr"
)

// notify-guided-setup §3: a topic is agentbus- and 20 letters or digits from
// crypto/rand, never asked.
func TestDrawTopic(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tp := DrawTopic()
		if !regexp.MustCompile(`^agentbus-[a-z0-9]{20}$`).MatchString(tp) || seen[tp] {
			t.Fatalf("topic %q", tp)
		}
		seen[tp] = true
	}
}

// §5.3, §9 The QR: half blocks, two modules per cell in height, a 4-module
// quiet zone; decoding the drawn rows gives back the code's matrix.
func TestQRRoundTrip(t *testing.T) {
	for _, link := range []string{"https://ntfy.sh/agentbus-goldentopic0000000", "http://192.168.1.20:8080/agentbus-abcdefghijklmnopqrst"} {
		rows, err := QR(link)
		if err != nil {
			t.Fatal(err)
		}
		code, _ := qr.Encode(link, qr.L)
		n := code.Size + 2*qrQuiet
		if len(rows) != (n+1)/2 {
			t.Fatalf("%s: %d rows for %d modules", link, len(rows), n)
		}
		dark := func(x, y int) bool {
			x, y = x-qrQuiet, y-qrQuiet
			return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
		}
		for r, row := range rows {
			cells := []rune(row)
			if len(cells) != n {
				t.Fatalf("row %d is %d cells, want %d", r, len(cells), n)
			}
			for x, c := range cells {
				top, bottom := dark(x, 2*r), dark(x, 2*r+1)
				want := map[[2]bool]rune{{false, false}: ' ', {true, false}: '▀', {false, true}: '▄', {true, true}: '█'}[[2]bool{top, bottom}]
				if c != want {
					t.Fatalf("%s: row %d cell %d = %q, want %q", link, r, x, c, want)
				}
			}
		}
	}
}

// §5.3 The sizes: an ntfy.sh link is a version-3 code, 37 cells by 19 rows
// with its quiet zone; an own server's longer link stays within version 6.
func TestQRSizes(t *testing.T) {
	rows, _ := QR("https://ntfy.sh/agentbus-goldentopic0000000")
	if len(rows) != 19 || len([]rune(rows[0])) != 37 {
		t.Errorf("ntfy.sh: %d×%d", len([]rune(rows[0])), len(rows))
	}
	rows, _ = QR("https://notify.my-own-server.example.org:8443/agentbus-abcdefghijklmnopqrst")
	if w := len([]rune(rows[0])); w > 49 || len(rows) > 25 {
		t.Errorf("own server: %d×%d", w, len(rows))
	}
	if _, err := QR(strings.Repeat("x", 400)); err == nil {
		t.Error("a link past version 6 drawn")
	}
}
