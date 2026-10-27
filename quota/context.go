package quota

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Context fill (context-alert, ca1): Claude Code gives its statusLine the
// session's context fill, pre-calculated (context_window.used_percentage,
// 0-100, null before the first message: Claude Code 2.1.311's own statusLine
// documentation). agentbus statusline records it per session here, locally;
// the dispatcher publishes it per agent ({p}:context). No window table: an
// absent or null value records nothing, and the agent reads unknown.

// ContextKeep is how long a session's file is kept without a new reading.
const ContextKeep = 24 * time.Hour

// ContextReading is one session's last context fill.
type ContextReading struct {
	Pct        float64 `json:"pct"`
	ObservedAt int64   `json:"observed_at"` // Unix ms, when the status line ran
	Session    string  `json:"session"`
}

// sessionName is a session id that is safe as a file name.
var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ContextDir is where the readings are kept: <cache>/agentbus/context.
func ContextDir() string { return filepath.Join(CacheDir(), "context") }

// RecordContext writes the session's fill from a statusline stdin, when it
// carries one, and prunes the sessions not seen for ContextKeep.
func RecordContext(dir string, stdin []byte, now time.Time) error {
	var in struct {
		SessionID     string `json:"session_id"`
		ContextWindow struct {
			UsedPercentage *float64 `json:"used_percentage"`
		} `json:"context_window"`
	}
	if json.Unmarshal(stdin, &in) != nil || in.ContextWindow.UsedPercentage == nil || !sessionName.MatchString(in.SessionID) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(ContextReading{Pct: *in.ContextWindow.UsedPercentage, ObservedAt: now.UnixMilli(), Session: in.SessionID})
	if err := writeAtomic(filepath.Join(dir, in.SessionID+".json"), b); err != nil {
		return err
	}
	pruneContext(dir, now)
	return nil
}

// pruneContext removes the readings older than ContextKeep.
func pruneContext(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if r, ok := ReadContext(dir, strings.TrimSuffix(name, ".json")); !ok || now.Sub(time.UnixMilli(r.ObservedAt)) > ContextKeep {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// ReadContext is the session's last reading, if there is one.
func ReadContext(dir, session string) (ContextReading, bool) {
	if !sessionName.MatchString(session) {
		return ContextReading{}, false
	}
	b, err := os.ReadFile(filepath.Join(dir, session+".json"))
	if err != nil {
		return ContextReading{}, false
	}
	var r ContextReading
	if json.Unmarshal(b, &r) != nil || r.Session != session {
		return ContextReading{}, false
	}
	return r, true
}
