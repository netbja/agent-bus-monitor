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
// §5.1): an older reset never replaces; a later one does; the same reset with
// a different percent replaces, up or down; the same reset and percent keeps
// the stored observation time; a window absent from the reading stays.
func MergeWindows(stored map[string]storedWindow, in []Window, now time.Time) (map[string]storedWindow, bool) {
	out := make(map[string]storedWindow, len(stored)+len(in))
	for k, v := range stored {
		out[k] = v
	}
	changed := false
	for _, w := range in {
		old, ok := out[w.Name]
		switch {
		case !ok, w.ResetsAt > old.ResetsAt, w.ResetsAt == old.ResetsAt && w.UsedPct != old.UsedPct:
			out[w.Name] = storedWindow{Window: w, ObservedAt: now.UnixMilli()}
			changed = true
		}
	}
	return out, changed
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

// RecordStatusline merges one statusline stdin into the file, under a lock.
func RecordStatusline(path string, stdin []byte, now time.Time) error {
	ws, err := ParseStatusline(stdin)
	if err != nil || len(ws) == 0 {
		return err
	}
	return withLock(path, func() error {
		var st claudeStore
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &st)
		}
		merged, changed := MergeWindows(st.Windows, ws, now)
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
	for name, hit := range transcriptRejections(c.Transcripts, now) {
		if s, ok := stored[name]; ok && (s.ResetsAt > hit.ResetsAt || s.ObservedAt > hit.ObservedAt) {
			continue // a fresher statusline reading outranks the backstop
		}
		stored[name] = hit
		r.Source = "statusline+transcript"
	}
	if len(stored) == 0 {
		return Unknown("anthropic", "statusline", "no statusline reading yet (agentbus statusline --print-install)"), nil
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
// is ignored; a hit sets that window to 100 %.
func transcriptRejections(root string, now time.Time) map[string]storedWindow {
	out := map[string]storedWindow{}
	files, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil || now.Sub(st.ModTime()) > 6*time.Hour {
			continue
		}
		for _, line := range tailLines(f, 256<<10) {
			if !bytes.Contains(line, []byte(`"rateLimitType"`)) {
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
	return out
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
