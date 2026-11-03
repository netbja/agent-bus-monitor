package notify

import (
	"strings"
	"testing"
)

// full detail sends an agent's text scrubbed, then cut (spec "The message"):
// a credential-like field whole, every URL, absolute or ~/ path and email.
func TestScrub(t *testing.T) {
	for in, want := range map[string]string{
		"merge PR 281?":                        "merge PR 281?",
		"see https://x.example/a for it":       "see [url] for it",
		"open www.example.org now":             "open [url] now",
		"backups in /home/u/backups failed":    "backups in [path] failed",
		"read ~/x/y first":                     "read [path] first",
		"mail a@b.example please":              "mail [email] please",
		"ratio 1/2 stays":                      "ratio 1/2 stays",
		`folder "/home/u/backups" missing`:     `folder "[path]" missing`,
		"(/home/u/x/y)":                        "([path])",
		"dir=/home/u/x":                        "dir=[path]",
		"see '~/x/y'":                          "see '[path]'",
		"a,/etc/passwd":                        "a,[path]",
		"and/or a/b/c":                         "and/or a/b/c",
		"the root / alone":                     "the root / alone",
		"ftp://h/x and /etc/passwd":            "[url] and [path]",
		"token=ghp_abcdefghijklmnopqrstuvwxyz0": "[redacted]",
	} {
		if got := Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetailText(t *testing.T) {
	long := strings.Repeat("é", 200)
	got := DetailText(long)
	if n := len([]rune(got)); n != 120 || !strings.HasSuffix(got, "…") {
		t.Fatalf("%d runes: %q", n, got)
	}
	if got := DetailText("a\nb\tc"); got != "a b c" {
		t.Fatalf("one line: %q", got)
	}
}
