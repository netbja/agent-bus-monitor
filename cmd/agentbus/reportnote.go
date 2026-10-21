package main

import (
	"strings"
	"unicode/utf8"
)

// reportFlatMax is the longest report that may be one block of text before
// report suggests a structure.
const reportFlatMax = 300

// reportShapeNote is the stderr note for a long report written as one
// paragraph: busmon renders reports as Markdown, and a flat block reads as a
// wall of text (busmon UX item 7). The report is sent either way.
func reportShapeNote(msg string) string {
	if strings.Contains(msg, "\n") || utf8.RuneCountInString(msg) <= reportFlatMax {
		return ""
	}
	return "note: busmon renders Markdown: a summary line, then ## Done and ## What remains for you, with - items (sent as written)"
}
