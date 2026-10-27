package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func readFixture(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSignatures runs every client's signature over the screens captured in
// the spec §10 probes (see testdata/screens/README.md for real vs synthetic).
func TestSignatures(t *testing.T) {
	cases := []struct {
		client, screen string
		ready, busy    bool
	}{
		{"claude", "claude-empty", true, false},
		{"claude", "claude-idle", true, false},
		{"claude", "claude-typed", false, false},
		{"claude", "claude-busy", true, true}, // input box stays empty during a turn: busy decides
		{"claude", "claude-modal", false, false},
		{"codex", "codex-empty", true, false},
		{"codex", "codex-typed", false, false},
		{"codex", "codex-scroll", false, false},
		{"kimi", "kimi-empty", true, false},
		{"kimi", "kimi-typed", false, false},
		{"kimi", "kimi-spinner", true, true},
		{"grok", "grok-empty", true, false},
		{"grok", "grok-typed", false, false},
		{"agy", "agy-empty", true, false},
		{"agy", "agy-typed", false, false},
		{"agy", "agy-approval", false, false},
		// Only the CURRENT (last) input box counts: an old empty box higher in
		// scrollback must not make a typed current box look ready, and an old
		// typed box must not veto an empty current one. DERIVED fixtures.
		{"claude", "claude-old-empty-typed", false, false},
		{"claude", "claude-old-typed-empty", true, false},
		{"codex", "codex-old-empty-typed", false, false},
		{"codex", "codex-old-typed-empty", true, false},
		{"kimi", "kimi-old-empty-typed", false, false},
		{"kimi", "kimi-old-typed-empty", true, false},
		{"grok", "grok-old-empty-typed", false, false},
		{"grok", "grok-old-typed-empty", true, false},
		{"agy", "agy-old-empty-typed", false, false},
		{"agy", "agy-old-typed-empty", true, false},
	}
	for _, c := range cases {
		t.Run(c.screen, func(t *testing.T) {
			sig, ok := Signatures[c.client]
			if !ok {
				t.Fatalf("no signature for %s", c.client)
			}
			ready, busy := sig.match(readFixture(t, "screens/"+c.screen+".txt"))
			if ready != c.ready || busy != c.busy {
				t.Fatalf("%s: ready=%v busy=%v; want ready=%v busy=%v", c.screen, ready, busy, c.ready, c.busy)
			}
		})
	}
	for name, sig := range Signatures {
		if sig.Prompt == nil {
			t.Errorf("%s: no Prompt marker; its readiness could match an old input box", name)
		}
		// No input line on screen at all: never ready.
		if ready, _ := sig.match("some output\nmore output\n"); ready {
			t.Errorf("%s: ready without any input line", name)
		}
		if !Supported()[name] {
			t.Errorf("Supported() misses %s", name)
		}
	}
	if Supported()["gemini"] {
		t.Error("an unprobed client is supported")
	}
}

func TestParseAgentGet(t *testing.T) {
	a, err := parseAgentGet([]byte(readFixture(t, "herdr/agent-get-kimi.json")))
	if err != nil {
		t.Fatal(err)
	}
	if a.Agent != "kimi" || a.Status != "idle" || a.Focused || a.Terminal != "term_65d790de0b4604" {
		t.Fatalf("parseAgentGet = %+v", a)
	}
	c, err := parseAgentGet([]byte(readFixture(t, "herdr/agent-get-claude.json")))
	if err != nil || c.Agent != "claude" {
		t.Fatalf("claude: %+v %v", c, err)
	}
	_, err = parseAgentGet([]byte(readFixture(t, "herdr/agent-get-notfound.json")))
	if !isGone(err) {
		t.Fatalf("unknown pane: err = %v; want gone", err)
	}
}

func TestParseProcessInfo(t *testing.T) {
	pids, err := parseProcessInfo([]byte(readFixture(t, "herdr/process-info-kimi.json")))
	if err != nil {
		t.Fatal(err)
	}
	if len(pids) != 1 || pids[0] != 1983859 {
		t.Fatalf("pids = %v", pids)
	}
	_, err = parseProcessInfo([]byte(readFixture(t, "herdr/process-info-notfound.json")))
	if !isGone(err) {
		t.Fatalf("unknown pane: err = %v; want gone", err)
	}
}

// fakeHerdr builds a FAKE_HERDR_DIR from fixtures (dst name -> testdata path
// or literal content prefixed with "=") and returns a Herdr pointing at it.
func fakeHerdr(t *testing.T, files map[string]string) (Herdr, string) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		var b []byte
		if strings.HasPrefix(src, "=") {
			b = []byte(src[1:])
		} else {
			b = []byte(readFixture(t, src))
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE_HERDR_DIR", dir)
	bin, err := filepath.Abs("testdata/fake-herdr")
	if err != nil {
		t.Fatal(err)
	}
	return Herdr{Bin: bin}, dir
}

func TestPromptObservesOnlyNewNonce(t *testing.T) {
	h, dir := fakeHerdr(t, map[string]string{
		"prompt.json": "herdr/prompt-stalled.json", // stalled is not an error
		"screen.txt":  "screens/old-prompt.txt",    // carries [agentbus#abc123]
	})
	ctx := context.Background()
	text := "[agentbus#def456] probe: 1 message(s) waiting for kimi."
	observed, err := h.Prompt(ctx, "w4:p1", text, "def456")
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if observed {
		t.Fatal("an old [agentbus#abc123] line counted as observing the new prompt")
	}
	sent, _ := os.ReadFile(filepath.Join(dir, "prompted"))
	if strings.TrimSpace(string(sent)) != text {
		t.Fatalf("prompted %q; want %q", sent, text)
	}
	// Same old screen, but this time the new line reaches it.
	if err := os.WriteFile(filepath.Join(dir, "echo"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	observed, err = h.Prompt(ctx, "w4:p1", "[agentbus#0a1b2c] probe", "0a1b2c")
	if err != nil || !observed {
		t.Fatalf("new nonce on screen: observed=%v err=%v", observed, err)
	}
}

func TestPromptFailureIsAnError(t *testing.T) {
	h, _ := fakeHerdr(t, map[string]string{
		"prompt.json": "={\"error\":{\"code\":\"agent_blocked\",\"message\":\"blocked\"}}\n",
		"screen.txt":  "screens/kimi-empty.txt",
	})
	if _, err := h.Prompt(context.Background(), "w4:p1", "[agentbus#aaaaaa] x", "aaaaaa"); err == nil {
		t.Fatal("a rejected prompt returned no error")
	}
}

func TestInspectReady(t *testing.T) {
	h, _ := fakeHerdr(t, map[string]string{
		"agent-get.json":    "herdr/agent-get-kimi.json",
		"process-info.json": "=" + `{"result":{"process_info":{"foreground_processes":[{"pid":` + strconv.Itoa(os.Getpid()) + `}]}}}`,
		"screen.txt":        "screens/kimi-empty.txt",
	})
	in := h.Inspect(context.Background(), "w4:p1")
	if in.Err != nil || in.Gone {
		t.Fatalf("Inspect: %+v", in)
	}
	if in.State != "idle" || in.Client != "kimi" || in.Focused || !in.Ready || in.Busy {
		t.Fatalf("Inspect = %+v", in)
	}
	start, _ := ProcStart(os.Getpid())
	if len(in.PIDs) != 1 || in.PIDs[0] != os.Getpid() || in.Starts[os.Getpid()] != start {
		t.Fatalf("pids %v starts %v; want self with start %d", in.PIDs, in.Starts, start)
	}
}

// Two reads that are each Ready but differ must not be Ready: the settle
// comparison is the only thing that can say no. The identical-screens control
// proves it.
func TestInspectUnsettledNotReady(t *testing.T) {
	for _, s := range []string{"kimi-empty", "old-prompt"} {
		if ready, _ := Signatures["kimi"].match(readFixture(t, "screens/"+s+".txt")); !ready {
			t.Fatalf("precondition: %s is not Ready on its own", s)
		}
	}
	inspect := func(second string) Inspection {
		h, _ := fakeHerdr(t, map[string]string{
			"agent-get.json":    "herdr/agent-get-kimi.json",
			"process-info.json": "herdr/process-info-kimi.json",
			"screen1.txt":       "screens/kimi-empty.txt",
			"screen2.txt":       "screens/" + second + ".txt",
		})
		return h.Inspect(context.Background(), "w4:p1")
	}
	if in := inspect("kimi-empty"); in.Err != nil || !in.Ready {
		t.Fatalf("identical settled reads: Ready=%v err=%v; want Ready", in.Ready, in.Err)
	}
	if in := inspect("old-prompt"); in.Err != nil || in.Ready {
		t.Fatalf("two different Ready reads gave Ready=%v err=%v", in.Ready, in.Err)
	}
}

func TestInspectGone(t *testing.T) {
	h, _ := fakeHerdr(t, map[string]string{
		"agent-get.json":    "herdr/agent-get-notfound.json",
		"process-info.json": "herdr/process-info-notfound.json",
		"read.json":         "herdr/read-notfound.json",
	})
	in := h.Inspect(context.Background(), "w99:p9")
	if !in.Gone || in.Err != nil || in.Ready {
		t.Fatalf("unknown pane: %+v", in)
	}
}

func TestInspectUnsupportedClientNeverReady(t *testing.T) {
	get := strings.Replace(readFixture(t, "herdr/agent-get-kimi.json"), `"agent":"kimi"`, `"agent":"gemini"`, 1)
	h, _ := fakeHerdr(t, map[string]string{
		"agent-get.json":    "=" + get,
		"process-info.json": "herdr/process-info-kimi.json",
		"screen.txt":        "screens/kimi-empty.txt",
	})
	in := h.Inspect(context.Background(), "w4:p1")
	if in.Client != "gemini" || in.Ready {
		t.Fatalf("unsupported client: %+v", in)
	}
}

func TestForegroundPIDs(t *testing.T) {
	h, _ := fakeHerdr(t, map[string]string{
		"process-info.json": "herdr/process-info-kimi.json",
		"pane-get.json":     "herdr/pane-get-kimi.json",
	})
	pids, term, err := h.ForegroundPIDs(context.Background(), "w4:p1")
	if err != nil || len(pids) != 1 || pids[0] != 1983859 || term != "term_65d790de0b4604" {
		t.Fatalf("ForegroundPIDs = %v %q %v", pids, term, err)
	}
}

func TestParseProcessInfoProcs(t *testing.T) {
	procs, err := parseProcessInfoProcs([]byte(readFixture(t, "herdr/process-info-codex.json")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var node *Proc
	for i := range procs {
		if procs[i].PID == 1930926 {
			node = &procs[i]
		}
	}
	if node == nil || node.Name != "MainThread" || node.Cmdline != "node /home/user/.nvm/versions/node/v24.10.0/bin/codex" {
		t.Fatalf("procs = %+v, want the codex node process with its command line", procs)
	}
	if len(procs) != 4 {
		t.Fatalf("got %d procs, want 4", len(procs))
	}
	if _, err := parseProcessInfoProcs([]byte(readFixture(t, "herdr/process-info-notfound.json"))); err == nil {
		t.Fatal("a herdr error must not parse as an empty list")
	}
}

// A10: a client's own pre-session dialog (Claude Code's trust check, captured
// live in a probe pane: screens/claude-trust.txt) is reported by herdr as
// blocked, so the observe pass reads it as a prompt like any other menu.
func TestParseAgentGetTrustDialogIsBlocked(t *testing.T) {
	a, err := parseAgentGet([]byte(readFixture(t, "herdr/agent-get-claude-trust.json")))
	if err != nil || a.Agent != "claude" || a.Status != "blocked" {
		t.Fatalf("trust dialog: %+v %v", a, err)
	}
	if s := readFixture(t, "screens/claude-trust.txt"); !strings.Contains(s, "Yes, I trust this folder") {
		t.Fatal("the trust screen fixture lost its dialog")
	}
}
