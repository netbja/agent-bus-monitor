package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/config"
	guardian "github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// The person's console in busmon (herdr exit spec 4.6, step 7b2): an agent's
// page shows a read-only preview of its terminal on the project's holder,
// and t puts the person's terminal on it through agentbus attach. busmon
// itself never types into a terminal: it only reads List and Screen.

// termReader is the part of termhost.Host the preview uses: reads only.
type termReader interface {
	List(ctx context.Context, project string) ([]termhost.Term, error)
	Screen(ctx context.Context, id termhost.TermID, lines int) (termhost.Screen, error)
	Processes(ctx context.Context, id termhost.TermID) ([]termhost.Proc, error)
}

// busmonTerms is the project's holders, the current build's first; nil (no
// preview) when the project cannot have one.
func busmonTerms(project string) termReader {
	h, err := holder.NewHost(project, holder.BuildID(), os.Getenv)
	if err != nil {
		return nil
	}
	return h
}

// termPreviewRows is how many of the screen's last lines the page shows.
const termPreviewRows = 30

// blockedWords is what busmon says of an agent whose client asks something
// in its terminal (agent-screen-blocked §3).
const blockedWords = "blocked: waiting for an answer in its terminal"

// blockedLines is how many of a blocked screen's last non-blank lines the
// page shows (the person's answer to Q4).
const blockedLines = 6

// termPreviewEvery: while an agent's page is on top, its terminal is read
// again this often.
const termPreviewEvery = time.Second

// termView is one read of an agent's terminal: found is false when the agent
// has no live terminal on its holder.
type termView struct {
	found bool
	lines []string
	err   error
	// prefix is the attach prefix (attach.prefix), named beside "t attaches"
	// with its detach (P1-8); "" names none.
	prefix string
	// heading: the words of an agent the person must look at (blocked on its
	// client's prompt, or stuck); set when the page is drawn, from the
	// snapshot, not by the read. The screen then shows its last lines only.
	heading string
	// procs: the program names of the agent's descendants, its own process
	// left out (background-work-visible Q3): read with the screen, in this
	// page's memory only, never stored.
	procs []string
}

// lastNonBlank is the last n lines of ls that are not blank, in order.
func lastNonBlank(ls []string, n int) []string {
	var out []string
	for i := len(ls) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(ls[i]) != "" {
			out = append([]string{ls[i]}, out...)
		}
	}
	return out
}

// attachPrefix is the attach.prefix agentbus attach will use; "" when the
// setting is invalid (attach refuses it).
func attachPrefix(getenv func(string) string) string {
	r := config.Resolver{Getenv: getenv, Files: []*config.File{config.Load(config.Path(getenv), "config.toml")}}
	got := r.Get("attach.prefix")
	if got.Invalid {
		return ""
	}
	s, _ := got.Value.(string)
	return s
}

// readTermView reads agent's terminal in project (holderTerm: the one attach
// picks); a nil host or no terminal is an empty view, a failed read its error.
func readTermView(ctx context.Context, h termReader, project, agent string, rows int) termView {
	if h == nil {
		return termView{}
	}
	terms, err := h.List(ctx, project)
	if err != nil {
		return termView{err: err}
	}
	t, ok := holderTerm(terms, project, agent)
	if !ok {
		return termView{}
	}
	sc, err := h.Screen(ctx, t.ID, rows)
	if err != nil {
		return termView{err: err}
	}
	lines := sc.Lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	var names []string
	if ps, err := h.Processes(ctx, t.ID); err == nil { // a failed read names nothing; the screen stands
		for _, p := range ps {
			if p.PID != t.PID {
				names = append(names, p.Command)
			}
		}
	}
	return termView{found: true, lines: lines, procs: names}
}

