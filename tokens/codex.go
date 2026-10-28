package tokens

import (
	"encoding/json"
	"sort"
	"time"
)

// CodexScan counts Codex rollouts ($CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl).
// Each token_count adds its total's delta from the previous one of the same
// session to the day of its own timestamp: the baseline crosses midnight and
// files. The first event of a session never seen before adds its
// last_token_usage; a repeated total adds 0; a lower total (a reset) adds
// last_token_usage. The baseline follows the session id (the file's first
// session_meta), so a resumed session, appended or in a new file whose first
// event replays the last total, counts no turn twice: the total is
// cumulative, so a first event equal to the session's previous total carries
// no new tokens, whatever its last_token_usage says. Known edge: a file
// replaced or shrunk whose events all repeat the session's last total is
// rescanned to nothing (its days were cleared, its events now add 0). Only the session id is
// decoded from session_meta: its account identifiers never reach a structure.
type CodexScan struct {
	loc   *time.Location
	files map[string]*codexFile
	last  map[string]codexTotal // session -> its last total, whichever file
}

type codexFile struct {
	fileState
	path    string
	session string
}

type codexTotal struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Output int64 `json:"output_tokens"`
	Total  int64 `json:"total_tokens"`
}

func (t codexTotal) usage() Usage {
	return Usage{In: max(0, t.Input-t.Cached), Read: t.Cached, Out: t.Output}
}

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		ID   string `json:"id"`   // session_meta
		Type string `json:"type"` // event_msg
		Info *struct {
			Total codexTotal `json:"total_token_usage"`
			Last  codexTotal `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// NewCodexScan buckets days in loc.
func NewCodexScan(loc *time.Location) *CodexScan {
	return &CodexScan{loc: loc, files: map[string]*codexFile{}, last: map[string]codexTotal{}}
}

// Pass reads what was appended to paths, oldest first (rollout names start
// with their time), within budget; partial when a file is not read to its end.
func (s *CodexScan) Pass(paths []string, b Budget) (partial bool) {
	sorted := append([]string(nil), paths...)
	sort.Slice(sorted, func(i, j int) bool { return baseName(sorted[i]) < baseName(sorted[j]) })
	room, files := b.Bytes, 0
	for _, p := range sorted {
		f := s.files[p]
		if f != nil && f.upToDate(p) { // nothing appended: costs no file of the budget
			continue
		}
		if files >= b.Files || room <= 0 {
			return true
		}
		files++
		if f == nil {
			f = &codexFile{fileState: fileState{ino: ^uint64(0), days: DaySums{}}, path: p}
			s.files[p] = f
		}
		done, n, err := readAppended(p, &f.fileState, room, func() { f.session = "" }, func(raw []byte) { s.line(f, raw) })
		room -= n
		if err == nil && !done {
			partial = true
		}
	}
	return partial
}

func (s *CodexScan) line(f *codexFile, raw []byte) {
	var l codexLine
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	switch {
	case l.Type == "session_meta":
		if f.session == "" {
			f.session = l.Payload.ID
		}
		return
	case l.Type != "event_msg" || l.Payload.Type != "token_count" || l.Payload.Info == nil:
		return
	}
	cur, last := l.Payload.Info.Total, l.Payload.Info.Last
	key := f.session
	if key == "" { // no session_meta: its own session, never chained to another such file
		key = "path:" + f.path
	}
	var add Usage
	prev, seen := s.last[key]
	switch {
	case !seen:
		add = last.usage()
	case cur.Total < prev.Total:
		add = last.usage() // a reset
	default:
		add = Usage{In: max(0, (cur.Input-cur.Cached)-(prev.Input-prev.Cached)), Read: max(0, cur.Cached-prev.Cached),
			Out: max(0, cur.Output-prev.Output)}
	}
	s.last[key] = cur
	if add == (Usage{}) {
		return // a repeat
	}
	d := day(l.Timestamp, s.loc)
	if d == "" {
		return
	}
	add.N = 1
	u := f.days[d]
	u.Add(add)
	f.days[d] = u
}

// Sums are the days counted so far for path (a copy).
func (s *CodexScan) Sums(path string) DaySums {
	out := DaySums{}
	if f := s.files[path]; f != nil {
		for d, u := range f.days {
			out[d] = u
		}
	}
	return out
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
