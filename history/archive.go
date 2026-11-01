// Package history is the project's append-only archive of its streams (spec
// docs/superpowers/specs/2026-11-01-history-design.md): one JSONL file per
// UTC day under the project's state directory, written by the dispatch
// service's chore, read by agentbus history and busmon's History page.
//
// Files only: nothing here writes to Redis. The writer reads the streams
// through a Source and holds an flock on the directory while it appends.
package history

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
)

// Streams are the archived stream kinds, in the order a pass reads them.
var Streams = []string{"cmd", "report", "status", "notify", "verdicts", "health:log", "swaps"}

// GapStream marks a line that records what was not archived: f.of is the
// stream ("*" for every one), f.from and f.to the ids around the hole.
const GapStream = "gap"

// Entry is one archived line: the entry as the stream holds it, plus where it
// came from.
type Entry struct {
	Stream string            `json:"stream"`
	ID     string            `json:"id"`
	F      map[string]string `json:"f"`
}

// Source reads the streams. After returns at most count entries newer than
// after ("" = from the oldest), oldest first; Oldest the oldest retained id
// (ok false for an empty or absent stream).
type Source interface {
	After(ctx context.Context, stream, after string, count int64) ([]Entry, error)
	Oldest(ctx context.Context, stream string) (id string, ok bool, err error)
}

// ErrLocked: another writer holds the directory's lock; the pass wrote nothing.
var ErrLocked = errors.New("history: another writer holds the archive")

const (
	cursorFile = "cursor.json"
	lockFile   = ".lock"
	dayLayout  = "2006-01-02"
	// maxLine bounds a line read back at start: a report's full text is at
	// most 8000 characters, a cmd body less.
	maxLine = 1 << 20
)

// Writer archives the streams into Dir. It keeps each stream's position in
// memory between passes and recovers it from disk on the first pass, or after
// a failed write or a deleted cursor.
type Writer struct {
	Dir    string
	Budget int   // entries a pass, 2000 when 0
	Page   int64 // entries a read, 500 when 0

	started bool
	pos     map[string]string // stream -> last archived id
	restart bool              // no cursor and no day file: write the restart line
	gaps    map[string]string // stream -> the oldest id a gap line was written for
}

// Pass appends what the streams hold beyond each position, up to the budget
// and ctx's deadline, then fsyncs and replaces the cursor. It returns the
// entries archived (gap lines not counted).
func (w *Writer) Pass(ctx context.Context, src Source, nowMS int64) (int, error) {
	if err := os.MkdirAll(w.Dir, 0o700); err != nil {
		return 0, err
	}
	unlock, err := lockDir(w.Dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if w.started {
		if _, err := os.Stat(filepath.Join(w.Dir, cursorFile)); errors.Is(err, os.ErrNotExist) {
			w.started = false // the directory, or the cursor, was deleted under us
		}
	}
	if !w.started {
		if err := w.recover(); err != nil {
			return 0, err
		}
	}
	budget, page := w.Budget, w.Page
	if budget <= 0 {
		budget = 2000
	}
	if page <= 0 {
		page = 500
	}
	out := newAppender(w.Dir)
	defer out.close()
	fail := func(err error) (int, error) {
		w.started = false // re-read the files: a failed write may leave a torn line
		return 0, err
	}

	if w.restart {
		to := fmt.Sprintf("%d-0", nowMS)
		for _, s := range Streams {
			if id, ok, err := src.Oldest(ctx, s); err != nil {
				return fail(err)
			} else if ok && idLess(id, to) {
				to = id
			}
		}
		if err := out.write(Entry{Stream: GapStream, ID: fmt.Sprintf("%d-0", nowMS),
			F: map[string]string{"of": "*", "from": "unknown", "to": to}}); err != nil {
			return fail(err)
		}
	}
	// First, a gap line for each stream trimmed past its position.
	for _, s := range Streams {
		pos := w.pos[s]
		if pos == "" || ctx.Err() != nil {
			continue
		}
		oldest, ok, err := src.Oldest(ctx, s)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return fail(err)
		}
		if ok && idLess(pos, oldest) && w.gaps[s] != oldest {
			if err := out.write(Entry{Stream: GapStream, ID: oldest,
				F: map[string]string{"of": s, "from": pos, "to": oldest}}); err != nil {
				return fail(err)
			}
			w.gaps[s] = oldest
		}
	}
	// Then a page per stream in turn, so a flood on one never starves the
	// others (reviewer-codex), until the budget is spent or every stream is
	// read to its end.
	n := 0
	open := append([]string(nil), Streams...)
	for len(open) > 0 && n < budget && ctx.Err() == nil {
		var next []string
		for _, s := range open {
			if n >= budget || ctx.Err() != nil {
				break
			}
			pos := w.pos[s]
			es, err := src.After(ctx, s, pos, min(page, int64(budget-n)))
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				return fail(err)
			}
			for _, e := range es {
				e.Stream = s
				e.F = redact(e.F)
				if err := out.write(e); err != nil {
					return fail(err)
				}
				pos = e.ID
				n++
			}
			w.pos[s] = pos
			if int64(len(es)) == page {
				next = append(next, s) // more may wait
			}
		}
		open = next
	}
	if err := out.sync(); err != nil {
		return fail(err)
	}
	w.restart = false
	if err := w.saveCursor(); err != nil {
		return n, err
	}
	return n, nil
}

