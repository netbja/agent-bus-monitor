package quota

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ClaudeLoginExpired reports whether Claude Code's login has expired, from
// its transcripts (spec 2026-11-15-login-updates-design.md 2.1): the newest
// entry the client wrote for an authentication failure (isApiErrorMessage,
// error "authentication_failed", model <synthetic>: fields the client sets,
// never the model) in a transcript touched in the last 6 h, when no answer
// the API served is newer in any of them (a /login in any session clears
// it). The login is the account's: every Claude agent shares it. Only
// type, timestamp, the error fields and the model are read.
func ClaudeLoginExpired(root string, now time.Time) (time.Time, bool) {
	var failed, served int64
	files, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil || now.Sub(st.ModTime()) > 6*time.Hour {
			continue
		}
		for _, line := range tailLines(f, 256<<10) {
			if at := servedAt(line); at > served {
				served = at
			}
			if at := authFailedAt(line); at > failed {
				failed = at
			}
		}
	}
	if failed == 0 || failed <= served {
		return time.Time{}, false
	}
	return time.UnixMilli(failed), true
}

// authFailedAt is the time (Unix ms) of the client's authentication-failure
// entry, 0 for any other line.
func authFailedAt(line []byte) int64 {
	if !bytes.Contains(line, []byte(`"authentication_failed"`)) {
		return 0
	}
	var e struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		APIError  bool   `json:"isApiErrorMessage"`
		Error     string `json:"error"`
		Message   struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || !e.APIError || e.Error != "authentication_failed" ||
		e.Message.Model != "<synthetic>" {
		return 0
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return 0
	}
	return at.UnixMilli()
}