// termPreviewLines is the page's Screen section: nothing when the agent has
// no terminal on a holder. The text lines only, through Preview: no byte the
// program printed reaches busmon's own terminal.
func termPreviewLines(v termView, width int) []Line {
	var b body
	switch {
	case v.err != nil:
		b.seg(RoleAccent, "Screen").text("\n")
		b.seg(RoleMuted, "not read: "+Preview(v.err.Error())).text("\n\n")
	case v.found && v.heading != "":
		b.seg(RoleAccent, "Screen").seg(RoleAttention, " "+GlyphNone+" "+v.heading).seg(RoleMuted, " "+GlyphNone+" t attaches").text("\n")
		for _, l := range lastNonBlank(v.lines, blockedLines) {
			b.seg(RoleText, Truncate(Preview(guardian.Redact(l)), max(1, width))).text("\n")
		}
		b.text("\n")
	case v.found:
		how := " " + GlyphNone + " read-only; t attaches"
		if v.prefix != "" {
			how += ", then " + config.AttachKeyLabel(v.prefix) + " d detaches"
		}
		b.seg(RoleAccent, "Screen").seg(RoleMuted, how).text("\n")
		for _, l := range v.lines {
			b.seg(RoleText, Truncate(Preview(l), max(1, width))).text("\n")
		}
		b.text("\n")
	default:
		return nil
	}
	return b.Lines()
}

// termViewMsg is a read of the terminal of the agent page with that serial.
type termViewMsg struct {
	serial int
	view   termView
}

// termTickMsg asks for the next read of the agent page with that serial.
type termTickMsg struct{ serial int }

func termTick(serial int) tea.Cmd {
	return tea.Tick(termPreviewEvery, func(time.Time) tea.Msg { return termTickMsg{serial: serial} })
}

// agentPage is the open agent page with that serial.
func (m *Model) agentPage(serial int) *page {
	for i := range m.rd.pages {
		if p := &m.rd.pages[i]; p.serial == serial && p.kind == ovAgent {
			return p
		}
	}
	return nil
}

// termRead reads the page's terminal, unless the page is gone (the chain
// ends) or under another one (the chain waits without reading).
func (m *Model) termRead(serial int) tea.Cmd {
	p := m.agentPage(serial)
	if p == nil || m.cfg.terms == nil {
		return nil
	}
	if p != m.rd.top() {
		return termTick(serial)
	}
	ctx, h, project, agent := m.cfg.ctx, m.cfg.terms, m.cfg.project, p.key
	if ctx == nil { // a test's shell
		ctx = context.Background()
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		v := readTermView(c, h, project, agent, termPreviewRows)
		v.prefix = attachPrefix(os.Getenv)
		return termViewMsg{serial: serial, view: v}
	}
}

// gotTermView stores the read on its page and schedules the next one.
func (m *Model) gotTermView(msg termViewMsg) tea.Cmd {
	p := m.agentPage(msg.serial)
	if p == nil {
		return nil
	}
	v := msg.view
	p.term, p.dirty = &v, true
	m.refresh()
	return termTick(msg.serial)
}

// attachCommand is `agentbus --project <p> attach <agent>`: stdin and stdout
// left to bubbletea (the person's terminal), stderr captured for a refusal.
func attachCommand(bin, project, agent string) *exec.Cmd {
	c := exec.Command(bin, "--project", project, "attach", agent)
	c.Stderr = &bytes.Buffer{}
	return c
}

// attachDoneMsg is the end of an attach: its error and what it wrote on stderr.
type attachDoneMsg struct {
	agent, stderr string
	err           error
}

// attachCmd hands the terminal to agentbus attach; detach returns to busmon.
func (m *Model) attachCmd(agent string) tea.Cmd {
	if agent == "" {
		return nil
	}
	bin := m.cfg.agentbusBin
	if bin == "" {
		bin = "agentbus"
	}
	c := attachCommand(bin, m.cfg.project, agent)
	buf := c.Stderr.(*bytes.Buffer)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return attachDoneMsg{agent: agent, stderr: buf.String(), err: err}
	})
}

// attachFlash is the status line after an attach: "" after a clean detach,
// else agentbus's last line (the refusal names the --prefix choices).
func attachFlash(agent string, err error, stderr string) string {
	if err == nil {
		return ""
	}
	why := err.Error()
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		why = last
	}
	return "attach " + Preview(agent) + ": " + Preview(why)
}
