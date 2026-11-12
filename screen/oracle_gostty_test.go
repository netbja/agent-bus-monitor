//go:build gostty

package screen

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/ironpark/gostty"
)

// The oracle (spec 6.4, the person's Q3: gostty for tests only, behind a
// build tag): Ghostty's VT core renders each corpus case; its plain text is
// the expected screen every candidate is held to. Production never links it.
//
//	go test ./screen -tags gostty -run TestWriteOracle -update-oracle
//
// rewrites testdata/oracle; without the flag it checks the committed files
// are still gostty's.
var updateOracle = flag.Bool("update-oracle", false, "rewrite testdata/oracle from gostty")

func gosttyPlain(t *testing.T, c corpusCase) string {
	t.Helper()
	s, err := gostty.New(uint16(c.cols), uint16(c.rows), gostty.WithTerminalOptions(gostty.WithScrollbackMaxLines(0)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Write([]byte(c.data)); err != nil {
		t.Fatal(err)
	}
	out, err := s.Terminal().FormatString(gostty.FormatOptions{Format: gostty.FormatterFormatPlain})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWriteOracle(t *testing.T) {
	for _, c := range corpus {
		got := gosttyPlain(t, c)
		path := filepath.Join("testdata", "oracle", c.name+".txt")
		if *updateOracle {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil || string(want) != got {
			t.Errorf("%s: the committed oracle is not gostty's (%v):\n%q\n%q", c.name, err, want, got)
		}
	}
}
