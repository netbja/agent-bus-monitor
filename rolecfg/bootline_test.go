package rolecfg

import (
	"strings"
	"testing"
)

// fresh-agent-boot (2026-11-16): a Claude Code agent started on the holder
// (master and sentinel that day) sat at an empty prompt: its role prompt is a
// system prompt (--append-system-prompt), nothing gave it a first turn, so it
// never ran its boot steps, never published presence, and the dispatcher read
// it "agent unknown" until a person typed a boot line into it. The launch now
// gives it BootLine as its first user turn, on the command line: the
// dispatcher still types nothing but its fixed lines.
func TestClaudeLaunchGivesTheBootLine(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "", "", "")
	if _, e, err := f.launch(m, "coder"); err != nil {
		t.Fatal(err, e)
	}
	n := len(f.execed)
	if n < 3 || f.execed[n-1] != BootLine {
		t.Fatalf("a fresh claude has no first turn: last argv %q", f.execed[n-1])
	}
	if f.execed[n-3] != "--append-system-prompt" || !strings.Contains(f.execed[n-2], "ROLE INSTRUCTIONS") {
		t.Fatalf("the role prompt is no longer the system prompt: %q", f.execed[n-3:])
	}

	// Resumed (up --resume, a reboot): the session is back at an empty prompt too.
	if _, e, err := f.launchWithResume(m, "coder", "session-123"); err != nil {
		t.Fatal(err, e)
	}
	if got := f.execed[len(f.execed)-1]; got != BootLine {
		t.Fatalf("a resumed claude has no first turn: last argv %q", got)
	}

	// The dry run shows it.
	out, _, err := f.launch(m, "coder", "AGENT_LAUNCH_DRYRUN=1")
	must(t, err)
	if !strings.Contains(out, " "+ShellJoin([]string{BootLine})+"\n") {
		t.Fatalf("dry run without the boot line: %q", out)
	}
}

// The boot line is one fixed line that names the role's own steps: every
// role file has an "On boot" section.
func TestBootLineIsFixed(t *testing.T) {
	if BootLine == "" || strings.ContainsAny(BootLine, "\r\n") || !strings.Contains(BootLine, "On boot") {
		t.Fatalf("BootLine %q", BootLine)
	}
}
