package bus

// Fuzz targets (plain `go test` runs only the seeds below). Fuzz one with
//
//	go test ./bus -run '^$' -fuzz='^FuzzGateMetaRoundTrip$' -fuzztime=20s -fuzzminimizetime=0
//
// -fuzzminimizetime=0: the default 60 s minimization of each new interesting
// input stalls the run at 0 execs/s for most of a short -fuzztime.

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// FuzzGateMetaRoundTrip: Format(Parse(m)) == m for every meta holding the
// separator, and Parse(Format(c, s, t)) gives back (c, s, t) for a challenger
// without "|" and a valid subject.
func FuzzGateMetaRoundTrip(f *testing.F) {
	f.Add("master", "pr:12", "please review")
	f.Add("coder", "", "[subject x] looks like a tag")
	f.Add("a", "", "")
	f.Add("", "mission:c/m@abc", "]] [subject ")
	f.Add("x|y", "s", "t")
	f.Fuzz(func(t *testing.T, challenger, subject, text string) {
		meta := FormatGateMeta(challenger, subject, text)
		g := ParseGateMeta(meta)
		if strings.Contains(meta, "|") {
			if back := FormatGateMeta(g.Challenger, g.Subject, g.Text); back != meta {
				t.Fatalf("Format(Parse(%q)) = %q", meta, back)
			}
		}
		if g.Subject != "" && ValidGateSubject(g.Subject) != nil {
			t.Fatalf("parsed an invalid subject %q from %q", g.Subject, meta)
		}
		if strings.Contains(challenger, "|") || subject != "" && ValidGateSubject(subject) != nil {
			return
		}
		want := GateMeta{Challenger: challenger, Subject: subject, Text: text}
		if subject == "" {
			// The old form: a text that itself starts with a valid tag reads
			// as one (documented); otherwise the whole text is kept.
			if g.Subject != "" {
				if FormatGateMeta(g.Challenger, g.Subject, g.Text) != meta {
					t.Fatalf("tagged text did not round trip: %q", meta)
				}
				return
			}
		}
		if g != want {
			t.Fatalf("Parse(Format(%q, %q, %q)) = %#v", challenger, subject, text, g)
		}
	})
}

// FuzzParseGateMeta: arbitrary stored metas never panic and re-format exactly.
func FuzzParseGateMeta(f *testing.F) {
	for _, s := range []string{"", "|", "master|text", "m|[subject pr:1] t", "m|[subject ] t", "m|[subject a]b", "no separator"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, meta string) {
		g := ParseGateMeta(meta)
		if strings.Contains(meta, "|") && FormatGateMeta(g.Challenger, g.Subject, g.Text) != meta {
			t.Fatalf("Format(Parse(%q)) differs", meta)
		}
	})
}

// FuzzSanitizeReportMessage: one line, no control rune, no doubled or edge
// space, bounded; idempotent when not cut.
func FuzzSanitizeReportMessage(f *testing.F) {
	for _, s := range []string{"", "hello", "a\nb\tc\r\n", "  lead  trail  ", "\x00\x1b[31mred\x1b[0m", strings.Repeat("é ", 400), "\xff\xfe"} {
		f.Add(s)
	}
	max := reportMaxLen()
	f.Fuzz(func(t *testing.T, s string) {
		out := SanitizeReportMessage(s)
		n := utf8.RuneCountInString(out)
		if n > max+1 {
			t.Fatalf("%d runes > cap %d+1", n, max)
		}
		for _, r := range out {
			if unicode.IsControl(r) {
				t.Fatalf("control rune %U in %q", r, out)
			}
		}
		if out != strings.TrimSpace(out) {
			t.Fatalf("edge space in %q", out)
		}
		if strings.Join(strings.Fields(out), " ") != out {
			t.Fatalf("whitespace not collapsed in %q", out)
		}
		if !strings.HasSuffix(out, "…") || n <= max {
			if again := SanitizeReportMessage(out); again != out {
				t.Fatalf("not idempotent: %q -> %q", out, again)
			}
		}
	})
}

// FuzzSanitizeReportFull: only newline and tab survive among control runes,
// the rune count is kept when not cut, bounded, idempotent when not cut.
func FuzzSanitizeReportFull(f *testing.F) {
	for _, s := range []string{"", "line1\nline2\tx", "\r\n\x00\x7f", strings.Repeat("ab\n", 3000), "\xff"} {
		f.Add(s)
	}
	max := reportFullMax()
	f.Fuzz(func(t *testing.T, s string) {
		out := SanitizeReportFull(s)
		n := utf8.RuneCountInString(out)
		if n > max+1 {
			t.Fatalf("%d runes > cap %d+1", n, max)
		}
		for _, r := range out {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				t.Fatalf("control rune %U kept", r)
			}
		}
		in := utf8.RuneCountInString(s)
		if in <= max {
			if n != in {
				t.Fatalf("uncut text changed length: %d -> %d", in, n)
			}
			if again := SanitizeReportFull(out); again != out {
				t.Fatalf("not idempotent: %q -> %q", out, again)
			}
		}
	})
}
