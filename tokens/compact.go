package tokens

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// AutoCompactAfter is a Claude Code session's last automatic compaction
// after afterMs (Unix ms): the compact_boundary record its client writes
// in the session's transcript, with trigger auto (context-escalation §3.5,
// §4.1): its preTokens and its time. The session's own transcript only, under
// any project directory of root (~/.claude/projects); a subagent's is not the
// agent's context. A line that is not such a record is skipped.
func AutoCompactAfter(root, session string, afterMs int64) (preTokens, atMs int64, found bool, err error) {
	if !ValidSessionID(session) {
		return 0, 0, false, errors.New("invalid session id")
	}
	paths, err := filepath.Glob(filepath.Join(root, "*", session+".jsonl"))
	if err != nil {
		return 0, 0, false, err
	}
	marker := []byte(`"compact_boundary"`)
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return 0, 0, false, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			raw := sc.Bytes()
			if !bytes.Contains(raw, marker) {
				continue
			}
			var l struct {
				Type      string `json:"type"`
				Subtype   string `json:"subtype"`
				Timestamp string `json:"timestamp"`
				Meta      struct {
					Trigger   string `json:"trigger"`
					PreTokens int64  `json:"preTokens"`
				} `json:"compactMetadata"`
			}
			if json.Unmarshal(raw, &l) != nil || l.Type != "system" || l.Subtype != "compact_boundary" || l.Meta.Trigger != "auto" {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, l.Timestamp)
			if err != nil || ts.UnixMilli() <= afterMs {
				continue
			}
			if ms := ts.UnixMilli(); !found || ms >= atMs {
				preTokens, atMs, found = l.Meta.PreTokens, ms, true
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return 0, 0, false, err
		}
	}
	return preTokens, atMs, found, nil
}
