package main

import (
	"testing"
	"time"
)

func TestExtractFlag(t *testing.T) {
	rest, v := extractFlag([]string{"a", "--ref", "C1", "b"}, "--ref")
	if v != "C1" {
		t.Fatalf("value = %q, want C1", v)
	}
	if len(rest) != 2 || rest[0] != "a" || rest[1] != "b" {
		t.Fatalf("rest = %v, want [a b]", rest)
	}
	if _, v := extractFlag([]string{"a", "b"}, "--ref"); v != "" {
		t.Fatalf("absent flag value = %q, want \"\"", v)
	}
	// a flag with no following value is treated as absent (not consumed)
	if rest, v := extractFlag([]string{"a", "--ref"}, "--ref"); v != "" || len(rest) != 2 {
		t.Fatalf("dangling flag = (%v, %q), want ([a --ref], \"\")", rest, v)
	}
}

func TestExtractBool(t *testing.T) {
	rest, ok := extractBool([]string{"a", "--auto", "b"}, "--auto")
	if !ok || len(rest) != 2 || rest[0] != "a" || rest[1] != "b" {
		t.Fatalf("got (%v, %v), want ([a b], true)", rest, ok)
	}
	if _, ok := extractBool([]string{"a", "b"}, "--auto"); ok {
		t.Fatal("absent bool flag reported present")
	}
}

func TestGenRefUnique(t *testing.T) {
	if a, b := genRef(), genRef(); a == b || a == "" {
		t.Fatalf("genRef not unique/non-empty: %q %q", a, b)
	}
}

func TestParseIdle(t *testing.T) {
	def := 240 * time.Second
	if got := parseIdle("", def); got != def {
		t.Fatalf("empty = %v, want default %v", got, def)
	}
	if got := parseIdle("3600", def); got != 3600*time.Second {
		t.Fatalf("\"3600\" = %v, want 3600s", got)
	}
	// non-positive and non-numeric fall back to the default, never zero/negative
	// (a zero idle window would make the watcher exit immediately, busy-looping).
	if got := parseIdle("0", def); got != def {
		t.Fatalf("\"0\" = %v, want default", got)
	}
	if got := parseIdle("nope", def); got != def {
		t.Fatalf("\"nope\" = %v, want default", got)
	}
}
