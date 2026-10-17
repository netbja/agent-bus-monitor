package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadUsedModel(t *testing.T) {
	write := func(lines ...string) string {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		body := ""
		for _, l := range lines {
			body += l + "\n"
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ok := `{"type":"assistant","timestamp":"2026-10-17T08:00:00Z","message":{"model":"claude-sonnet-5","content":[{"type":"text","text":"hi"}]}}`
	side := `{"type":"assistant","isSidechain":true,"timestamp":"2026-10-17T08:01:00Z","message":{"model":"claude-haiku-4-5"}}`
	synth := `{"type":"assistant","timestamp":"2026-10-17T08:02:00Z","isApiErrorMessage":true,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 404 not_found_error model: claude-opus-3"}]}}`
	got, err := ReadUsedModel(write(ok, side, synth))
	if err != nil || got.Model != "claude-sonnet-5" || got.ModelError == "" {
		t.Fatalf("got %+v, %v: want sonnet used and a model error", got, err)
	}
	later := `{"type":"assistant","timestamp":"2026-10-17T08:03:00Z","message":{"model":"claude-sonnet-5-5"}}`
	if got, _ := ReadUsedModel(write(ok, synth, later)); got.Model != "claude-sonnet-5-5" || got.ModelError != "" {
		t.Fatalf("an answer after the error kept it: %+v", got)
	}
	limit := `{"type":"assistant","timestamp":"2026-10-17T08:02:00Z","isApiErrorMessage":true,"message":{"model":"<synthetic>","content":[{"type":"text","text":"You've hit your limit"}]}}`
	if got, _ := ReadUsedModel(write(ok, limit)); got.ModelError != "" {
		t.Fatalf("a limit error read as a model error: %+v", got)
	}
}
