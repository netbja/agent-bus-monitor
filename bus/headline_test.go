package bus

import "testing"

// report-one-row §4: a report's headline is its first sentence of substance,
// never a Markdown heading, a fence, a table or a list marker.
func TestReportHeadline(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"heading then list", "## Done\n- Vérif: all green\n- next", "Vérif: all green"},
		{"heading then sentence", "## Done\n\nmerge-queue 958b636 approved. Next the docs.", "merge-queue 958b636 approved."},
		{"heading alone", "## Done", "Done"},
		{"heading alone, trailing hashes kept as text", "###   Spaced out  ", "Spaced out"},
		{"no heading", "plain line", "plain line"},
		{"blank lines first", "\n\n  \nfirst real line\nsecond", "first real line"},
		{"numbered list", "# Status\n1. Built the thing. Tested it.", "Built the thing."},
		{"star and plus lists", "* starred\n", "starred"},
		{"plus list", "+ plussed", "plussed"},
		{"abbreviation is no sentence end", "Use e.g. x for that. Then y.", "Use e.g. x for that."},
		{"exclamation and question", "Ready! 3 left", "Ready!"},
		{"question then digit", "Merged? 2 PRs remain", "Merged?"},
		{"fence skipped", "## Done\n```go\nx := 1\n```\nThe fix is in.", "The fix is in."},
		{"tilde fence skipped", "~~~\ncode. Here\n~~~\nAfter the fence.", "After the fence."},
		{"table skipped", "| a | b |\n|---|---|\nBelow the table", "Below the table"},
		{"only headings and fences", "# Title\n## Sub\n```\nx\n```", "Title"},
		{"empty", "", ""},
		{"only blanks and a table", "\n| a |\n", ""},
		{"hash without space is text", "#123 merged", "#123 merged"},
		{"heading after text not used", "text first\n## later", "text first"},
	}
	for _, c := range cases {
		if got := ReportHeadline(c.in); got != c.want {
			t.Errorf("%s: ReportHeadline(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
