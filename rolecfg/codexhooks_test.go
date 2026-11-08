package rolecfg

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Codex reads each -c value as TOML and takes one that does not parse as a
// raw string, silently: every turn hook must come back as the command given,
// whatever the agentbus path holds (a space, a quote, a backslash, a tab,
// an escape character and DEL, which TOML refuses raw, non-ASCII, a character beyond
// the BMP that JSON would write as a surrogate pair).
func TestCodexHookArgsAreTOML(t *testing.T) {
	for _, bin := range []string{"/usr/local/bin/agentbus", "/opt/agent bus/agentbus", `/tmp/a"b\c/agentbus`, "/tmp/tab\there/agentbus", "/tmp/esc\x1b/agentbus", "/tmp/del\x7f/agentbus", "/home/zoé/agentbus", "/tmp/\U0001F600/agentbus"} {
		h := claudeHooks{TurnStart: ShellJoin([]string{bin, "roles", "turn", "start"}), TurnEnd: ShellJoin([]string{bin, "roles", "turn", "end"})}
		args := codexHookArgs(h)
		if len(args) != 8 || args[0] != "-c" || args[2] != "-c" || args[4] != "-c" || args[6] != "-c" {
			t.Fatalf("%q: args %q", bin, args)
		}
		var got struct {
			Features struct{ Hooks bool }
			Hooks    map[string][]struct {
				Hooks []map[string]any
			}
		}
		for _, v := range []string{args[1], args[3], args[5], args[7]} {
			if _, err := toml.Decode(v, &got); err != nil {
				t.Fatalf("%q: %s does not parse: %v", bin, v, err)
			}
		}
		if !got.Features.Hooks {
			t.Errorf("%q: features.hooks not on", bin)
		}
		// codex-identity-recovery: SessionStart (startup, resume) ends a turn,
		// which proves the new process's identity, as Claude Code's does.
		for event, want := range map[string]string{"UserPromptSubmit": h.TurnStart, "Stop": h.TurnEnd, "SessionStart": h.TurnEnd} {
			m := got.Hooks[event]
			if len(m) != 1 || len(m[0].Hooks) != 1 || m[0].Hooks[0]["type"] != "command" || m[0].Hooks[0]["command"] != want || len(m[0].Hooks[0]) != 2 {
				t.Errorf("%q: %s = %+v, want the one command %q", bin, event, m, want)
			}
		}
		if len(got.Hooks) != 3 {
			t.Errorf("%q: hooks %v, want only UserPromptSubmit, Stop and SessionStart", bin, got.Hooks)
		}
		if strings.Contains(strings.Join(args, " "), `\ud`) {
			t.Errorf("%q: a surrogate escape: %q", bin, args)
		}
	}
}

// The dry run shows the turn hooks byte for byte, each value quoted for the
// shell; Kimi, Grok and Claude carry no Codex flag (their own routes come
// with their trials).
func TestCodexDryRunCarriesTheTurnHooks(t *testing.T) {
	f := newLaunchFixture(t)
	f.env = append(f.env, "AGENTBUS_BIN=/opt/bus/agentbus")
	m := f.config("codex", "", "", "")
	out, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1")
	want := `codex --model test-model -c 'model_provider="test-provider"' --sandbox read-only --ask-for-approval on-request` +
		` -c features.hooks=true` +
		` -c 'hooks.UserPromptSubmit=[{hooks=[{type="command",command="/opt/bus/agentbus roles turn start"}]}]'` +
		` -c 'hooks.Stop=[{hooks=[{type="command",command="/opt/bus/agentbus roles turn end"}]}]'` +
		` -c 'hooks.SessionStart=[{hooks=[{type="command",command="/opt/bus/agentbus roles turn end"}]}]'` +
		" '<role prompt and skill paths>'\n"
	if err != nil || !strings.Contains(out, want) {
		t.Fatalf("dry run = %q %v, want the line %q", out, err, want)
	}
	for _, c := range []string{"claude-code", "kimi-cli", "grok"} {
		out, _, err := f.launch(f.config(c, "", "", ""), "coder", "AGENT_LAUNCH_DRYRUN=1")
		if err != nil || strings.Contains(out, "features.hooks") || strings.Contains(out, "hooks.Stop") {
			t.Errorf("%s dry run carries a Codex hook flag: %q %v", c, out, err)
		}
	}
}
