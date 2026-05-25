package main

import "testing"

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
