package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// A service's log page that reads (services-restart-buttons §4): each line on
// its own row with a level, the dispatcher's JSON decisions in a fixed field
// order with their time, the routine "none" decisions folded until a, and f
// filtering by a word. The page reads back until it holds svcLogVisible lines
// a person reads (the routine ones folded), within svcLogMaxBytes.

const (
	svcLogVisible  = 200
	svcLogMaxBytes = 8 << 20 // the read-back's cap, across the log and its rotated file
	svcLogChunk    = 64 << 10
)

// svcLogLine is one parsed line.
type svcLogLine struct {
	raw      string
	decision bool // a dispatcher's JSON line (a decision or an error)
	ts       time.Time
	fields   string // the decision's fields in order, or the error's
	role     Role
	routine  bool // a "none" decision: folded until a
}

// svcLogJSON is what the page reads of a dispatcher's line; the raw file
// keeps the rest (high_id, latency_ms).
type svcLogJSON struct {
	Action string `json:"action"`
	Agent  string `json:"agent"`
	Reason string `json:"reason"`
	Rule   *int   `json:"rule"`
	Op     string `json:"op"`
	Error  string `json:"error"`
	TS     string `json:"ts"`
}

var svcLogErrorWord = regexp.MustCompile(`(?i)\b(failed|error|panic)\b`)

// parseSvcLogLine levels a line: a decision by its action (escalate, blocked,
// unblocked: attention; none: muted and routine; the others info), a
// dispatcher's error line an error, a text line an error when it says
// failed, error or panic, else info.
func parseSvcLogLine(raw string) svcLogLine {
	l := svcLogLine{raw: raw, role: RoleText}
	var j svcLogJSON
	if strings.HasPrefix(raw, "{") && json.Unmarshal([]byte(raw), &j) == nil && (j.Action != "" || j.Error != "") {
		l.decision = true
		if t, err := time.Parse(time.RFC3339, j.TS); err == nil {
			l.ts = t
		}
		if j.Error != "" {
			l.role = RoleError
			l.fields = strings.TrimSpace(j.Agent + "  " + j.Op + ": " + j.Error)
			return l
		}
		parts := []string{j.Action, j.Agent, j.Reason}
		if j.Rule != nil {
			parts = append(parts, fmt.Sprintf("(rule %d)", *j.Rule))
		}
		var kept []string
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		l.fields = strings.Join(kept, "  ")
		switch j.Action {
		case "escalate", "blocked", "unblocked":
			l.role = RoleAttention
		case "none":
			l.role, l.routine = RoleMuted, true
		}
		return l
	}
	if svcLogErrorWord.MatchString(raw) {
		l.role = RoleError
	}
	return l
}

// svcLogRow is a line as drawn: a decision's local time (as the text lines
// write theirs) then its fields; a text line unchanged.
func svcLogRow(l svcLogLine, width int) Line {
	text := l.raw
	if l.decision {
		text = l.fields
		if !l.ts.IsZero() {
			text = l.ts.Local().Format("2006-01-02 15:04:05") + "  " + text
		}
	}
	return Line{{Text: clip(Preview(text), width), Role: l.role}}
}

// readSvcLog reads the log back, its rotated file (.1) after it, until want
// lines that are not routine are held or maxBytes were read; it returns the
// lines from the earliest of those on, in file order.
func readSvcLog(path string, want int, maxBytes int64) ([]svcLogLine, error) {
	files := []string{path, path + ".1"} // newest first
	sizes := make([]int64, len(files))
	found := false
	for i, f := range files {
		if st, err := os.Stat(f); err == nil {
			sizes[i], found = st.Size(), true
		}
	}
	if !found {
		return nil, fmt.Errorf("no log at %s", path)
	}
	var lines []svcLogLine
	for n := int64(svcLogChunk); ; n *= 4 {
		n = min(n, maxBytes)
		var data []byte
		whole, left := true, n
		for i, f := range files { // the newest bytes first, then the rotated file's
			if left <= 0 || sizes[i] == 0 {
				whole = whole && sizes[i] == 0
				continue
			}
			b, all, err := tailBytes(f, left)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			data = append(b, data...)
			left -= int64(len(b))
			whole = whole && all
			if !all {
				break
			}
		}
		text := string(data)
		if !whole { // the read starts inside a line: drop it
			if i := strings.IndexByte(text, '\n'); i >= 0 {
				text = text[i+1:]
			}
		}
		lines = lines[:0]
		for _, raw := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			if raw != "" {
				lines = append(lines, parseSvcLogLine(raw))
			}
		}
		if whole || n >= maxBytes || countVisible(lines) >= want {
			break
		}
	}
	// Keep from the want-th visible line from the end.
	seen := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if !lines[i].routine {
			if seen++; seen == want {
				return lines[i:], nil
			}
		}
	}
	return lines, nil
}

func countVisible(lines []svcLogLine) int {
	n := 0
	for _, l := range lines {
		if !l.routine {
			n++
		}
	}
	return n
}

// tailBytes is the last n bytes of a file; all says it is the whole file.
func tailBytes(path string, n int64) (b []byte, all bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	off := max(0, st.Size()-n)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, false, err
	}
	b, err = io.ReadAll(f)
	return b, off == 0, err
}

// svcLogView is what the page draws from: the read lines, kept so a and f
// redraw without reading again.
type svcLogView struct {
	kind  string
	head  []Line // the log's path, or why it is not readable here
	lines []svcLogLine
	crash []Line
	all   bool   // a: the routine lines shown
	word  string // f: only the lines holding it (case-insensitive)
}

// svcLogPage builds the page's title and body.
func svcLogPage(v svcLogView, width int) (string, []Line) {
	body := append([]Line{}, v.head...)
	shown, hidden := 0, 0
	word := strings.ToLower(v.word)
	for _, l := range v.lines {
		if l.routine && !v.all {
			hidden++
			continue
		}
		row := svcLogRow(l, width)
		if word != "" && !strings.Contains(strings.ToLower(row.text()), word) {
			continue
		}
		body = append(body, row)
		shown++
	}
	body = append(body, v.crash...)
	title := fmt.Sprintf("%s log %s %d lines", v.kind, GlyphNone, shown)
	if hidden > 0 {
		title += fmt.Sprintf(" %s %d routine hidden (a)", GlyphNone, hidden)
	}
	if v.word != "" {
		title += fmt.Sprintf(" %s filter: %s", GlyphNone, Preview(v.word))
	}
	return title, body
}
