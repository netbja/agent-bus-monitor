package agentstate

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func readScreen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "screens", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Herdr exit step 2a moved the signatures without changing them: the
// embedded profiles compile to the table dispatch held before, pattern for
// pattern. One change since, on purpose: the architect's ruling of
// 2026-11-13 (herdr exit 5d) anchors claude's, kimi's and grok's Ready to
// the input box's end, so it is the empty-input check too; it can only make
// the dispatcher wake less, never type into text.
func TestProfilesAreTheOldTable(t *testing.T) {
	type sig struct {
		ansi, rule                 bool
		prompt                     string
		ready, busy, modal, dialog []string
	}
	old := map[string]sig{
		"claude": {true, true, `^❯`,
			[]string{`(?m)^(?:\x1b\[[0-9;]*m)*─{3,}.*\n(?:\x1b\[[0-9;]*m)*❯[ \x{a0}]*(?:\x1b\[0m)?(?:\x1b\[2m[^\x1b\n]*\x1b\[0m)?[ \x{a0}]*(?:\x1b\[0m)?\n(?:\x1b\[[0-9;]*m)*─{3,}`},
			[]string{`(?m)^[ \t]*[·✢✳✶✻✽*][ \t]+\S+…`}, nil, []string{`(?s)Accessing workspace:.*Yes, I trust this folder`}},
		"codex": {true, false, `^›`,
			[]string{`(?m)^(?:\x1b\[[0-9;]*m)*›(?:\x1b\[[0-9;]*m| )*\x1b\[2m`}, nil, []string{`enter/esc latest`},
			[]string{`(?s)Hooks need review.*Trust all and continue`}},
		"kimi": {false, false, `^[ \t]*│ >`,
			[]string{`(?m)^[ \t]*│ >[ \t]*(?:│[ \t]*)?\n[ \t]*╰`}, []string{`(?m)^[ \t]*(?:🌑|🌒|🌓|🌔|🌕|🌖|🌗|🌘)(?:[ \t]|·|$)`}, nil, nil},
		"grok": {false, false, `^[ \t]*│ ❯`, []string{`(?m)^[ \t]*│ ❯[ \t]*(?:│[ \t]*)?\n[ \t]*╰`}, nil, nil, nil},
		"agy":  {false, true, `^>(?:[ \t]|$)`, []string{`(?m)^─{3,}[ \t]*\n>[ \t]*\n─{3,}`}, nil, []string{`(?m)^[ \t]*>[ \t]*1\.[ \t]`}, nil},
	}
	strs := func(rs []*regexp.Regexp) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.String())
		}
		return out
	}
	got := Signatures()
	if !slices.Equal(Clients(), []string{"agy", "claude", "codex", "grok", "kimi"}) {
		t.Fatalf("clients %v", Clients())
	}
	for c, w := range old {
		s := got[c]
		if s.ANSI != w.ansi || s.RuleAbove != w.rule || s.Prompt.String() != w.prompt ||
			!slices.Equal(strs(s.Ready), w.ready) || !slices.Equal(strs(s.Busy), w.busy) ||
			!slices.Equal(strs(s.Modal), w.modal) || !slices.Equal(strs(s.Dialog), w.dialog) {
			t.Errorf("%s: %+v differs from the old table", c, s)
		}
	}
}

// TestSignatures runs every client's signature over the screens captured in
// the probes (see testdata/screens/README.md for real vs synthetic).
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
		{"agy", "agy153-empty", true, false}, // agy 1.5.3, the agy-client probe (2026-11-07)
		{"agy", "agy153-typed", false, false},
		{"agy", "agy153-approval", false, false},
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
		// EmptyInput is Ready on the current box (architect's ruling on 5d):
		// the box must be empty to its end, not only its prompt line, and
		// text the client put back in the input is not empty. DERIVED.
		{"claude", "claude-multiline-typed", false, false},
		{"codex", "codex-multiline-typed", false, false},
		{"kimi", "kimi-multiline-typed", false, false},
		{"grok", "grok-multiline-typed", false, false},
		{"agy", "agy-multiline-typed", false, false},
		{"claude", "claude-returned", false, false},
		{"codex", "codex-returned", false, false},
		{"kimi", "kimi-returned", false, false},
		{"grok", "grok-returned", false, false},
		{"agy", "agy-returned", false, false},
	}
	sigs := Signatures()
	for _, c := range cases {
		t.Run(c.screen, func(t *testing.T) {
			sig, ok := sigs[c.client]
			if !ok {
				t.Fatalf("no signature for %s", c.client)
			}
			ready, busy := sig.Match(readScreen(t, c.screen+".txt"))
			if ready != c.ready || busy != c.busy {
				t.Fatalf("%s: ready=%v busy=%v; want ready=%v busy=%v", c.screen, ready, busy, c.ready, c.busy)
			}
		})
	}
	for name, sig := range sigs {
		if sig.Prompt == nil {
			t.Errorf("%s: no Prompt marker; its readiness could match an old input box", name)
		}
		if ready, _ := sig.Match("some output\nmore output\n"); ready { // no input line: never ready
			t.Errorf("%s: ready without any input line", name)
		}
	}
	if _, ok := sigs["gemini"]; ok {
		t.Error("an unprobed client has a profile")
	}
}

