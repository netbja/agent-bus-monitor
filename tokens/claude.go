package tokens

import (
	"encoding/json"
	"time"
)

// ClaudeScan counts Claude Code transcripts incrementally. One request's
// usage repeats on every line of its content blocks, identical and
// consecutive (measured: 4,099 such lines, none differing, none apart), so a
// request is counted once by remembering the last (message.id, requestId)
// per file, kept across passes like the offset.
type ClaudeScan struct {
	used  Budget
	loc   *time.Location
	files map[string]*claudeFile
}

type claudeFile struct {
	fileState
	lastKey string // the request open at the offset
}

// NewClaudeScan buckets days in loc.
func NewClaudeScan(loc *time.Location) *ClaudeScan {
	return &ClaudeScan{loc: loc, files: map[string]*claudeFile{}}
}

type claudeLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID    string `json:"id"`
		Usage *struct {
			Input  int64 `json:"input_tokens"`
			Create int64 `json:"cache_creation_input_tokens"`
			Read   int64 `json:"cache_read_input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Pass reads what was appended to paths since the last pass, in the order
// given (newest first, by the caller), within budget. It reports partial when
// a file is not read to its end.
func (s *ClaudeScan) Pass(paths []string, b Budget) (partial bool) {
	room, files := b.Bytes, 0
	s.used = Budget{}
	for _, p := range paths {
		f := s.files[p]
		if f != nil && f.upToDate(p) { // nothing appended: costs no file of the budget
			continue
		}
		if files >= b.Files || room <= 0 {
			return true
		}
		files++
		if f == nil {
			f = &claudeFile{fileState: fileState{ino: ^uint64(0), days: DaySums{}}}
			s.files[p] = f
		}
		done, n, err := readAppended(p, &f.fileState, room, func() { f.lastKey = "" }, func(raw []byte) { s.line(f, raw) })
		room -= n
		s.used.Files, s.used.Bytes = files, b.Bytes-room
		if err != nil || !done { // an unreadable file is not counted: never complete
			partial = true
		}
	}
	return partial
}

func (s *ClaudeScan) line(f *claudeFile, raw []byte) {
	var l claudeLine
	if json.Unmarshal(raw, &l) != nil || l.Type != "assistant" || l.Message.Usage == nil {
		return
	}
	key := l.Message.ID + "|" + l.RequestID
	if key == f.lastKey {
		return
	}
	f.lastKey = key
	d := day(l.Timestamp, s.loc)
	if d == "" {
		return
	}
	u := f.days[d]
	m := l.Message.Usage
	u.Add(Usage{In: m.Input, Write: m.Create, Read: m.Read, Out: m.Output, N: 1})
	f.days[d] = u
}

// Sums are the days counted so far for path (a copy).
func (s *ClaudeScan) Sums(path string) DaySums {
	out := DaySums{}
	if f := s.files[path]; f != nil {
		for d, u := range f.days {
			out[d] = u
		}
	}
	return out
}

// Used is what the last pass spent of its budget (files opened, bytes read).
func (s *ClaudeScan) Used() Budget { return s.used }
