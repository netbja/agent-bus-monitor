package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// drawnText is what the person reads of a body in the overlay: its Lines
// through the one adapter, read back by a tview TextView with tags on.
func drawnText(ls []Line) string {
	return tview.NewTextView().SetDynamicColors(true).SetText(linesToTview(ls)).GetText(true)
}

func TestBodyBuilderKeepsTextSemantics(t *testing.T) {
	var b body
	b.text("a").seg(RoleMuted, "b\nc").text("\n\n").join([]Line{{S(RoleOK, "d")}, {S(RoleOK, "e")}}).text("\n")
	if got := linesPlain(b.Lines()); got != "ab\nc\n\nd\ne\n" {
		t.Fatalf("plain = %q", got)
	}
	if b.at() != 5 {
		t.Fatalf("at = %d", b.at())
	}
	if got := linesPlain(trimEnd(b.Lines())); got != "ab\nc\n\nd\ne" {
		t.Fatalf("trimEnd = %q", got)
	}
}

// hostile is agent text that looks like every kind of tview tag.
const hostile = `[red]a[::b]b["x"]c[-]d`

// Every body builder's agent text goes through linesToTview and is drawn
// literally: no builder escapes, the adapter escapes every segment once.
func TestHostileTagsAreDrawnLiterally(t *testing.T) {
	ev := bus.Event{ID: "1792999990000-0", Kind: "cmd", Type: bus.CmdDirective, From: "coder", Target: "master",
		Message: hostile, Ref: hostile}
	a := &agentState{state: "waiting", message: hostile, stateAt: detNow, lastSeen: detNow, pane: hostile}
	board := map[string]bus.BoardEntry{hostile: {Owner: "grok", State: hostile, Branch: hostile}}
	reqs := boardRequests(map[string]bus.BoardEntry{hostile: requestedEntry()})
	ask := bus.Ask{ID: "1-0", From: "master", Title: hostile, State: "open", Questions: []bus.AskQuestionState{
		{AskQuestion: bus.AskQuestion{ID: "q", Header: hostile, Question: hostile, Context: hostile,
			Options: []bus.AskOption{{Label: hostile, Description: hostile}}}}}}
	_, agent := agentDetail("grok", a, false, board, []feedLine{{ev: ev, at: detNow}}, detNow)
	_, task, _ := taskDetail(board[hostile], hostile, detNow)
	_, req, _ := requestDetail(reqs[0], detNow)
	bodies := map[string][]Line{
		"message":  detailText(ev, detNow, detNow, threadState{ID: hostile, Loaded: true, Entries: []bus.Event{ev}}),
		"agent":    agent,
		"task":     task,
		"request":  req,
		"requests": requestsPanel(reqs, []openThread{{ID: "1-0", Type: "directive", From: hostile, Target: "x", Message: hostile}}, detNow, 100),
		"services": servicesPanel(nil, map[string]bus.ServiceRecord{"dispatch": {State: "failed", Detail: hostile, Error: hostile, Host: "elsewhere"}}, "here", detNow, 100),
		"health": healthOverlayText(map[string]guardian.Record{"tracked-secrets": {ID: "tracked-secrets", Status: guardian.Fail, Detail: hostile, ObservedAt: detNow.UnixMilli(), Since: detNow.UnixMilli()}},
			map[string]guardian.Note{"tracked-secrets": {Author: "sentinel", Text: hostile}}, detNow),
		"ask":      askPanelBody(ask, askPanel{}, 100),
		"summary":  askSummary(ask),
		"markdown": renderMarkdown("# "+hostile+"\n- "+hostile+"\n> "+hostile+"\n```\n"+hostile+"\n```", 100),
	}
	for name, ls := range bodies {
		sim := tcell.NewSimulationScreen("UTF-8")
		if err := sim.Init(); err != nil {
			t.Fatal(err)
		}
		sim.SetSize(200, 120)
		v := tview.NewTextView().SetDynamicColors(true).SetText(linesToTview(ls))
		v.SetRect(0, 0, 200, 120)
		v.Draw(sim)
		sim.Show()
		if screen := screenText(sim); !strings.Contains(screen, hostile) {
			t.Errorf("%s: %q not drawn literally:\n%s", name, hostile, screen)
		}
		sim.Fini()
	}
}

// The reminder, the recap and the overlay titles go through the same adapter.
func TestHostileTagsInUIBodiesAndTitles(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.u.cachePath = filepath.Join(t.TempDir(), "cache.json")
	h.u.cache = &busmonCache{Project: "demo", Snoozes: map[string]int64{}}
	h.u.openReminder([]attentionItem{{Key: "wait:x:1", Agent: "x", Text: hostile, At: time.Now()}})
	if got := h.u.overlay.GetText(true); !strings.Contains(got, hostile) {
		t.Fatalf("reminder: %q", got)
	}
	h.u.closeOverlay()
	h.u.showOverlay(ovMessage, hostile, nil, "", "")
	if got := tview.NewTextView().SetDynamicColors(true).SetText(h.u.overlay.GetTitle()).GetText(true); !strings.Contains(got, hostile) {
		t.Fatalf("title: %q", got)
	}
}

// tviewTag is tview's colour/style tag syntax, the names this package used.
var tviewTag = regexp.MustCompile(`\[(-|::[a-z-]*|#[0-9a-fA-F]{6}|(red|gray|grey|yellow|aqua|white|green|blue|fuchsia|silver|maroon|olive|teal|purple|orange|cyan)(::[a-z-]*)?)\]`)

// Only theme.go writes a tview tag or escapes for one (segTag, called by
// linesToTview): no other file of the package may.
func TestOnlyThemeWritesTviewTags(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || path == "theme.go" {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "tview.Escape(") {
			t.Errorf("%s escapes for tview; only theme.go's segTag may", path)
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if bl, ok := n.(*ast.BasicLit); ok && bl.Kind == token.STRING {
				if s, _ := strconv.Unquote(bl.Value); tviewTag.MatchString(s) {
					t.Errorf("%s: a tview tag in %s", path, bl.Value)
				}
			}
			return true
		})
	}
}
