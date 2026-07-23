package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// entry builds one assistant JSONL line with the given usage numbers.
func entry(model string, in, cacheRead, cacheCreate int, sidechain bool, ts string) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":%t,"timestamp":%q,"message":{"model":%q,`+
		`"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":7}}}`,
		sidechain, ts, model, in, cacheRead, cacheCreate)
}

func writeTranscript(t *testing.T, sessionID string, lines ...string) (root, path string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, "-data-projects-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestReadTranscriptTakesLastMainThreadTurn(t *testing.T) {
	_, p := writeTranscript(t, "sess-1",
		`{"type":"user","message":{"content":"hi"}}`,
		entry("claude-opus-4-8", 10, 1000, 100, false, "2026-07-23T09:00:00.000Z"),
		entry("claude-sonnet-5", 2, 141726, 246, false, "2026-07-23T09:05:00.000Z"),
	)
	got, err := ReadTranscript(p)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if got.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q, want the LAST turn's model", got.Model)
	}
	if want := 2 + 141726 + 246; got.ContextTokens != want {
		t.Errorf("ContextTokens = %d, want %d (input + cache read + cache creation)", got.ContextTokens, want)
	}
	if got.TS == 0 {
		t.Error("TS should come from the entry timestamp")
	}
}

func TestReadTranscriptSkipsSidechains(t *testing.T) {
	// A subagent turn has its own context window; reporting it as the agent's
	// own would overstate the fill.
	_, p := writeTranscript(t, "sess-2",
		entry("claude-sonnet-5", 1, 50_000, 0, false, "2026-07-23T09:00:00.000Z"),
		entry("claude-haiku-4-5", 1, 900_000, 0, true, "2026-07-23T09:01:00.000Z"),
	)
	got, err := ReadTranscript(p)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if got.Model != "claude-sonnet-5" || got.ContextTokens != 50_001 {
		t.Errorf("got %q/%d, want the main-thread turn (claude-sonnet-5/50001)", got.Model, got.ContextTokens)
	}
}

func TestReadTranscriptIgnoresJunkLines(t *testing.T) {
	_, p := writeTranscript(t, "sess-3",
		"", "not json at all", `{"type":"assistant","message":{"model":"m"}}`, // assistant with no usage
		entry("claude-sonnet-5", 5, 5, 5, false, "2026-07-23T09:00:00.000Z"),
	)
	got, err := ReadTranscript(p)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if got.ContextTokens != 15 {
		t.Errorf("ContextTokens = %d, want 15", got.ContextTokens)
	}
}

func TestReadTranscriptEmptyWhenNoAssistantTurn(t *testing.T) {
	_, p := writeTranscript(t, "sess-4", `{"type":"user","message":{"content":"hi"}}`)
	got, err := ReadTranscript(p)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if !got.Empty() {
		t.Errorf("want Empty(), got %+v", got)
	}
}

// A transcript larger than the tail window must still be read correctly — this
// is the path where the tail optimisation could silently lose the answer.
func TestReadTranscriptBeyondTailWindow(t *testing.T) {
	pad := `{"type":"user","message":{"content":"` + strings.Repeat("x", 4096) + `"}}`
	lines := make([]string, 0, 300)
	lines = append(lines, entry("claude-opus-4-8", 1, 1, 1, false, "2026-07-23T08:00:00.000Z"))
	for i := 0; i < 200; i++ {
		lines = append(lines, pad)
	}
	lines = append(lines, entry("claude-sonnet-5", 3, 300_000, 0, false, "2026-07-23T09:00:00.000Z"))
	for i := 0; i < 200; i++ {
		lines = append(lines, pad)
	}
	_, p := writeTranscript(t, "sess-5", lines...)

	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() <= tailBytes {
		t.Fatalf("fixture is only %d bytes; it must exceed the %d-byte tail window", st.Size(), tailBytes)
	}
	got, err := ReadTranscript(p)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if got.ContextTokens != 300_003 {
		t.Errorf("ContextTokens = %d, want 300003 — the tail read must fall back to a full scan", got.ContextTokens)
	}
}

func TestFindTranscript(t *testing.T) {
	root, p := writeTranscript(t, "sess-6", entry("m", 1, 1, 1, false, "2026-07-23T09:00:00.000Z"))
	got, err := FindTranscript(root, "sess-6")
	if err != nil {
		t.Fatalf("FindTranscript: %v", err)
	}
	if got != p {
		t.Errorf("FindTranscript = %q, want %q", got, p)
	}
	if _, err := FindTranscript(root, "nope"); err == nil {
		t.Error("unknown session should error")
	}
	if _, err := FindTranscript(root, ""); err == nil {
		t.Error("empty session id should error")
	}
}
