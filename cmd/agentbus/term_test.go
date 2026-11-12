package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/termrec"
)

// fakeRecorder records nothing real: it keeps the options and writes a small
// recording, F12 free or bound.
func fakeRecorder(t *testing.T, got *termrec.Options, f12Out string) termDeps {
	t.Helper()
	return termDeps{
		record: func(_ context.Context, w io.Writer, o termrec.Options) error {
			*got = o
			enc := json.NewEncoder(w)
			enc.Encode(termrec.Header{V: termrec.Version, Client: o.Client, Fixture: o.Fixture, ClientVersion: o.ClientVersion, Argv: o.Argv, Cols: o.Cols, Rows: o.Rows, Start: time.Unix(0, 0)})
			b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
			for _, e := range []termrec.Event{
				{T: 10, Kind: termrec.KindOutput, Data: b64("ready> ")},
				{T: 20, Kind: termrec.KindInput, Data: b64("abc")},
				{T: 30, Kind: termrec.KindMark, Text: "before-f12"},
				{T: 40, Kind: termrec.KindOutput, Data: b64(f12Out)},
				{T: 50, Kind: termrec.KindMark, Text: "after-f12-kitty"},
				{T: 60, Kind: termrec.KindResize, Cols: 100, Rows: 30},
				{T: 70, Kind: termrec.KindExit, Text: "stopped: signal: terminated"},
			} {
				if e.Kind == termrec.KindOutput && e.Data == "" {
					continue
				}
				enc.Encode(e)
			}
			return nil
		},
		version: func(string) string { return "9.9.9 (fake)" },
		look:    func(exe string) (string, error) { return "/opt/bin/" + exe, nil },
	}
}

