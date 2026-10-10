package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Runtime is how the dispatcher sees and touches an agent's terminal. herdr is
// the only one in v0.8; it is the only file that knows herdr exists.
type Runtime interface {
	Inspect(ctx context.Context, pane string) Inspection // errors in Inspection.Err
	Prompt(ctx context.Context, pane, text, nonce string) (observed bool, err error)
}

const (
	herdrCallTimeout = 3 * time.Second        // every herdr call, prompt included
	settleDelay      = 200 * time.Millisecond // two reads this far apart must agree
	observeWait      = 5 * time.Second        // wait-output for the nonce line
	screenLines      = "40"
)

// Signature recognises one client's screen. Ready: any regex matches the input
// line as it looks when empty and submittable. Busy: any regex matches a
// running turn the client's herdr state may not report. Modal: any regex
// matches a state where Enter would not submit (it vetoes Ready). Ready is
// matched on the ANSI read when ANSI is set (some empty inputs differ from
// typed ones only by their dim placeholder); Busy and Modal always match the
// screen with escapes stripped.
type Signature struct {
	Ready, Busy []*regexp.Regexp
	Modal       []*regexp.Regexp
	ANSI        bool
}

var res = regexp.MustCompile

// Signatures holds only clients probed live (spec §10); a client without an
// entry is unsupported and never prompted. Fixtures: testdata/screens.
var Signatures = map[string]Signature{
	// Empty input is `❯` + NBSP (optionally a dim `Try "…"` placeholder;
	// the glyph itself turns grey during a turn)
	// directly under a `─` rule; typed text is not dim. A turn keeps the
	// input box empty, so the spinner line (`✶ Kneading… (2s · ↓ 91 tokens)`)
	// is the busy signature.
	"claude": {
		Ready: []*regexp.Regexp{res(`(?m)^(?:\x1b\[[0-9;]*m)*─{3,}.*\n(?:\x1b\[[0-9;]*m)*❯[ \x{a0}]*(?:\x1b\[0m)?(?:\x1b\[2m[^\x1b\n]*\x1b\[0m)?[ \x{a0}]*(?:\x1b\[0m)?$`)},
		Busy:  []*regexp.Regexp{res(`(?m)^[ \t]*[·✢✳✶✻✽*][ \t]+\S+…`)},
		ANSI:  true,
	},
	// `›` followed only by styling and spaces, then the dim placeholder.
	// Transcript-scroll mode (footer `enter/esc latest`) does not submit on Enter.
	"codex": {
		Ready: []*regexp.Regexp{res(`(?m)^(?:\x1b\[[0-9;]*m)*›(?:\x1b\[[0-9;]*m| )*\x1b\[2m`)},
		Modal: []*regexp.Regexp{res(`enter/esc latest`)},
		ANSI:  true,
	},
	// The input box has a right border on screen: `│ >   …   │`.
	// Kimi 2.1 reports idle/done while its moon spinner runs (`🌑 · Tip: …`).
	"kimi": {
		Ready: []*regexp.Regexp{res(`(?m)^[ \t]*│ >[ \t]*(?:│[ \t]*)?$`)},
		Busy:  []*regexp.Regexp{res(`(?m)^[ \t]*(?:🌑|🌒|🌓|🌔|🌕|🌖|🌗|🌘)(?:[ \t]|·|$)`)},
	},
	"grok": {
		Ready: []*regexp.Regexp{res(`(?m)^[ \t]*│ ❯[ \t]*(?:│[ \t]*)?$`)},
	},
	// `>` alone between two `─` rules; the approval menu shows `> 1. Yes…`.
	"agy": {
		Ready: []*regexp.Regexp{res(`(?m)^─{3,}[ \t]*\n>[ \t]*\n─{3,}`)},
		Modal: []*regexp.Regexp{res(`(?m)^[ \t]*>[ \t]*1\.[ \t]`)},
	},
}