// recover truncates a torn last line in every day file, then takes each
// stream's position as max(cursor, the highest id in the files).
func (w *Writer) recover() error {
	w.pos, w.gaps = map[string]string{}, map[string]string{}
	cursorOK := false
	if b, err := os.ReadFile(filepath.Join(w.Dir, cursorFile)); err == nil {
		var c map[string]string
		if json.Unmarshal(b, &c) == nil {
			cursorOK = true
			for s, id := range c {
				if validID(id) {
					w.pos[s] = id
				}
			}
		}
	}
	days, err := dayFiles(w.Dir)
	if err != nil {
		return err
	}
	for _, d := range days {
		p := filepath.Join(w.Dir, d.name)
		if err := truncateTorn(p); err != nil {
			return err
		}
		if err := scanMax(p, w.pos); err != nil {
			return err
		}
	}
	w.restart = !cursorOK && len(days) == 0
	w.started = true
	return nil
}

// truncateTorn cuts a file after its last newline: a crash in the middle of an
// append never glues the next line to a torn one.
func truncateTorn(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return err
	}
	size := st.Size()
	buf := make([]byte, 64*1024)
	for end := size; end > 0; {
		start := max(0, end-int64(len(buf)))
		chunk := buf[:end-start]
		if _, err := f.ReadAt(chunk, start); err != nil && err != io.EOF {
			return err
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			keep := start + int64(i) + 1
			if keep == size {
				return nil
			}
			return f.Truncate(keep)
		}
		end = start
	}
	return f.Truncate(0)
}

// scanMax raises pos[stream] to the highest id the file holds for it.
func scanMax(path string, pos map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	for sc.Scan() {
		var e struct{ Stream, ID string }
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Stream == GapStream || !validID(e.ID) {
			continue
		}
		if cur := pos[e.Stream]; cur == "" || idLess(cur, e.ID) {
			pos[e.Stream] = e.ID
		}
	}
	return sc.Err()
}

func (w *Writer) saveCursor() error {
	b, err := json.Marshal(w.pos)
	if err != nil {
		return err
	}
	tmp := filepath.Join(w.Dir, cursorFile+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(w.Dir, cursorFile))
}

// redact replaces each sensitive field whole (health.Redact).
func redact(f map[string]string) map[string]string {
	out := make(map[string]string, len(f))
	for k, v := range f {
		out[k] = health.Redact(v)
	}
	return out
}

// appender keeps one open file per day for a pass.
type appender struct {
	dir   string
	files map[string]*os.File
}

func newAppender(dir string) *appender { return &appender{dir: dir, files: map[string]*os.File{}} }

func (a *appender) write(e Entry) error {
	ms, _, ok := parseID(e.ID)
	if !ok {
		return fmt.Errorf("history: invalid id %q", e.ID)
	}
	day := time.UnixMilli(ms).UTC().Format(dayLayout)
	f := a.files[day]
	if f == nil {
		var err error
		f, err = os.OpenFile(filepath.Join(a.dir, day+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		a.files[day] = f
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func (a *appender) sync() error {
	for _, f := range a.files {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	return nil
}

func (a *appender) close() {
	for _, f := range a.files {
		f.Close()
	}
}

// lockDir takes the directory's flock without waiting.
func lockDir(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

type dayFile struct {
	name string
	day  time.Time
	size int64
}

// dayFiles lists YYYY-MM-DD.jsonl, oldest first.
func dayFiles(dir string) ([]dayFile, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []dayFile
	for _, e := range ents {
		name := e.Name()
		d, err := time.Parse(dayLayout, strings.TrimSuffix(name, ".jsonl"))
		if err != nil || !strings.HasSuffix(name, ".jsonl") || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, dayFile{name: name, day: d, size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].day.Before(out[j].day) })
	return out, nil
}

// Prune deletes day files older than keepDays, then the oldest until the
// directory's day files fit maxBytes (0 = no cap), under the writer's lock
// (ErrLocked when it is held). Today's file is never deleted.
func Prune(dir string, keepDays int, maxBytes int64, now time.Time) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil // no archive: nothing to prune, nothing created
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return err // ErrLocked: a writer appends; the next day's prune runs
	}
	defer unlock()
	days, err := dayFiles(dir)
	if err != nil {
		return err
	}
	today := now.UTC().Format(dayLayout)
	cutoff := now.UTC().AddDate(0, 0, -keepDays).Format(dayLayout)
	var total int64
	var kept []dayFile
	for _, d := range days {
		name := d.day.Format(dayLayout)
		if keepDays > 0 && name <= cutoff { // never today: cutoff is before it
			if err := os.Remove(filepath.Join(dir, d.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		total += d.size
		kept = append(kept, d)
	}
	for _, d := range kept {
		if maxBytes <= 0 || total <= maxBytes {
			break
		}
		if d.day.Format(dayLayout) == today {
			continue
		}
		if err := os.Remove(filepath.Join(dir, d.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		total -= d.size
	}
	return nil
}

// parseID splits a stream id "<ms>-<seq>".
func parseID(id string) (ms, seq int64, ok bool) {
	a, b, found := strings.Cut(id, "-")
	if !found {
		return 0, 0, false
	}
	m, err1 := strconv.ParseInt(a, 10, 64)
	s, err2 := strconv.ParseInt(b, 10, 64)
	return m, s, err1 == nil && err2 == nil && m >= 0 && s >= 0
}

func validID(id string) bool { _, _, ok := parseID(id); return ok }

// idLess orders two valid stream ids.
func idLess(a, b string) bool {
	am, as, _ := parseID(a)
	bm, bs, _ := parseID(b)
	return am < bm || (am == bm && as < bs)
}

// Dir is the archive's directory in a project's state directory
// (service.Paths.StateDir), shared by the writer and its readers.
func Dir(stateDir string) string { return filepath.Join(stateDir, "history") }
