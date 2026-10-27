package main

import (
	"strings"
	"testing"
)

func TestHelpExplainsComposeTab(t *testing.T) {
	if !strings.Contains(plainOf(helpText()), "a text that is only @prefix becomes the recipient") {
		t.Fatal("help does not explain Tab in the compose window")
	}
}

// The to: field holds only a recipient, so it completes a bare prefix, as it
// did before the two completions were merged; the line still needs the "@"
// (there plain text is a message). An empty field opens no list, so Tab
// there still moves to the text.
func TestToFieldCompletesABarePrefix(t *testing.T) {
	names := []string{"master", "coder"}
	if got := strings.Join(toCompletions("ma", names), "|"); got != "@master " {
		t.Fatalf("ma = %q", got)
	}
	if got := toCompletions("", names); got != nil {
		t.Fatalf("empty = %q", got)
	}
	if got := strings.Join(toCompletions("@", names), "|"); got != "@all |@coder |@master " {
		t.Fatalf("@ = %q", got)
	}
	if got := strings.Join(toCompletions("@co", names), "|"); got != "@coder " {
		t.Fatalf("@co = %q", got)
	}
	if recipientCompletions("ma", names) != nil {
		t.Fatal("the line completes a bare word")
	}
}
