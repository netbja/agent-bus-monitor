package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReadDays reads the day files whose UTC day is within [from, to], oldest
// first. maxBytes (0 = none) bounds what is read, newest day first: a day
// that does not fit is read from its end, and cut reports it. A missing
// directory is an empty archive.
func ReadDays(dir string, from, to time.Time, maxBytes int64) ([]Entry, bool, error) {
	es, _, cut, err := ReadBack(dir, from, Cursor{Day: to.UTC().Format(dayLayout), Offset: -1}, maxBytes)
	return es, cut, err
}

// Cursor is a position in the archive: a day file and a byte offset in it.
// Offset -1 stands for the file's end.
type Cursor struct {
	Day    string // YYYY-MM-DD
	Offset int64
}

// ReadBack reads what lies before at, back to from's day, newest first under
// maxBytes (0 = none), and returns it oldest first with the cursor of what it
// did not read: the first whole line it read. Reading again from that cursor
// shows the lines before it, so every line is shown once. A day file is not
// in id order (a pass writes one stream's page after another), so a reader
// pages by position, never by id (foureyes).
func ReadBack(dir string, from time.Time, at Cursor, maxBytes int64) (es []Entry, next Cursor, cut bool, err error) {
	days, err := dayFiles(dir)
	if err != nil {
		return nil, at, false, err
	}
	lo := from.UTC().Format(dayLayout)
	next = at
	var chunks [][]Entry
	left := maxBytes
	for i := len(days) - 1; i >= 0; i-- {
		name := days[i].day.Format(dayLayout)
		if name < lo || name > at.Day {
			continue
		}
		end := days[i].size
		if name == at.Day && at.Offset >= 0 {
			end = min(at.Offset, end)
		}
		if maxBytes > 0 && left <= 0 {
			cut = true
			break
		}
		start := int64(0)
		if maxBytes > 0 && end > left {
			start, cut = end-left, true
		}
		part, first, err := readSpan(filepath.Join(dir, days[i].name), start, end)
		if err != nil {
			return nil, at, false, err
		}
		chunks = append(chunks, part)
		left -= end - first
		next = Cursor{Day: name, Offset: first}
		if cut {
			break
		}
	}
	for i := len(chunks) - 1; i >= 0; i-- {
		es = append(es, chunks[i]...)
	}
	return es, next, cut, nil
}

// readSpan reads the whole lines of a day file between start and end; a
// start inside a line skips that line. first is where the first whole line
// read begins (end when none).
func readSpan(path string, start, end int64) ([]Entry, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, end, err
	}
	defer f.Close()
	first := start
	if start > 0 {
		// Back one byte: if it is a newline, start begins a whole line.
		if _, err := f.Seek(start-1, io.SeekStart); err != nil {
			return nil, end, err
		}
		r := bufio.NewReader(io.LimitReader(f, end-start+1))
		skipped, err := r.ReadBytes('\n')
		if err != nil {
			return nil, end, nil // no whole line in the span
		}
		first = start - 1 + int64(len(skipped))
	}
	if first >= end {
		return nil, end, nil
	}
	if _, err := f.Seek(first, io.SeekStart); err != nil {
		return nil, end, err
	}
	var out []Entry
	sc := bufio.NewScanner(io.LimitReader(f, end-first))
	sc.Buffer(make([]byte, 64*1024), maxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		var e Entry
		if len(line) == 0 || json.Unmarshal(line, &e) != nil || !validID(e.ID) {
			continue
		}
		out = append(out, e)
	}
	return out, first, sc.Err()
}