// Supported is the set of clients the dispatcher may prompt.
func Supported() map[string]bool {
	m := make(map[string]bool, len(Signatures))
	for k := range Signatures {
		m[k] = true
	}
	return m
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func anyMatch(rs []*regexp.Regexp, s string) bool {
	for _, r := range rs {
		if r.MatchString(s) {
			return true
		}
	}
	return false
}

// match judges one screen read.
func (s Signature) match(screen string) (ready, busy bool) {
	screen = strings.ReplaceAll(screen, "\r", "")
	plain := ansiEscape.ReplaceAllString(screen, "")
	on := plain
	if s.ANSI {
		on = screen
	}
	ready = anyMatch(s.Ready, on) && !anyMatch(s.Modal, plain)
	return ready, anyMatch(s.Busy, plain)
}

// Herdr drives panes through the herdr CLI. Bin "" means "herdr" on PATH.
type Herdr struct{ Bin string }

var _ Runtime = Herdr{}

// herdrError is herdr's JSON error object ({"error":{"code","message"}}).
type herdrError struct{ Code, Message string }

func (e *herdrError) Error() string { return "herdr: " + e.Code + ": " + e.Message }

func errCode(err error) string {
	var he *herdrError
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}

// isGone: herdr no longer knows the pane, or no agent runs in it.
func isGone(err error) bool {
	c := errCode(err)
	return c == "pane_not_found" || c == "agent_not_found"
}

// asHerdrError recognises herdr's error object.
func asHerdrError(b []byte) error {
	b = bytes.TrimSpace(b)
	if !bytes.HasPrefix(b, []byte(`{"error"`)) {
		return nil
	}
	var v struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if json.Unmarshal(b, &v) != nil || v.Error.Code == "" {
		return nil
	}
	return &herdrError{Code: v.Error.Code, Message: v.Error.Message}
}

func (h Herdr) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	bin := h.Bin
	if bin == "" {
		bin = "herdr"
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if he := asHerdrError(stderr.Bytes()); he != nil {
			return nil, he
		}
		if he := asHerdrError(stdout.Bytes()); he != nil {
			return nil, he
		}
		return nil, fmt.Errorf("herdr %s: %w: %s", strings.Join(args[:min(2, len(args))], " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

type agentInfo struct {
	Agent    string `json:"agent"`
	Status   string `json:"agent_status"`
	Focused  bool   `json:"focused"`
	Terminal string `json:"terminal_id"`
}

func parseAgentGet(b []byte) (agentInfo, error) {
	if he := asHerdrError(b); he != nil {
		return agentInfo{}, he
	}
	var v struct {
		Result struct {
			Agent *agentInfo `json:"agent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return agentInfo{}, fmt.Errorf("herdr agent get: %w", err)
	}
	if v.Result.Agent == nil {
		return agentInfo{}, fmt.Errorf("herdr agent get: no agent in reply")
	}
	return *v.Result.Agent, nil
}

func parseProcessInfo(b []byte) ([]int, error) {
	if he := asHerdrError(b); he != nil {
		return nil, he
	}
	var v struct {
		Result struct {
			ProcessInfo *struct {
				Foreground []struct {
					PID int `json:"pid"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("herdr process-info: %w", err)
	}
	if v.Result.ProcessInfo == nil {
		return nil, fmt.Errorf("herdr process-info: no process_info in reply")
	}
	var pids []int
	for _, p := range v.Result.ProcessInfo.Foreground {
		if p.PID > 0 {
			pids = append(pids, p.PID)
		}
	}
	return pids, nil
}

func (h Herdr) processPIDs(ctx context.Context, pane string) ([]int, error) {
	out, err := h.run(ctx, herdrCallTimeout, "pane", "process-info", "--pane", pane)
	if err != nil {
		return nil, err
	}
	return parseProcessInfo(out)
}

// ForegroundPIDs lists the pane's foreground processes and its herdr terminal
// id. The terminal id is display-only and best-effort ("" if unavailable).
func (h Herdr) ForegroundPIDs(ctx context.Context, pane string) ([]int, string, error) {
	pids, err := h.processPIDs(ctx, pane)
	if err != nil {
		return nil, "", err
	}
	terminal := ""
	if out, err := h.run(ctx, herdrCallTimeout, "pane", "get", pane); err == nil {
		var v struct {
			Result struct {
				Pane struct {
					Terminal string `json:"terminal_id"`
				} `json:"pane"`
			} `json:"result"`
		}
		if json.Unmarshal(out, &v) == nil {
			terminal = v.Result.Pane.Terminal
		}
	}
	return pids, terminal, nil
}

func (h Herdr) read(ctx context.Context, pane string, ansi bool) (string, error) {
	args := []string{"pane", "read", pane, "--source", "recent", "--lines", screenLines}
	if ansi {
		args = append(args, "--ansi")
	}
	out, err := h.run(ctx, herdrCallTimeout, args...)
	return string(out), err
}

// failed records an inspection error: an unknown pane or agent is Gone.
func failed(in Inspection, err error) Inspection {
	if isGone(err) {
		in.Gone = true
	} else {
		in.Err = err
	}
	in.Ready = false
	return in
}

// Inspect reports the pane's herdr state, foreground processes (with start
// times) and, for a supported client, whether its screen is ready: two reads
// settleDelay apart must be identical, so a screen still rendering is never
// judged ready.
func (h Herdr) Inspect(ctx context.Context, pane string) Inspection {
	var in Inspection
	out, err := h.run(ctx, herdrCallTimeout, "agent", "get", pane)
	var a agentInfo
	if err == nil {
		a, err = parseAgentGet(out)
	}
	if err != nil {
		return failed(in, err)
	}
	in.State, in.Client, in.Focused = a.Status, a.Agent, a.Focused
	if in.State == "" {
		in.State = "unknown"
	}

	pids, err := h.processPIDs(ctx, pane)
	if err != nil {
		return failed(in, err)
	}
	in.PIDs = pids
	in.Starts = make(map[int]uint64, len(pids))
	for _, pid := range pids {
		if s, err := ProcStart(pid); err == nil {
			in.Starts[pid] = s
		}
	}

	sig, ok := Signatures[a.Agent]
	if !ok {
		return in // unsupported: never ready
	}
	first, err := h.read(ctx, pane, sig.ANSI)
	if err != nil {
		return failed(in, err)
	}
	select {
	case <-ctx.Done():
		return failed(in, ctx.Err())
	case <-time.After(settleDelay):
	}
	second, err := h.read(ctx, pane, sig.ANSI)
	if err != nil {
		return failed(in, err)
	}
	r1, b1 := sig.match(first)
	r2, b2 := sig.match(second)
	in.Busy = b1 || b2
	in.Ready = first == second && r1 && r2
	return in
}

// Prompt submits text (which must carry "[agentbus#<nonce>]") and waits for
// that exact marker to appear on the pane. Observed means the line reached the
// screen, not that the agent took the work. herdr's agent_prompt_stalled is
// not an error: it fired for Kimi on a successful submission (spec §10).
func (h Herdr) Prompt(ctx context.Context, pane, text, nonce string) (bool, error) {
	marker := "[agentbus#" + nonce + "]"
	if nonce == "" || !strings.Contains(text, marker) {
		return false, fmt.Errorf("prompt text does not carry %s", marker)
	}
	if _, err := h.run(ctx, herdrCallTimeout, "agent", "prompt", pane, text); err != nil && errCode(err) != "agent_prompt_stalled" {
		return false, err
	}
	_, err := h.run(ctx, observeWait+time.Second, "pane", "wait-output", pane,
		"--match", marker, "--timeout", fmt.Sprint(observeWait.Milliseconds()))
	switch {
	case err == nil:
		return true, nil
	case errCode(err) == "timeout":
		return false, nil
	}
	return false, err
}