// record writes <out>/<client>/<fixture>.jsonl with the built-in script, the
// client's binary and its version; an existing recording needs --force.
func TestTermRecord(t *testing.T) {
	dir := t.TempDir()
	var o termrec.Options
	var out, errb bytes.Buffer
	if code := runTerm([]string{"record", "claude", "f12-at-input", "--out", dir, "--size", "100x30"}, fakeRecorder(t, &o, ""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	path := filepath.Join(dir, "claude", "f12-at-input.jsonl")
	if !strings.Contains(out.String(), path) || o.Client != "claude" || o.Fixture != "f12-at-input" || o.ClientVersion != "9.9.9 (fake)" ||
		strings.Join(o.Argv, " ") != "/opt/bin/claude" || o.Cols != 100 || o.Rows != 30 || len(o.Steps) == 0 {
		t.Fatalf("options %+v, out %q", o, out.String())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("the temporary file stayed")
	}
	if code := runTerm([]string{"record", "claude", "f12-at-input", "--out", dir}, fakeRecorder(t, &o, ""), &out, &errb); code != 1 || !strings.Contains(errb.String(), "--force") {
		t.Fatalf("an existing recording was replaced: %d %s", code, errb.String())
	}
	if code := runTerm([]string{"record", "claude", "f12-at-input", "--out", dir, "--force", "--cmd", "claude --model x"}, fakeRecorder(t, &o, ""), &out, &errb); code != 0 ||
		len(o.Argv) != 3 || strings.Join(o.Argv, " ") != "/opt/bin/claude --model x" {
		t.Fatalf("--cmd %q: %d %s", o.Argv, code, errb.String())
	}
	script := filepath.Join(dir, "s.txt")
	os.WriteFile(script, []byte("mark only\n"), 0o644)
	if code := runTerm([]string{"record", "kimi", "my-probe", "--out", dir, "--script", script}, fakeRecorder(t, &o, ""), &out, &errb); code != 0 || len(o.Steps) != 1 || o.Steps[0].Mark != "only" {
		t.Fatalf("--script: %d %+v %s", code, o.Steps, errb.String())
	}
}

// What record refuses: a client with no profile, a bad fixture name or
// size, a fixture with no script, a broken script, a missing binary, a
// failed recording (and then no file is left).
func TestTermRecordRefuses(t *testing.T) {
	dir := t.TempDir()
	var o termrec.Options
	bad := filepath.Join(dir, "bad.txt")
	os.WriteFile(bad, []byte("jump\n"), 0o644)
	for name, args := range map[string][]string{
		"no profile":  {"record", "gemini", "empty"},
		"fixture":     {"record", "claude", "Bad_Name"},
		"size":        {"record", "claude", "empty", "--size", "10x2"},
		"no script":   {"record", "claude", "something-else"},
		"bad script":  {"record", "claude", "x", "--script", bad},
		"no args":     {"record", "claude"},
		"unknown sub": {"play", "claude", "empty"},
	} {
		var out, errb bytes.Buffer
		if code := runTerm(append(args, "--out", dir), fakeRecorder(t, &o, ""), &out, &errb); code == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	d := fakeRecorder(t, &o, "")
	d.look = func(string) (string, error) { return "", errors.New("not found") }
	var out, errb bytes.Buffer
	if code := runTerm([]string{"record", "grok", "empty", "--out", dir}, d, &out, &errb); code != 1 || !strings.Contains(errb.String(), "not found") {
		t.Errorf("missing binary: %d %s", code, errb.String())
	}
	d = fakeRecorder(t, &o, "")
	d.record = func(context.Context, io.Writer, termrec.Options) error { return errors.New("pty failed") }
	if code := runTerm([]string{"record", "grok", "empty", "--out", dir}, d, &out, &errb); code != 1 {
		t.Errorf("failed recording: exit %d", code)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "grok")); len(entries) != 0 {
		t.Errorf("files left after a failure: %v", entries)
	}
}

// dump prints every event; --between the bytes between two marks.
func TestTermDump(t *testing.T) {
	dir := t.TempDir()
	var o termrec.Options
	var out, errb bytes.Buffer
	runTerm([]string{"record", "agy", "f12-at-input", "--out", dir}, fakeRecorder(t, &o, "[F12]"), &out, &errb)
	path := filepath.Join(dir, "agy", "f12-at-input.jsonl")
	out.Reset()
	if code := runTerm([]string{"dump", path}, termDeps{}, &out, &errb); code != 0 {
		t.Fatalf("dump: %s", errb.String())
	}
	for _, w := range []string{"agy f12-at-input (9.9.9 (fake)) 120x40", `out    "ready> "`, `in     "abc"`, `mark   "before-f12"`, "resize 100x30", `exit   "stopped: signal: terminated"`} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("dump lacks %q:\n%s", w, out.String())
		}
	}
	out.Reset()
	if code := runTerm([]string{"dump", path, "--between", "before-f12", "after-f12-kitty"}, termDeps{}, &out, &errb); code != 0 || !strings.Contains(out.String(), `5 bytes between before-f12 and after-f12-kitty: "[F12]"`) {
		t.Fatalf("between: %d %q", code, out.String())
	}
	if code := runTerm([]string{"dump", path, "--between", "before-f12", "nope"}, termDeps{}, &out, &errb); code != 1 {
		t.Fatal("a missing mark accepted")
	}
	if code := runTerm([]string{"dump", filepath.Join(dir, "none.jsonl")}, termDeps{}, &out, &errb); code != 1 {
		t.Fatal("a missing file accepted")
	}
}

// A rename that fails leaves no temporary file either (reviewer-codex): the
// destination is a directory.
func TestTermRecordLeavesNothingWhenTheRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude", "empty.jsonl")
	if err := os.MkdirAll(filepath.Join(path, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	var o termrec.Options
	var out, errb bytes.Buffer
	if code := runTerm([]string{"record", "claude", "empty", "--out", dir, "--force"}, fakeRecorder(t, &o, ""), &out, &errb); code != 1 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("the temporary file stayed after a failed rename")
	}
}
