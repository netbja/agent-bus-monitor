package main

import (
	"strings"
	"testing"
)

// stranger-test: busmon with no project says how to name one and how to find one.
func TestNoProjectSaysHowToFindOne(t *testing.T) {
	msg := noProjectText()
	for _, want := range []string{"--project <p>", "AGENT_BUS_PROJECT", "busmon --list", "busmon --pick"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, " -project") {
		t.Errorf("message names a single-dash flag:\n%s", msg)
	}
	for _, f := range []string{"--project", "--list", "--pick"} { // every flag it names exists
		found := false
		for _, b := range busmonFlags {
			if "--"+b.Long == f {
				found = true
			}
		}
		if !found {
			t.Errorf("message names %s, which busmon does not have", f)
		}
	}
}
