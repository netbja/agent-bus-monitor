package main

import (
	"testing"
)

func TestNormalizeCompose(t *testing.T) {
	in := "\n\n  line one  \r\nline\x07 two\t\rlast \x1b[31m\n\n"
	if got := normalizeCompose(in); got != "  line one\nline two\nlast [31m" {
		t.Fatalf("got %q", got)
	}
}