// A10: a client's own pre-session dialog (Codex's hooks review, Claude Code's
// trust check) matches; no other fixture does.
func TestDialogSignatures(t *testing.T) {
	sigs := Signatures()
	for _, c := range []struct{ client, screen string }{{"codex", "codex-hooks"}, {"claude", "claude-trust"}} {
		if !sigs[c.client].InDialog(readScreen(t, c.screen+".txt")) {
			t.Errorf("%s: %s not seen as a dialog", c.client, c.screen)
		}
	}
	entries, err := os.ReadDir("testdata/screens")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".txt")
		if name == "codex-hooks" || name == "claude-trust" || name == "README.md" ||
			strings.HasSuffix(name, "-then-ready") || strings.Contains(name, "-accepted") {
			continue
		}
		client := strings.SplitN(name, "-", 2)[0]
		if sig, ok := sigs[client]; ok && sig.InDialog(readScreen(t, e.Name())) {
			t.Errorf("%s reads as a dialog", e.Name())
		}
	}
	// The anchor: the dialog's lines far above the bottom do not count.
	if sigs["codex"].InDialog(readScreen(t, "codex-hooks.txt") + strings.Repeat("some later line\n", 20)) {
		t.Error("dialog text 20 lines above the bottom read as a dialog")
	}
}

// A profile is refused when it names another client, holds an unknown key,
// has no prompt marker or a pattern that does not compile.
func TestParseProfileRefuses(t *testing.T) {
	good := "client = \"x\"\n[signature]\nprompt = '^>'\nready = ['>']\n"
	if _, err := parseProfile("x.toml", []byte(good)); err != nil {
		t.Fatalf("good profile: %v", err)
	}
	for name, c := range map[string]struct{ file, text string }{
		"another client": {"y.toml", good},
		"unknown key":    {"x.toml", good + "colour = 'x'\n"},
		"no prompt":      {"x.toml", "client = \"x\"\n[signature]\nready = ['>']\n"},
		"bad pattern":    {"x.toml", "client = \"x\"\n[signature]\nprompt = '^>'\nready = ['(']\n"},
	} {
		if _, err := parseProfile(c.file, []byte(c.text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// EmptyInput (spec 4.2 step 1, the architect's ruling): the current input
// box is empty to its end, with the Modal and Dialog vetoes; InputHolds:
// the current input region holds a token (the wake line's agentbus#<nonce>,
// or /clear), never an older box in scrollback.
func TestEmptyInputAndInputHolds(t *testing.T) {
	sigs := Signatures()
	for _, c := range []string{"claude", "codex", "kimi", "grok", "agy"} {
		sig := sigs[c]
		empty := readScreen(t, c+"-empty.txt")
		if !sig.EmptyInput(empty) {
			t.Errorf("%s: its empty fixture reads not empty", c)
		}
		for _, f := range []string{"typed", "multiline-typed", "returned", "old-empty-typed"} {
			if sig.EmptyInput(readScreen(t, c+"-"+f+".txt")) {
				t.Errorf("%s-%s reads empty", c, f)
			}
		}
		typed := readScreen(t, c+"-typed.txt")
		woken := strings.Replace(typed, "zz-probe", "[agentbus#a1b2c3] demo: 2 message(s)", 1)
		if !sig.InputHolds(woken, "agentbus#a1b2c3") || sig.InputHolds(woken, "agentbus#ffffff") {
			t.Errorf("%s: the wake token in its input not read", c)
		}
		if !sig.InputHolds(strings.Replace(typed, "zz-probe", "/clear", 1), "/clear") {
			t.Errorf("%s: /clear in its input not read", c)
		}
		if sig.InputHolds(empty, "agentbus#a1b2c3") || sig.InputHolds(typed, "") {
			t.Errorf("%s: an empty input or an empty token holds", c)
		}
		// An older box in scrollback holding the token is not the input.
		old := strings.Replace(readScreen(t, c+"-old-typed-empty.txt"), "zz-probe", "agentbus#a1b2c3", 1)
		if !strings.Contains(old, "agentbus#a1b2c3") || sig.InputHolds(old, "agentbus#a1b2c3") {
			t.Errorf("%s: an old box's token read as the input's", c)
		}
	}
	// A token wrapped inside a bordered box is still read (a long wake line
	// wraps; the border and the blanks are not the token).
	wrapped := " ╭──────────╮\n │ > [agentbus#a1b │\n │   2c3] demo  │\n ╰──────────╯\n"
	if !sigs["kimi"].InputHolds(wrapped, "agentbus#a1b2c3") {
		t.Error("a wrapped token not read")
	}
	if sigs["claude"].EmptyInput(readScreen(t, "claude-trust.txt")) {
		t.Error("a dialog reads as an empty input")
	}
	// The Dialog veto on its own: an empty box Ready matches, under a dialog.
	empty := readScreen(t, "codex-empty.txt")
	lines := strings.Split(strings.TrimRight(empty, "\n"), "\n")
	at := len(lines) - 4
	withDialog := strings.Join(append(append(append([]string{}, lines[:at]...), "Hooks need review", "Trust all and continue"), lines[at:]...), "\n")
	if ready, _ := sigs["codex"].Match(withDialog); !ready || sigs["codex"].EmptyInput(withDialog) {
		t.Errorf("a dialog under an empty box: ready %v, reads empty %v", ready, sigs["codex"].EmptyInput(withDialog))
	}
}