// readFile reads a day file from offset; a read that starts inside a line
// skips that line. Unreadable lines are skipped.
func readFile(path string, offset int64) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if offset > 0 {
		// Back one byte: if it is a newline, the offset starts a whole line.
		if _, err := f.Seek(offset-1, io.SeekStart); err != nil {
			return nil, err
		}
	}
	r := bufio.NewReaderSize(f, 64*1024)
	if offset > 0 {
		if _, err := r.ReadSlice('\n'); err != nil && err != bufio.ErrBufferFull {
			return nil, nil // no whole line after the offset
		}
	}
	var out []Entry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		var e Entry
		if len(line) == 0 || json.Unmarshal(line, &e) != nil || !validID(e.ID) {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// ReadGaps reads every gap line kept in gaps.jsonl: a reader adds them to the
// days it read, so a hole is said whatever the query's days (reviewer-codex).
func ReadGaps(dir string) ([]Entry, error) {
	es, err := readFile(filepath.Join(dir, gapsFile), 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return es, err
}

// key identifies an entry across sources: stream ids are unique within a
// stream only, and a gap line by the stream it covers.
func key(e Entry) string {
	if e.Stream == GapStream {
		return e.Stream + "|" + e.F["of"] + "|" + e.ID
	}
	return e.Stream + "|" + e.ID
}

// Merge joins lists (the archive, then the live streams), each entry once by
// (stream, id), ordered by id.
func Merge(lists ...[]Entry) []Entry {
	seen := map[string]bool{}
	var out []Entry
	for _, l := range lists {
		for _, e := range l {
			if k := key(e); !seen[k] {
				seen[k] = true
				out = append(out, e)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out
}

// Kind is what an entry is: a cmd's type (directive, reply, verdict, ask,
// escalation, ...), or its stream's kind: report, status, notify, verdict,
// health, swap; gap for a gap line.
func Kind(e Entry) string {
	switch e.Stream {
	case "cmd":
		if e.F["type"] == "verdict" {
			return "verdict-notice" // the ledger's entry is the verdict
		}
		return e.F["type"]
	case "verdicts":
		return "verdict"
	case "health:log":
		return "health"
	case "swaps":
		return "swap"
	}
	return e.Stream
}

// WhoText is who an entry is from (and to), and its text, as listen and the
// readers print it.
func WhoText(e Entry) (who, text string) {
	f := e.F
	switch e.Stream {
	case "cmd":
		return f["from"] + " → " + f["target"], f["command"]
	case "report":
		return f["agent"], f["message"]
	case "status":
		return f["agent"], f["state"] + ": " + f["message"]
	case "notify":
		return f["from"], f["message"]
	case "verdicts":
		return f["reviewer"] + " → " + f["author"], f["decision"] + " " + f["subject"] + ": " + f["message"]
	case "health:log":
		return f["check"], f["previous"] + " → " + f["status"]
	case "swaps":
		return f["role"], f["event"]
	}
	return e.Stream, ""
}

// At is the entry's time, from its id.
func At(e Entry) time.Time {
	ms, _, _ := parseID(e.ID)
	return time.UnixMilli(ms)
}

// Filter selects entries; a zero field does not filter.
type Filter struct {
	Agent  string // from, target or agent (a verdict's author or reviewer)
	Thread string // the entry whose id or ref is Thread
	Kind   string
	Since  time.Time
	Until  time.Time
	Grep   string // case-insensitive, in the text fields (not the names)
}

// Match reports whether e passes f. A gap line passes the time bounds, and
// passes an agent or kind filter when it may hide such entries; never a
// thread or a text search.
func (f Filter) Match(e Entry) bool {
	if e.Stream == GapStream {
		// The stretch it covers, not when it was written, meets the bounds.
		from, to := gapStretch(e)
		if !f.Since.IsZero() && to.Before(f.Since) || !f.Until.IsZero() && from.After(f.Until) {
			return false
		}
		return f.Thread == "" && f.Grep == "" && (f.Kind == "" || gapCovers(e.F["of"], f.Kind))
	}
	at := At(e)
	if !f.Since.IsZero() && at.Before(f.Since) || !f.Until.IsZero() && at.After(f.Until) {
		return false
	}
	if f.Agent != "" && e.F["from"] != f.Agent && e.F["target"] != f.Agent && e.F["agent"] != f.Agent &&
		e.F["author"] != f.Agent && e.F["reviewer"] != f.Agent {
		return false
	}
	if f.Thread != "" && !inThread(e, f.Thread) {
		return false
	}
	if f.Kind != "" && Kind(e) != f.Kind {
		return false
	}
	if f.Grep != "" {
		needle, found := strings.ToLower(f.Grep), false
		for k, v := range e.F {
			if nameFields[k] {
				continue
			}
			if strings.Contains(strings.ToLower(v), needle) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// inThread: the entry's id or ref is t; a bare millisecond cursor (no "-")
// stands for every id of that millisecond, as agentbus thread reads it.
func inThread(e Entry, t string) bool {
	if e.ID == t || e.F["ref"] == t {
		return true
	}
	if strings.Contains(t, "-") {
		return false
	}
	return strings.HasPrefix(e.ID, t+"-") || strings.HasPrefix(e.F["ref"], t+"-")
}

// gapStretch is what a gap covers: from its from id (the beginning of time
// when unknown) to its to id.
func gapStretch(e Entry) (from, to time.Time) {
	to = At(Entry{ID: e.F["to"]})
	if validID(e.F["from"]) {
		from = At(Entry{ID: e.F["from"]})
	}
	if !validID(e.F["to"]) {
		to = At(e)
	}
	return from, to
}

// nameFields are who and what an entry is about, not its text: --grep skips
// them.
var nameFields = map[string]bool{"from": true, "target": true, "agent": true, "author": true, "reviewer": true,
	"role": true, "ref": true, "type": true, "kind": true, "human": true, "wake": true, "for": true}

// gapCovers: could a gap in stream of hide entries of kind?
func gapCovers(of, kind string) bool {
	if of == "*" {
		return true
	}
	switch kind {
	case "report", "status", "notify":
		return of == kind
	case "health":
		return of == "health:log"
	case "swap":
		return of == "swaps"
	case "verdict":
		return of == "verdicts"
	}
	return of == "cmd"
}
