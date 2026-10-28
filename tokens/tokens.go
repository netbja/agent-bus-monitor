// Package tokens counts the tokens agents spend, per local day, from the
// clients' own logs (spec docs/superpowers/specs/2026-10-28-tokens-dashboard-design.md):
// Claude Code transcripts and Codex rollouts. It reads files only, never a
// credential, keeps its offsets in memory (nothing persisted: a restart
// rescans, bounded per pass) and computes no cost, by rule.
package tokens

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

// Usage is one day's tokens: In (input, uncached), Write (cache creation),
// Read (cache read), Out (output), N (requests).
type Usage struct {
	In, Write, Read, Out, N int64
}

// Add adds o to u.
func (u *Usage) Add(o Usage) {
	u.In, u.Write, u.Read, u.Out, u.N = u.In+o.In, u.Write+o.Write, u.Read+o.Read, u.Out+o.Out, u.N+o.N
}

// DaySums are the usage per local day, "2006-01-02".
type DaySums map[string]Usage

// Budget bounds one pass: files opened and bytes read.
type Budget struct {
	Files int
	Bytes int64
}

// sessionIDPattern is a session id that can be joined into a path: no
// separator, no dot, no glob.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{7,63}$`)

// ValidSessionID reports an id SessionFiles accepts.
func ValidSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

// SessionFiles are a Claude Code session's transcript and its subagents'
// transcripts, under any project directory of root (~/.claude/projects).
func SessionFiles(root, id string) ([]string, error) {
	if !ValidSessionID(id) {
		return nil, errors.New("invalid session id")
	}
	var out []string
	for _, pat := range []string{filepath.Join(root, "*", id+".jsonl"), filepath.Join(root, "*", id, "subagents", "*.jsonl")} {
		m, err := filepath.Glob(pat)
		if err != nil {
			return nil, err
		}
		out = append(out, m...)
	}
	return out, nil
}

// fileState is what a pass keeps of one file between passes.
type fileState struct {
	ino    uint64
	offset int64
	days   DaySums
}

// upToDate reports a file read to its end and unchanged since.
func (st *fileState) upToDate(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	return ok && sys.Ino == st.ino && info.Size() == st.offset
}

// readAppended calls line for each complete line appended to path since st's
// offset, within room bytes, and moves the offset past the last complete
// line. A file that shrank or changed identity is reset (reset is called) and
// read from 0. It reports whether the file is read to its end.
func readAppended(path string, st *fileState, room int64, reset func(), line func([]byte)) (done bool, read int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return true, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return true, 0, err
	}
	var ino uint64
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		ino = sys.Ino
	}
	if info.Size() < st.offset || ino != st.ino {
		st.ino, st.offset, st.days = ino, 0, DaySums{}
		reset()
	}
	want := min(info.Size()-st.offset, room)
	if want <= 0 {
		return st.offset >= info.Size(), 0, nil
	}
	buf := make([]byte, want)
	n, err := f.ReadAt(buf, st.offset)
	if err != nil && err != io.EOF {
		return false, 0, err
	}
	buf = buf[:n]
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return false, int64(n), nil // half a line: the next pass
	}
	sc := bufio.NewScanner(bytes.NewReader(buf[:end+1]))
	sc.Buffer(make([]byte, 0, 64<<10), len(buf))
	for sc.Scan() {
		line(sc.Bytes())
	}
	st.offset += int64(end + 1)
	return st.offset >= info.Size(), int64(n), nil
}

// day is the local date of an RFC 3339 timestamp, "" when it does not parse.
func day(ts string, loc *time.Location) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	return t.In(loc).Format("2006-01-02")
}
