package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The launcher's preflight asks `claude --help` for these flags (rolecfg's
// preflight, claude-code, a resume and an effort included).
func TestFakeClaudeHelpNamesTheLaunchFlags(t *testing.T) {
	var out bytes.Buffer
	if code := runFakeClient([]string{"claude", "--help"}, nil, &out, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, f := range []string{"--model", "--append-system-prompt", "--name", "--resume", "--effort", "--fallback-model", "--permission-mode", "--settings"} {
		if !strings.Contains(out.String(), f) {
			t.Errorf("--help lacks %s:\n%s", f, out.String())
		}
	}
}

func TestFakeClientRefusesAnotherClient(t *testing.T) {
	var errb bytes.Buffer
	if code := runFakeClient([]string{"codex"}, nil, io.Discard, &errb); code != 2 || !strings.Contains(errb.String(), "claude") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

// claudeSettings is a launch's --settings in Claude's shape (rolecfg's
// settingsJSON): each event a list of matchers, each with its commands.
func claudeSettings(start, submit, stop []string) string {
	m := func(cmds []string) []any {
		var hs []any
		for _, c := range cmds {
			hs = append(hs, map[string]string{"type": "command", "command": c})
		}
		return []any{map[string]any{"hooks": hs}}
	}
	b, _ := json.Marshal(map[string]any{"permissions": map[string]any{"deny": []string{"Bash(rm:*)"}},
		"hooks": map[string]any{"SessionStart": m(start), "UserPromptSubmit": m(submit), "Stop": m(stop)}})
	return string(b)
}

func TestFakeClaudeArgs(t *testing.T) {
	const s = "0b8f3c55-9d6e-4a57-b3a1-2f0f6d1e9c11"
	a, err := parseFakeClaude([]string{"--model", "opus", "--resume", s, "--fallback-model", "sonnet", "--permission-mode", "plan",
		"--settings", claudeSettings([]string{"a", "b"}, []string{"c"}, []string{"d"}), "--effort", "high", "--name", "demo:coder",
		"--append-system-prompt", "the role", "Run the On boot steps of your role now."})
	if err != nil {
		t.Fatal(err)
	}
	if a.Resume != s || a.Name != "demo:coder" || a.Prompt != "Run the On boot steps of your role now." {
		t.Fatalf("%+v", a)
	}
	if strings.Join(a.Hooks["SessionStart"], ",") != "a,b" || strings.Join(a.Hooks["UserPromptSubmit"], ",") != "c" || strings.Join(a.Hooks["Stop"], ",") != "d" {
		t.Fatalf("hooks %v", a.Hooks)
	}
	if _, err := parseFakeClaude([]string{"--settings", "{not json"}); err == nil {
		t.Fatal("bad settings accepted")
	}
	if _, err := parseFakeClaude([]string{"--resume"}); err == nil {
		t.Fatal("a flag without its value accepted")
	}
	if _, err := parseFakeClaude([]string{"--resume", "../x"}); err == nil {
		t.Fatal("an invalid session accepted")
	}
}

// A fresh start draws a new session, valid as a recorded one.
func TestFakeClaudeNewSession(t *testing.T) {
	a, b := newFakeSession(), newFakeSession()
	if !bus.ValidSessionID(a) || a == b {
		t.Fatalf("%q %q", a, b)
	}
}

// The fake run as the launcher runs Claude Code, on a terminal: its
// SessionStart hooks get the session (the resumed one), its boot line is a
// turn (submit and stop hooks, presence published through agentbus), and a
// wake line typed into it runs the drain through agentbus, between its turn
// hooks; the screen says what it did.
func TestFakeClaudeOnATerminal(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	hook := func(name string) string {
		return "printf '" + name + " ' >> " + rec + "; cat >> " + rec + "; echo >> " + rec
	}
	bin := filepath.Join(dir, "agentbus")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"agentbus $*\" >> "+rec+"\nprintf '{\"v\":1,\"event\":\"cmd\"}\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	const s = "0b8f3c55-9d6e-4a57-b3a1-2f0f6d1e9c11"
	exe, _ := os.Executable()
	c := exec.Command(exe, "-test.run=^$")
	c.Env = append(os.Environ(), "AGENTBUS_E2E_FAKE=fakeclient", "AGENT_BUS_PROJECT=demo", "AGENT_BUS_AGENT=coder", "AGENTBUS_BIN="+bin,
		"AGENTBUS_FAKE_ARGS="+strings.Join([]string{"claude", "--model", "opus", "--resume", s,
			"--settings", claudeSettings([]string{hook("start")}, []string{hook("submit")}, []string{hook("stop")}),
			"--name", "demo:coder", "--append-system-prompt", "role", "Run the On boot steps of your role now."}, "\x1f"))
	f, err := pty.StartWithSize(c, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { c.Process.Kill(); c.Wait(); f.Close() }()
	var screen e2eBuf
	go io.Copy(&screen, f)
	read := func() string { b, _ := os.ReadFile(rec); return string(b) }
	if !until(10*time.Second, func() bool { return strings.Contains(read(), "stop ") && strings.Contains(screen.String(), "❯") }) {
		t.Fatalf("no boot turn:\n%s\nscreen:\n%s", read(), screen.String())
	}
	got := read()
	for _, want := range []string{`start {"hook_event_name":"SessionStart","session_id":"` + s + `"`,
		`submit {"hook_event_name":"UserPromptSubmit","prompt":"Run the On boot steps of your role now.","session_id":"` + s + `"`,
		"agentbus --project demo status coder idle coder online",
		`stop {"hook_event_name":"Stop","session_id":"` + s + `"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(screen.String(), "resumed "+s) {
		t.Fatalf("screen does not say the resume:\n%s", screen.String())
	}
	// The wake line, as the dispatcher types it: a bracketed paste, then Enter.
	f.WriteString("\x1b[200~[agentbus#1] demo: 1 message(s) waiting for coder. Run: agentbus --project demo subscribe --drain coder\x1b[201~")
	time.Sleep(100 * time.Millisecond)
	f.WriteString("\r")
	if !until(10*time.Second, func() bool {
		return strings.Contains(read(), "subscribe --drain coder") && strings.Count(read(), "stop ") == 2
	}) {
		t.Fatalf("no drain turn:\n%s", read())
	}
	if !until(5*time.Second, func() bool { return strings.Contains(screen.String(), "drained 1") }) {
		t.Fatalf("screen:\n%s", screen.String())
	}
	if strings.Index(read(), "subscribe --drain") < strings.LastIndex(read(), "submit ") {
		t.Fatalf("drain before its turn started:\n%s", read())
	}
}
