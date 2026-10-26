package quota

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Claude Code hands its statusLine command the account's windows on stdin
// (spec §5.1). `agentbus statusline` merges them into a file of ours; the
// claude reader reads that file, plus a transcript backstop for a limit hit.

// StatuslineMax is the largest stdin parsed; a bigger one is passed through
// to --then but never parsed.
const StatuslineMax = 64 << 10

type slWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"`
	Period         string   `json:"period"`
}

// claudeWindowNames maps statusLine names to canonical ones and lengths.
var claudeWindowNames = map[string]struct {
	name    string
	minutes int
}{
	"five_hour":                  {"5h", 300},
	"seven_day":                  {"7d", 10080},
	"seven_day_opus":             {"7d-opus", 10080},
	"seven_day_sonnet":           {"7d-sonnet", 10080},
	"seven_day_overage_included": {"7d-overage", 10080},
}

// ParseStatusline extracts the rate-limit windows from Claude Code's
// statusLine stdin. Nothing else of the payload is kept.
func ParseStatusline(stdin []byte) ([]Window, error) {
	if len(stdin) > StatuslineMax {
		return nil, errors.New("statusline input over the parse cap")
	}
	var in struct {
		RateLimits map[string]json.RawMessage `json:"rate_limits"`
	}
	if err := json.Unmarshal(stdin, &in); err != nil {
		return nil, err
	}
	var out []Window
	for key, raw := range in.RateLimits {
		var w slWindow
		if json.Unmarshal(raw, &w) != nil || w.UsedPercentage == nil || w.ResetsAt == nil || *w.ResetsAt <= 0 {
			continue
		}
		name, minutes := key, 0
		if m, ok := claudeWindowNames[key]; ok {
			name, minutes = m.name, m.minutes
		} else if key == "spend_limit" {
			name = "spend"
			if w.Period != "" {
				name = "spend-" + w.Period
			}
		} else {
			continue // a window this version does not know: not guessed at
		}
		out = append(out, Window{Name: name, UsedPct: *w.UsedPercentage, ResetsAt: *w.ResetsAt, Minutes: minutes})
	}
	return out, nil
}

// storedWindow is a window in the statusline file, with when its value last
// changed.
type storedWindow struct {
	Window
	ObservedAt int64 `json:"observed_at"`
}

type claudeStore struct {
	Windows map[string]storedWindow `json:"windows"`
}

// MergeWindows applies one statusline reading to the stored windows (spec
// §5.1). sourceAt is when the reading was true: the session's last API
// answer, not the render (see StatuslineSourceAt); zero = undated, stamped
// now. An older reset never replaces; a later one does; the same reset with
// a different percent replaces, up or down, only from a dated source newer
// than the stored one — an idle session keeps rendering the number of its
// last answer and must not overwrite a later one; the same reset and percent
// keeps the stored observation time; a window absent from the reading stays.
func MergeWindows(stored map[string]storedWindow, in []Window, sourceAt, now time.Time) (map[string]storedWindow, bool) {
	out := make(map[string]storedWindow, len(stored)+len(in))
	for k, v := range stored {
		out[k] = v
	}
	at := sourceAt
	if at.IsZero() {
		at = now
	}
	changed := false
	for _, w := range in {
		old, ok := out[w.Name]
		switch {
		case !ok, w.ResetsAt > old.ResetsAt,
			w.ResetsAt == old.ResetsAt && w.UsedPct != old.UsedPct && !sourceAt.IsZero() && sourceAt.UnixMilli() > old.ObservedAt:
			out[w.Name] = storedWindow{Window: w, ObservedAt: at.UnixMilli()}
			changed = true
		}
	}
	return out, changed
}

// StatuslineSourceAt dates a statusline stdin: Claude Code renders the
// windows of the session's last API answer, so the reading was true at that
// answer — the newest real answer in the tail of the session's transcript.
// Zero when there is none (no transcript named or readable, no answer in the
// tail, a fresh transcript after /clear): the reading is undated, and an
// undated reading never wins a same-reset change.
func StatuslineSourceAt(stdin []byte) time.Time {
	var in struct {
		TranscriptPath string `json:"transcript_path"`
	}
	if json.Unmarshal(stdin, &in) != nil || in.TranscriptPath == "" {
		return time.Time{}
	}
	if st, err := os.Stat(in.TranscriptPath); err != nil || !st.Mode().IsRegular() {
		return time.Time{}
	}
	var served int64
	for _, line := range tailLines(in.TranscriptPath, 256<<10) {
		if at := servedAt(line); at > served {
			served = at
		}
	}
	if served == 0 {
		return time.Time{}
	}
	return time.UnixMilli(served)
}

// ClaudeCachePath is the statusline file: $XDG_CACHE_HOME/agentbus or
// ~/.cache/agentbus.
func ClaudeCachePath() string { return filepath.Join(CacheDir(), "quota-claude.json") }

// CacheDir is agentbus's cache directory.
func CacheDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "agentbus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "agentbus")
}

