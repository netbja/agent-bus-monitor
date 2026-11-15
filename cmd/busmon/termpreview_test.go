package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

func startTerm(t *testing.T, h *fake.Host, project, name string) termhost.Term {
	t.Helper()
	tm, err := h.Start(context.Background(), termhost.Spec{Project: project, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

// The preview reads the agent's newest live terminal, never a closed one, never
// another agent's or another project's.
func TestTermViewReadsTheAgentsLiveTerminal(t *testing.T) {
	h := fake.New()
	old := startTerm(t, h, "p", "coder")
	h.SetScreen(old.ID, "old screen")
	cur := startTerm(t, h, "p", "coder")
	h.SetScreen(cur.ID, "claude> hello", "", "")
	gone := startTerm(t, h, "p", "coder")
	h.SetScreen(gone.ID, "closed screen")
	if err := h.Close(context.Background(), gone.ID); err != nil {
		t.Fatal(err)
	}
	other := startTerm(t, h, "p", "foureyes")
	h.SetScreen(other.ID, "not coder")
	elsewhere := startTerm(t, h, "q", "coder")
	h.SetScreen(elsewhere.ID, "other project")

	v := readTermView(context.Background(), h, "p", "coder", 20)
	if !v.found || v.err != nil {
		t.Fatalf("view %+v", v)
	}
	if got := strings.Join(v.lines, "|"); got != "claude> hello" { // trailing blank lines dropped
		t.Fatalf("lines %q", got)
	}
	if v := readTermView(context.Background(), h, "p", "master", 20); v.found || v.err != nil {
		t.Fatalf("an agent without a terminal: %+v", v)
	}
}

type failingTerms struct{}

func (failingTerms) List(context.Context, string) ([]termhost.Term, error) {
	return nil, errors.New("holder down")
}
func (failingTerms) Screen(context.Context, termhost.TermID, int) (termhost.Screen, error) {
	return termhost.Screen{}, nil
}

func TestTermViewReportsAFailedRead(t *testing.T) {
	if v := readTermView(context.Background(), failingTerms{}, "p", "coder", 20); v.err == nil || v.found {
		t.Fatalf("view %+v", v)
	}
	if v := readTermView(context.Background(), nil, "p", "coder", 20); v.found || v.err != nil {
		t.Fatalf("no host: %+v", v)
	}
}

// The preview shows text only: a control byte the program printed never
// reaches busmon's terminal.
func TestTermPreviewLinesAreSanitised(t *testing.T) {
	ls := termPreviewLines(termView{found: true, lines: []string{"ok \x1b]52;c;cG9pc29u\x07 end", "\x1b[2Jcleared"}}, 40)
	got := plainOf(ls)
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x07) {
		t.Fatalf("control bytes reached the preview: %q", got)
	}
	for _, want := range []string{"Screen", "t attaches", "ok", "cleared"} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview lacks %q:\n%s", want, got)
		}
	}
	if ls := termPreviewLines(termView{}, 40); len(ls) != 0 {
		t.Fatalf("no terminal: %q", plainOf(ls))
	}
	if got := plainOf(termPreviewLines(termView{err: errors.New("holder down")}, 40)); !strings.Contains(got, "holder down") {
		t.Fatalf("a failed read: %q", got)
	}
}

// t runs agentbus attach for the agent, on the project, through the
// person's terminal; nothing else is in the argv.
func TestAttachCommandArgv(t *testing.T) {
	c := attachCommand("/opt/bin/agentbus", "p", "coder")
	if got := strings.Join(c.Args, " "); got != "/opt/bin/agentbus --project p attach coder" {
		t.Fatalf("argv %q", got)
	}
	if c.Stderr == nil || c.Stdin != nil || c.Stdout != nil {
		t.Fatal("stderr is captured for the refusal; stdin and stdout are the person's terminal")
	}
}

// A refusal comes back as the status line: the last line agentbus wrote.
func TestAttachRefusalIsTheStatusLine(t *testing.T) {
	err := &exec.ExitError{}
	if got := attachFlash("coder", err, "usage\nf12 is bound by codex (free: f11); attach with --prefix <key>\n"); got != "attach coder: f12 is bound by codex (free: f11); attach with --prefix <key>" {
		t.Fatalf("flash %q", got)
	}
	if got := attachFlash("coder", nil, ""); got != "" {
		t.Fatalf("a clean detach says nothing: %q", got)
	}
	if got := attachFlash("coder", errors.New("exec: not found"), ""); got != "attach coder: exec: not found" {
		t.Fatalf("flash %q", got)
	}
	if got := attachFlash("coder", errors.New("exit status 1"), "\x1b]2;x\x07warn"); strings.ContainsRune(got, 0x1b) {
		t.Fatalf("control bytes in the flash: %q", got)
	}
}

// An agent's page reads its terminal while it is open and shows it; the
// chain of reads ends with the page.
func TestAgentPageShowsItsTerminal(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	h := fake.New()
	tm := startTerm(t, h, m.cfg.project, "foureyes")
	h.SetScreen(tm.ID, "claude> reviewing pr 7")
	m.cfg.terms = h
	cmd := m.pushAgent("foureyes")
	if cmd == nil {
		t.Fatal("the page reads no terminal")
	}
	msg, ok := cmd().(termViewMsg)
	if !ok || !msg.view.found {
		t.Fatalf("read %+v", msg)
	}
	next, tick := m.Update(msg)
	m = next.(Model)
	if tick == nil {
		t.Fatal("no next read scheduled")
	}
	if screen := m.grid().plain(); !strings.Contains(screen, "reviewing pr 7") || !strings.Contains(screen, "t attaches") {
		t.Fatalf("the page does not show the terminal:\n%s", screen)
	}
	if _, cmd := pressCmd(t, m, 't'); cmd == nil {
		t.Fatal("t on the agent's page does not attach")
	}
	m.pop()
	if m.termRead(msg.serial) != nil {
		t.Fatal("a closed page is still read")
	}
}

// t on the Agents panel attaches to the selected agent.
func TestAgentsPanelTAttaches(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	m.ui.focus = PanelAgents
	if m.lists.agents.Selected() == "" {
		t.Fatal("no agent selected")
	}
	if _, cmd := pressCmd(t, m, 't'); cmd == nil {
		t.Fatal("t on the Agents panel does not attach")
	}
}

// A page under another one is not read: its chain only waits.
func TestAgentPageUnderAnotherWaits(t *testing.T) {
	m := fixtureShell(t, 140, 40)
	h := fake.New()
	startTerm(t, h, m.cfg.project, "foureyes")
	m.cfg.terms = h
	m.pushAgent("foureyes")
	serial := m.rd.top().serial
	m.push(page{kind: ovHelp})
	cmd := m.termRead(serial)
	if cmd == nil {
		t.Fatal("the chain ended under another page")
	}
	if msg, ok := cmd().(termTickMsg); !ok || msg.serial != serial {
		t.Fatalf("a covered page was read: %T", cmd())
	}
}
