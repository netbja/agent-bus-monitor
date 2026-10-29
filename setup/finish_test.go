package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestClients(t *testing.T) {
	in, out, _, _ := skillsWorld(t, nil, map[string]string{"claude": "/b/claude"})
	in.Clients()
	want := "\n==> 5/5 Scanning installed AI coding agents\n" +
		"    ✓ claude: available (/b/claude)\n" +
		"    • codex: not installed (optional)\n" +
		"    • kimi: not installed (optional)\n" +
		"    • grok: not installed (optional)\n"
	if out.String() != want {
		t.Fatalf("%q", out.String())
	}
	// installer-no-client: none at all is a warning, never a failure, with each
	// client's install pointer and what works meanwhile.
	in2, out2, _, _ := skillsWorld(t, nil, nil)
	in2.Clients()
	got := out2.String()
	for _, w := range []string{
		"    ! No agent CLI on PATH: agent-bus starts its agents in one of these, so install at least one:\n",
		"      Claude Code (claude): https://code.claude.com/docs/en/setup\n",
		"      Codex CLI (codex): https://github.com/openai/codex\n",
		"      Kimi Code CLI (kimi): https://github.com/MoonshotAI/kimi-code\n",
		"      Grok Build (xAI, beta) (grok): https://x.ai/cli\n",
		"    • Meanwhile the bus and busmon work. Once one is installed: agentbus up <project> <path>\n",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if in2.O.Warnings != 1 {
		t.Errorf("%d warnings, want one", in2.O.Warnings)
	}
}

// The name on the bus: asked once on a terminal when none is set; skipped
// by --yes, a dry run, no terminal or AGENTBUS_SETUP_NO_IDENTITY=1.
func TestIdentity(t *testing.T) {
	ctx := context.Background()
	in, out, _, home := skillsWorld(t, nil, nil)
	ab := home + "/bin/agentbus"
	_ = os.MkdirAll(home+"/bin", 0o755)
	_ = os.WriteFile(ab, []byte("#!/bin/sh\n"), 0o755)
	f := in.R.(*fakeRunner)
	f.cmds[ab+" identity"] = fakeCmd{err: errors.New("none")}
	f.cmds[ab+" identity set charlie"] = fakeCmd{}
	var shown bytes.Buffer
	in.Identity(ctx, &Prompter{TTY: fakeTTY{strings.NewReader("charlie\n"), &shown}})
	if shown.String() != "    ? Your name on the bus (lowercase, e.g. charlie; Enter to skip): " ||
		out.String() != "    ✓ busmon and agentbus up will know you as charlie\n" {
		t.Fatalf("%q %q", shown.String(), out.String())
	}
	for name, set := range map[string]func(){
		"--yes":       func() { in.Opt.Yes = true },
		"dry run":     func() { in.Opt.DryRun = true },
		"no identity": func() { f.cmds[ab+" identity"] = fakeCmd{} }, // one is set
	} {
		in.Opt.Yes, in.Opt.DryRun = false, false
		f.cmds[ab+" identity"] = fakeCmd{err: errors.New("none")}
		set()
		before := len(f.ran)
		in.Identity(ctx, &Prompter{TTY: fakeTTY{strings.NewReader("x\n"), &bytes.Buffer{}}})
		for _, c := range f.ran[before:] {
			if strings.Contains(c, "identity set") {
				t.Errorf("%s: set an identity", name)
			}
		}
	}
	in.Opt.Yes, in.Opt.DryRun = false, false
	in.Identity(ctx, &Prompter{}) // no terminal: nothing asked, nothing fatal
}

// The model table: refreshed through the installed agentbus; skipped by
// AGENTBUS_SETUP_NO_MODELS=1; a dry run says so.
func TestModels(t *testing.T) {
	ctx := context.Background()
	in, out, _, home := skillsWorld(t, nil, nil)
	ab := home + "/bin/agentbus"
	_ = os.MkdirAll(home+"/bin", 0o755)
	_ = os.WriteFile(ab, []byte("#!/bin/sh\n"), 0o755)
	in.R.(*fakeRunner).cmds[ab+" models --refresh"] = fakeCmd{err: errors.New("x"), out: "a\ncodex: catalogue unreadable\n"}
	in.Models(ctx)
	if out.String() != "    ! model table not refreshed: codex: catalogue unreadable\n" {
		t.Fatalf("%q", out.String())
	}
	in.R.(*fakeRunner).cmds[ab+" models --refresh"] = fakeCmd{}
	out.Reset()
	in.Models(ctx)
	if out.String() != "    ✓ model table refreshed (agentbus models)\n" {
		t.Fatalf("%q", out.String())
	}
	in.Getenv = func(k string) string { return map[string]string{"HOME": home, "AGENTBUS_SETUP_NO_MODELS": "1"}[k] }
	out.Reset()
	in.Models(ctx)
	if out.String() != "" {
		t.Fatalf("skipped: %q", out.String())
	}
	in.Opt.DryRun = true
	in.Models(ctx)
	if out.String() != "    • [dry-run] would refresh the model table (agentbus models --refresh)\n" {
		t.Fatalf("dry: %q", out.String())
	}
}

// The summary box and the next steps, as setup.sh prints them.
func TestSummary(t *testing.T) {
	var out bytes.Buffer
	o := NewOutput(&out, &bytes.Buffer{}, false)
	o.Summary(Options{}, 0, "v9.9.9")
	bar := "================================================================="
	want := "\n" + bar + "\n Installation complete (v9.9.9) \n" + bar + "\n\n" +
		"Next steps:\n" +
		"  1. Start herdr (a named session per project is handy):\n" +
		"     herdr --session myproject\n\n" +
		"  2. Inside it, provision and open the Agent Bus team in one command:\n" +
		"     agentbus up myproject /path/to/your/codebase --as <your-name>\n" +
		"     (busmon opens first; later, reopen any project with Ctrl+B then Up)\n\n" +
		"  Keep it current: ./setup.sh update · check it: ./setup.sh status · remove it: ./setup.sh uninstall\n\n"
	if out.String() != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out.String(), want)
	}
	out.Reset()
	o.Warnings = 2
	if code := o.Summary(Options{}, 0, "v"); code != 0 || !strings.Contains(out.String(), " Installation complete with 2 warning(s) — see above \n") {
		t.Fatalf("%q", out.String())
	}
	out.Reset()
	if code := o.Summary(Options{DryRun: true}, 1, "v"); code != 1 || !strings.Contains(out.String(), " Dry run complete — nothing was changed (1 missing, 2 warning(s)) \n") || strings.Contains(out.String(), "Next steps") {
		t.Fatalf("%d %q", code, out.String())
	}
}
