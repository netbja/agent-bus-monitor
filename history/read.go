package history

import (
	"bufio"
	"bytes"
	"encoding/json"
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
func ReadDays(dir string, from, to time.Time, maxBytes int64) (es []Entry, cut bool, err error) {
	days, err := dayFiles(dir)
	if err != nil {
		return nil, false, err
	}
	lo, hi := from.UTC().Format(dayLayout), to.UTC().Format(dayLayout)
	var chunks [][]Entry
	left := maxBytes
	for i := len(days) - 1; i >= 0; i-- {
		name := days[i].day.Format(dayLayout)
		if name < lo || name > hi {
			continue
		}
		offset := int64(0)
		if maxBytes > 0 {
			if left <= 0 {
				cut = true
				break
			}
			if days[i].size > left {
				offset, cut = days[i].size-left, true
			}
			left -= days[i].size - offset
		}
		part, err := readFile(filepath.Join(dir, days[i].name), offset)
		if err != nil {
			return nil, false, err
		}
		chunks = append(chunks, part)
		if cut {
			break
		}
	}
	for i := len(chunks) - 1; i >= 0; i-- {
		es = append(es, chunks[i]...)
	}
	return es, cut, nil
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
	at := At(e)
	if !f.Since.IsZero() && at.Before(f.Since) || !f.Until.IsZero() && at.After(f.Until) {
		return false
	}
	if e.Stream == GapStream {
		return f.Thread == "" && f.Grep == "" && (f.Kind == "" || gapCovers(e.F["of"], f.Kind))
	}
	if f.Agent != "" && e.F["from"] != f.Agent && e.F["target"] != f.Agent && e.F["agent"] != f.Agent &&
		e.F["author"] != f.Agent && e.F["reviewer"] != f.Agent {
		return false
	}
	if f.Thread != "" && e.ID != f.Thread && e.F["ref"] != f.Thread {
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
