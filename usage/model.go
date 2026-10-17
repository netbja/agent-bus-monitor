package usage

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// UsedModel (v0.15.1 §11.7) is what the agent's own transcript says about
// the model it really runs: the last main-thread assistant entry whose
// message.model is set and is not "<synthetic>" (an error turn), with its
// time, and a recent API error that names the model as unknown or retired.
type UsedModel struct {
	Model      string
	TS         int64  // ms since epoch
	ModelError string // the error text, when the API refused the model
	ErrorTS    int64  // when it was refused (ms since epoch)
}

type modelEntry struct {
	Type              string `json:"type"`
	IsSidechain       bool   `json:"isSidechain"`
	Timestamp         string `json:"timestamp"`
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	Message           struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// ReadUsedModel scans the transcript's tail (tailBytes) for the used model.
// A model error older than the last real answer is void: the model works.
func ReadUsedModel(path string) (UsedModel, error) {
	f, err := os.Open(path)
	if err != nil {
		return UsedModel{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return UsedModel{}, err
	}
	start := int64(0)
	if st.Size() > tailBytes {
		start = st.Size() - tailBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return UsedModel{}, err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return UsedModel{}, err
	}
	lines := strings.Split(string(buf), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line of the window is cut
	}
	var out UsedModel
	var errTS int64
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var e modelEntry
		if json.Unmarshal([]byte(line), &e) != nil || e.Type != "assistant" || e.IsSidechain {
			continue
		}
		var ts int64
		if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			ts = t.UnixMilli()
		}
		if e.IsAPIErrorMessage || e.Message.Model == "<synthetic>" {
			if text := strings.ToLower(string(e.Message.Content)); modelRefused(text) {
				out.ModelError, errTS = clipText(string(e.Message.Content), 200), ts
			}
			continue
		}
		if e.Message.Model != "" {
			out.Model, out.TS = e.Message.Model, ts
		}
	}
	if out.ModelError != "" && out.TS > errTS {
		out.ModelError = "" // answered since: the model works
	}
	if out.ModelError != "" {
		out.ErrorTS = errTS
	}
	return out, nil
}

// modelRefused: an API error that names the model as unknown or retired.
func modelRefused(lower string) bool {
	if !strings.Contains(lower, "model") {
		return false
	}
	for _, s := range []string{"not_found", "not found", "does not exist", "deprecated", "retired", "invalid model", "unknown model"} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func clipText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
