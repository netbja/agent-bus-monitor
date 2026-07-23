package usage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tailBytes is how much of a transcript we read from the end before falling back
// to a full scan. Transcripts run to several MB; the entry we want is the last
// one, so reading the tail turns an O(file) scan into O(1) for the common case.
const tailBytes = 512 << 10

// Transcript is what one agent's session file says about that agent right now.
//
// ContextTokens is the real context fill of the last request (input + cache
// read + cache creation). It is NOT expressed as a percentage: that would need a
// per-model context-limit table, and such tables rot silently every time a model
// ships with a different window. Likewise no cost figure — on a subscription a
// dollar amount is fiction, and computing one needs a price table that rots the
// same way. Raw tokens are the thing we actually know.
type Transcript struct {
	Model         string
	ContextTokens int
	TS            int64 // ms since epoch, from the entry's timestamp
}

// Empty reports whether the scan found no assistant turn carrying usage.
func (t Transcript) Empty() bool { return t.ContextTokens == 0 && t.Model == "" }

// transcriptEntry is the subset of a JSONL line we care about. Everything else
// in the entry is ignored, so new fields upstream cannot break the read.
type transcriptEntry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ProjectsRoot is where Claude Code keeps per-project transcripts.
func ProjectsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// FindTranscript locates a session's JSONL under root. Claude Code slugs the
// project directory into the path, so we glob rather than reconstruct the slug —
// the slugging rule is Claude Code's business and has changed before.
func FindTranscript(root, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("empty session id")
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", sessionID+".jsonl"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no transcript for session %s under %s", sessionID, root)
	}
	return matches[0], nil
}

// ReadTranscript returns the last main-thread assistant turn's model and context
// fill. Sidechain entries (subagent turns) are skipped: their context is a
// separate window and reporting it as the agent's own would overstate the fill.
func ReadTranscript(path string) (Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return Transcript{}, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return Transcript{}, err
	}

	if st.Size() > tailBytes {
		if t, err := scanTail(f, st.Size()); err == nil && !t.Empty() {
			return t, nil
		}
		// The last assistant turn was bigger than the tail window — pay for the
		// full scan rather than report nothing.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return Transcript{}, err
		}
	}
	return scanAll(f)
}

// scanTail reads the final tailBytes and keeps the last usable entry. The first
// line of the window is almost certainly truncated, so it is dropped.
func scanTail(f *os.File, size int64) (Transcript, error) {
	if _, err := f.Seek(size-tailBytes, io.SeekStart); err != nil {
		return Transcript{}, err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return Transcript{}, err
	}
	lines := strings.Split(string(buf), "\n")
	if len(lines) > 0 {
		lines = lines[1:]
	}
	var out Transcript
	for _, line := range lines {
		if t, ok := parseEntry([]byte(line)); ok {
			out = t
		}
	}
	return out, nil
}

// scanAll walks the whole file. bufio.Reader (not Scanner) because a single
// transcript line routinely exceeds any fixed token limit.
func scanAll(f *os.File) (Transcript, error) {
	r := bufio.NewReader(f)
	var out Transcript
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if t, ok := parseEntry(line); ok {
				out = t
			}
		}
		if err != nil {
			if err == io.EOF {
				return out, nil
			}
			return out, err
		}
	}
}

// parseEntry reports whether a line is a main-thread assistant turn with usage.
func parseEntry(line []byte) (Transcript, bool) {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 || line[0] != '{' {
		return Transcript{}, false
	}
	var e transcriptEntry
	if json.Unmarshal(line, &e) != nil {
		return Transcript{}, false
	}
	if e.Type != "assistant" || e.IsSidechain || e.Message.Usage == nil {
		return Transcript{}, false
	}
	u := e.Message.Usage
	t := Transcript{
		Model:         e.Message.Model,
		ContextTokens: u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
	}
	if ts, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
		t.TS = ts.UnixMilli()
	}
	return t, true
}
