package dispatch

import (
	"context"
	"github.com/netbja/agent-bus-monitor/termhost"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func readFixture(t *testing.T, rel string) string {
	t.Helper()
	dir := "testdata"
	if strings.HasPrefix(rel, "screens/") { // the screens moved with the judgement (herdr exit step 2a)
		dir = filepath.Join("..", "agentstate", "testdata")
	}
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
	line, _ := termhost.WakeLine("probe", "kimi", 1, "def456")
	text := line.Text()
	observed, err := h.Prompt(ctx, "w4:p1", line)
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
	line, _ = termhost.WakeLine("probe", "kimi", 1, "0a1b2c")
	observed, err = h.Prompt(ctx, "w4:p1", line)
	if err != nil || !observed {
		t.Fatalf("new nonce on screen: observed=%v err=%v", observed, err)
	}
}

func TestPromptFailureIsAnError(t *testing.T) {
	h, _ := fakeHerdr(t, map[string]string{
		"prompt.json": "={\"error\":{\"code\":\"agent_blocked\",\"message\":\"blocked\"}}\n",
		"screen.txt":  "screens/kimi-empty.txt",
	})
	line, _ := termhost.WakeLine("probe", "kimi", 1, "aaaaaa")
	if _, err := h.Prompt(context.Background(), "w4:p1", line); err == nil {
		t.Fatal("a rejected prompt returned no error")
	}
}

// Prompt types a wake line only: /clear goes through Clear, and the zero
// Line never reaches herdr.
func TestPromptRefusesAnythingButAWakeLine(t *testing.T) {
	h, dir := fakeHerdr(t, map[string]string{"prompt.json": "herdr/prompt-stalled.json", "screen.txt": "screens/kimi-empty.txt"})
	for _, l := range []termhost.Line{{}, termhost.ClearLine()} {
		if _, err := h.Prompt(context.Background(), "w4:p1", l); err == nil {
			t.Errorf("kind %v: no error", l.Kind())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "prompted")); err == nil {
		t.Fatal("herdr was prompted")
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
		if ready, _ := Signatures["kimi"].Match(readFixture(t, "screens/"+s+".txt")); !ready {
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

func TestInspectCodexHooksDialogIsBlocked(t *testing.T) {
	h, _ := fakeHerdr(t, map[string]string{
		"agent-get.json":    "=" + `{"result":{"agent":{"agent":"codex","agent_status":"idle","focused":false,"pane_id":"w2:pE","terminal_id":"t"},"type":"agent_info"}}`,
		"process-info.json": "=" + `{"result":{"process_info":{"foreground_processes":[{"pid":` + strconv.Itoa(os.Getpid()) + `}]}}}`,
		"screen.txt":        "screens/codex-hooks.txt",
	})
	in := h.Inspect(context.Background(), "w2:pE")
	if in.Err != nil || in.State != "blocked" || in.Ready {
		t.Fatalf("Inspect = %+v, want blocked and not ready", in)
	}
}

// Review (foureyes): the dialog's words on screen are not the dialog. With
// its text above a ready prompt (Claude Code's trust accepted, Codex's hooks
// review accepted, or a review quoting them), the pane reads ready, never
// blocked: the dialog counts only near the bottom and when not ready.
func TestDialogTextAboveAReadyPromptIsNotADialog(t *testing.T) {
	for _, c := range []struct{ client, agentGet, screen string }{
		{"claude", "herdr/agent-get-claude.json", "claude-trust-then-ready"},
		{"codex", "=" + `{"result":{"agent":{"agent":"codex","agent_status":"idle","focused":false,"pane_id":"w2:pE","terminal_id":"t"},"type":"agent_info"}}`, "codex-hooks-accepted"},
	} {
		h, _ := fakeHerdr(t, map[string]string{
			"agent-get.json":    c.agentGet,
			"process-info.json": "=" + `{"result":{"process_info":{"foreground_processes":[{"pid":` + strconv.Itoa(os.Getpid()) + `}]}}}`,
			"screen.txt":        "screens/" + c.screen + ".txt",
		})
		in := h.Inspect(context.Background(), "w2:pE")
		if in.Err != nil || in.State == "blocked" || !in.Ready {
			t.Errorf("%s: Inspect = %+v, want ready and not blocked", c.screen, in)
		}
	}
	// The ready guard alone: the dialog's lines right above Codex's ready input
	// box (just accepted) are inside the window, yet the pane reads ready.
	close := readFixture(t, "screens/codex-hooks-accepted-close.txt")
	if !Signatures["codex"].InDialog(close) {
		t.Fatal("precondition: the close fixture's dialog lines are inside the window")
	}
	h, _ := fakeHerdr(t, map[string]string{
		"agent-get.json":    "=" + `{"result":{"agent":{"agent":"codex","agent_status":"idle","focused":false,"pane_id":"w2:pE","terminal_id":"t"},"type":"agent_info"}}`,
		"process-info.json": "=" + `{"result":{"process_info":{"foreground_processes":[{"pid":` + strconv.Itoa(os.Getpid()) + `}]}}}`,
		"screen.txt":        "screens/codex-hooks-accepted-close.txt",
	})
	if in := h.Inspect(context.Background(), "w2:pE"); in.State == "blocked" || !in.Ready {
		t.Errorf("dialog text just above a ready prompt: Inspect = %+v, want ready", in)
	}
	// The anchor alone: the dialog's lines far above the bottom do not count.
	quoted := readFixture(t, "screens/codex-hooks.txt") + strings.Repeat("some later line\n", 20)
	if Signatures["codex"].InDialog(quoted) {
		t.Error("dialog text 20 lines above the bottom read as a dialog")
	}
}
