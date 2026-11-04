package config

// Fuzz targets (plain `go test` runs only the seeds below). Fuzz one with
//
//	go test ./config -run '^$' -fuzz='^FuzzLoad$' -fuzztime=20s -fuzzminimizetime=0
//
// -fuzzminimizetime=0: the default 60 s minimization of each new interesting
// input stalls the run at 0 execs/s for most of a short -fuzztime.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzLoad: any config.toml (and busmon.toml) text loads without panicking;
// a warning names a line inside the file, and a key the file holds is one
// the registry knows.
func FuzzLoad(f *testing.F) {
	for _, s := range []string{
		"",
		"[display]\ntheme = \"dark\"\n",
		"[bus]\nreport_max = 500\nreport_full_max = -1\n",
		"[llm.local]\nurl = \"http://127.0.0.1:8080/v1\"\nmodel = \"m\"\n",
		"[llm.far]\nurl = \"http://user:pw@example.com/?k=v\"\nremote = true\n",
		"[notify]\nquiet = \"22:00-07:30\"\nkinds = [\"ask\", \"ask\"]\n",
		"unknown.token = \"x\"\n",
		"a = \n[[t]]\n",
		"x = 1\nx = 2\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		lines := strings.Count(text, "\n") + 1
		for _, file := range []*File{Load(path, "config.toml"), LoadLegacy(path, "busmon.toml")} {
			if !file.Exists {
				t.Fatal("an existing file read as missing")
			}
			for _, w := range file.Warnings {
				if w.Line < 0 || w.Line > lines {
					t.Fatalf("warning line %d outside 0..%d: %s", w.Line, lines, w)
				}
				_ = w.String()
			}
			for name := range file.values {
				if _, ok := Lookup(name); !ok {
					t.Fatalf("unknown key %q kept", name)
				}
			}
			for _, e := range file.Endpoints() {
				_ = e
			}
		}
	})
}

// FuzzParseQuiet: an accepted window is within the day, not empty, and reads
// back as the text it came from.
func FuzzParseQuiet(f *testing.F) {
	for _, s := range []string{"", "22:00-07:30", "00:00-00:00", "24:00-01:00", "7:30-8:00", "23:59-00:00", "aa:bb-cc:dd", "10:00-11:00-12:00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, ok := ParseQuiet(s)
		if !ok {
			return
		}
		if q.From < 0 || q.From >= 24*60 || q.To < 0 || q.To >= 24*60 || q.From == q.To {
			t.Fatalf("ParseQuiet(%q) = %+v", s, q)
		}
		back := fmt.Sprintf("%02d:%02d-%02d:%02d", q.From/60, q.From%60, q.To/60, q.To%60)
		if back != s {
			t.Fatalf("ParseQuiet(%q) reads back as %q", s, back)
		}
	})
}

// FuzzSplitList: joining the items gives the value back.
func FuzzSplitList(f *testing.F) {
	for _, s := range []string{"", "a", "a,b", ",", "a,,b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got := strings.Join(SplitList(s), ","); got != s {
			t.Fatalf("SplitList(%q) joins back as %q", s, got)
		}
	})
}