// RecordStatusline merges one statusline stdin into the file, under a lock,
// dated by StatuslineSourceAt.
func RecordStatusline(path string, stdin []byte, now time.Time) error {
	ws, err := ParseStatusline(stdin)
	if err != nil || len(ws) == 0 {
		return err
	}
	sourceAt := StatuslineSourceAt(stdin) // read outside the lock
	return withLock(path, func() error {
		var st claudeStore
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &st)
		}
		merged, changed := MergeWindows(st.Windows, ws, sourceAt, now)
		if !changed {
			return nil
		}
		b, _ := json.Marshal(claudeStore{Windows: merged})
		return writeAtomic(path, b)
	})
}

// withLock runs f holding an exclusive flock on path+".lock".
func withLock(path string, f func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	return f()
}

// writeAtomic writes b to path (0600) through a rename.
func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".quota-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ClaudeReader reads the statusline file and the transcript backstop.
type ClaudeReader struct {
	Cache       string // the statusline file
	Transcripts string // ~/.claude/projects
	Now         func() time.Time
}

func (ClaudeReader) Provider() string { return "anthropic" }

func (c ClaudeReader) Read(ctx context.Context) (Reading, error) {
	now := c.Now()
	r := Reading{Provider: "anthropic", Source: "statusline", CollectedAt: now.UnixMilli()}
	stored := map[string]storedWindow{}
	if b, err := os.ReadFile(c.Cache); err == nil {
		var st claudeStore
		if json.Unmarshal(b, &st) == nil {
			stored = st.Windows
		}
	}
	hits, served := transcriptRejections(c.Transcripts, now)
	r.AvailableAt = served
	for name, hit := range hits {
		if s, ok := stored[name]; ok && (s.ResetsAt > hit.ResetsAt || s.ObservedAt > hit.ObservedAt) {
			continue // a fresher statusline reading outranks the backstop
		}
		stored[name] = hit
		r.Source = "statusline+transcript"
	}
	if len(stored) == 0 {
		u := Unknown("anthropic", "statusline", "no statusline reading yet (agentbus statusline --print-install)")
		u.AvailableAt = served
		return u, nil
	}
	for _, s := range stored {
		w := s.Window
		w.ObservedAt = s.ObservedAt
		r.Windows = append(r.Windows, w)
		if s.ObservedAt > r.ObservedAt {
			r.ObservedAt = s.ObservedAt
		}
	}
	return r, nil
}

// transcriptRejections: live limit hits in recent Claude transcripts (spec
// §5.1 backstop) — only status, type and reset are read; a reset in the past
// is ignored; a hit sets that window to 100 %. served is the newest answer
// the API gave (an assistant turn that is not a synthetic error) in any of
// them: a hit older than it no longer holds — the account was served since,
// as after a quota reset granted before the announced time.
func transcriptRejections(root string, now time.Time) (map[string]storedWindow, int64) {
	out := map[string]storedWindow{}
	var served int64
	files, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil || now.Sub(st.ModTime()) > 6*time.Hour {
			continue
		}
		for _, line := range tailLines(f, 256<<10) {
			if !bytes.Contains(line, []byte(`"rateLimitType"`)) {
				if at := servedAt(line); at > served {
					served = at
				}
				continue
			}
			var e struct {
				Timestamp   string `json:"timestamp"`
				QuotaLimits *struct {
					Status        string `json:"status"`
					RateLimitType string `json:"rateLimitType"`
					ResetsAt      int64  `json:"resetsAt"`
				} `json:"quotaLimits"`
			}
			if json.Unmarshal(line, &e) != nil || e.QuotaLimits == nil || e.QuotaLimits.Status != "rejected" {
				continue
			}
			q := e.QuotaLimits
			m, ok := claudeWindowNames[q.RateLimitType]
			if !ok || time.Unix(q.ResetsAt, 0).Before(now) {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
			if err != nil {
				at = st.ModTime()
			}
			w := storedWindow{Window: Window{Name: m.name, UsedPct: 100, ResetsAt: q.ResetsAt, Minutes: m.minutes}, ObservedAt: at.UnixMilli()}
			if old, ok := out[m.name]; !ok || w.ObservedAt > old.ObservedAt {
				out[m.name] = w
			}
		}
	}
	for name, w := range out {
		if w.ObservedAt <= served {
			delete(out, name)
		}
	}
	return out, served
}

// servedAt is the time of an assistant turn the API really answered (Unix
// ms), 0 for anything else: a synthetic error (a limit hit, an outage) is not
// an answer. Only type, timestamp, the error flag and the model are read.
func servedAt(line []byte) int64 {
	if !bytes.Contains(line, []byte(`"type":"assistant"`)) {
		return 0
	}
	var e struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		APIError  bool   `json:"isApiErrorMessage"`
		Message   struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || e.APIError || e.Message.Model == "" || e.Message.Model == "<synthetic>" {
		return 0
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return 0
	}
	return at.UnixMilli()
}

// tailLines returns the complete lines of the last max bytes of a file.
func tailLines(path string, max int64) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	off := int64(0)
	if st.Size() > max {
		off = st.Size() - max
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), int(max)+1)
	var out [][]byte
	first := off > 0
	for sc.Scan() {
		if first { // the first line of a mid-file tail is partial
			first = false
			continue
		}
		out = append(out, append([]byte(nil), sc.Bytes()...))
	}
	return out
}
