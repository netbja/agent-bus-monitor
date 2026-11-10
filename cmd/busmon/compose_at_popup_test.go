package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/filepick"
)

// The popup (compose-at-file step 3, spec §4): the window's text field with a
// fake lister, as the other compose tests drive the Model.

const atRoot, atHome = "/work/agent-bus-monitor", "/home/charlie"

// atFixtureFiles is the project's listing: a file name prefix, a segment
// prefix and a worktree among them.
var atFixtureFiles = []filepick.Entry{
	{Path: "README.md"}, {Path: "cmd", Dir: true}, {Path: "cmd/busmon", Dir: true},
	{Path: "cmd/busmon/compose.go"}, {Path: "cmd/busmon/tea_compose.go"}, {Path: "docs", Dir: true},
	{Path: "docs/my notes.md"}, {Path: ".worktrees/x", Dir: true, Repo: true},
}

// atFake is a lister that counts its calls.
type atFake struct {
	calls, dirCalls int
	res             filepick.Result
	err             error
	sub             map[string]filepick.Result
	home            map[string]filepick.Result
}

func (f *atFake) source() *atSource {
	return &atSource{root: atRoot, home: atHome,
		list: func(_ context.Context, o filepick.Options) (filepick.Result, error) {
			f.calls++
			if o.Sub != "" {
				return f.sub[o.Sub], nil
			}
			return f.res, f.err
		},
		listDir: func(_ context.Context, home, dir string, _ filepick.Options) (filepick.Result, error) {
			f.dirCalls++
			r, ok := f.home[dir]
			if !ok {
				return filepick.Result{}, errors.New("not offered: cannot be read")
			}
			return r, nil
		}}
}

// atShell is the fixture shell at w x h with the window open on its text,
// the lister fake.
func atShell(t testing.TB, w, h int, f *atFake) Model {
	t.Helper()
	m := fixtureShell(t, w, h)
	m.cfg.files = f.source()
	m = keys(t, m, 'I', kTab) // the window, then its text
	if c := m.ui.compose; c == nil || c.onTo {
		t.Fatal("the window is not on its text")
	}
	return m
}

// typeAt types s rune by rune, running each key's command (the listing).
func typeAt(t testing.TB, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		var cmd tea.Cmd
		m, cmd = pressCmd(t, m, r)
		m = run(t, m, cmd)
	}
	return m
}

func pressAt(t testing.TB, m Model, k tea.Key) Model {
	t.Helper()
	m, cmd := pressCmd(t, m, k)
	return run(t, m, cmd)
}

func atRows(m Model) []string {
	var out []string
	if p := m.ui.compose.at; p != nil {
		for _, r := range p.rows {
			out = append(out, r.label)
		}
	}
	return out
}

func TestAtOpensOnATypedAtOnly(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := atShell(t, 140, 40, f)
	m, cmd := pressCmd(t, m, '@')
	if m.ui.compose.at == nil {
		t.Fatal("a typed @ at the start did not open the popup")
	}
	if !strings.Contains(m.grid().plain(), "reading the file list"+GlyphEllipsis) {
		t.Error("before the list answers the popup says it is reading")
	}
	m = run(t, m, cmd)
	if f.calls != 1 || len(atRows(m)) == 0 {
		t.Fatalf("calls %d rows %v", f.calls, atRows(m))
	}
	m = typeAt(t, m, "co")
	if f.calls != 1 {
		t.Errorf("the list is read once per window opening, read %d times", f.calls)
	}
	if got := atRows(m); len(got) == 0 || got[0] != "cmd/busmon/compose.go" {
		t.Errorf("ranked rows %v", got)
	}

	// An email never opens it; a paste never does.
	m = fixtureShell(t, 140, 40)
	m.cfg.files = f.source()
	m = typeAt(t, keys(t, m, 'I', kTab), "a@b")
	if m.ui.compose.at != nil {
		t.Error("a@b opened the popup")
	}
	m = step(t, m, tea.PasteMsg{Content: " @cmd"})
	if m.ui.compose.at != nil {
		t.Error("a paste opened the popup")
	}
	// A paste while it is open closes it: the query is no longer typed.
	m = typeAt(t, atShell(t, 140, 40, f), "@c")
	m = step(t, m, tea.PasteMsg{Content: "md"})
	if m.ui.compose.at != nil {
		t.Error("a paste typed into the open popup's query")
	}
	// After a space or an opener it does.
	for _, pre := range []string{"see ", "(", "[", "\"", "'"} {
		m := typeAt(t, atShell(t, 140, 40, f), pre+"@")
		if m.ui.compose.at == nil {
			t.Errorf("@ after %q did not open the popup", pre)
		}
	}
}

func TestAtSpaceAndCursorCloseIt(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := typeAt(t, atShell(t, 140, 40, f), "@cmd")
	m = typeAt(t, m, " ")
	if m.ui.compose.at != nil {
		t.Error("a space left the popup open")
	}
	if got := m.ui.compose.area.value(); got != "@cmd " {
		t.Errorf("text %q", got)
	}
	m = typeAt(t, atShell(t, 140, 40, f), "x @cm")
	m = pressAt(t, m, tea.Key{Code: tea.KeyHome})
	if m.ui.compose.at != nil {
		t.Error("the cursor moved out of the token and the popup stayed")
	}
}

func TestAtEnterInsertsTheAbsolutePath(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := typeAt(t, atShell(t, 140, 40, f), "read @tea_c")
	m = pressAt(t, m, kEnter)
	want := "read " + filepath.Join(atRoot, "cmd/busmon/tea_compose.go") + " "
	if got := m.ui.compose.area.value(); got != want {
		t.Errorf("text %q, want %q", got, want)
	}
	if m.ui.compose.at != nil {
		t.Error("the popup stayed open after the insert")
	}
	// The pick comes first on an empty query next time.
	m = typeAt(t, m, "@")
	if got := atRows(m); len(got) == 0 || got[0] != "cmd/busmon/tea_compose.go" {
		t.Errorf("recent first: %v", got)
	}
	m = pressAt(t, m, kEsc)
	m = pressAt(t, m, kBackspce)
	// One undo step brings the @query back.
	m = pressAt(t, m, tea.Key{Code: 'z', Mod: tea.ModCtrl}) // the backspace
	m = pressAt(t, m, tea.Key{Code: 'z', Mod: tea.ModCtrl}) // the typed @
	m = pressAt(t, m, tea.Key{Code: 'z', Mod: tea.ModCtrl}) // the insert
	if got := m.ui.compose.area.value(); got != "read @tea_c" {
		t.Errorf("after undo %q", got)
	}
}

func TestAtRelativeFormAndQuoting(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := atShell(t, 140, 40, f)
	m.cfg.settings.AtPath = "relative"
	m = pressAt(t, typeAt(t, m, "@notes"), kEnter)
	if got := m.ui.compose.area.value(); got != "`docs/my notes.md` " {
		t.Errorf("text %q", got)
	}
}

func TestAtTabDescendsADirectoryAndInsertsAFile(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := typeAt(t, atShell(t, 140, 40, f), "@cmd")
	if got := atRows(m); len(got) == 0 || got[0] != "cmd/" {
		t.Fatalf("rows %v", got)
	}
	m = pressAt(t, m, kTab)
	if got := m.ui.compose.area.value(); got != "@cmd/" || m.ui.compose.at == nil {
		t.Fatalf("tab on a directory: %q, open %v", got, m.ui.compose.at != nil)
	}
	if !m.ui.compose.onTo && m.ui.compose.kind == composeMessage && m.ui.compose.at == nil {
		t.Error("tab switched fields")
	}
	if got := atRows(m); len(got) == 0 || got[0] != "cmd/busmon/" {
		t.Fatalf("cmd/ lists %v", got)
	}
	m = pressAt(t, m, kEnter) // enter on a directory inserts it
	if got := m.ui.compose.area.value(); got != filepath.Join(atRoot, "cmd/busmon")+string(filepath.Separator)+" " {
		t.Errorf("enter on a directory: %q", got)
	}
	if strings.Contains(m.ui.compose.area.value(), "\n") {
		t.Error("enter added a new line")
	}
	m = typeAt(t, atShell(t, 140, 40, f), "@README")
	m = pressAt(t, m, kTab)
	if got := m.ui.compose.area.value(); got != filepath.Join(atRoot, "README.md")+" " {
		t.Errorf("tab on a file: %q", got)
	}
}

func TestAtEscClosesThePopupThenTheWindow(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := typeAt(t, atShell(t, 140, 40, f), "@do")
	m = pressAt(t, m, kEsc)
	if m.ui.compose == nil || m.ui.compose.at != nil {
		t.Fatal("esc did not close the popup only")
	}
	if got := m.ui.compose.area.value(); got != "@do" {
		t.Errorf("text %q", got)
	}
	m = typeAt(t, m, "c") // the query typed on: the popup stays closed
	if m.ui.compose.at != nil {
		t.Error("typing after esc reopened the popup")
	}
	m = pressAt(t, m, kEsc)
	if m.ui.compose != nil {
		t.Error("the second esc did not close the window")
	}
}

func TestAtArrowsMoveTheSelection(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := typeAt(t, atShell(t, 140, 40, f), "@comp")
	rows := atRows(m)
	if len(rows) < 2 {
		t.Fatalf("rows %v", rows)
	}
	m = pressAt(t, m, kDown)
	m = pressAt(t, m, kEnter)
	if got := m.ui.compose.area.value(); got != filepath.Join(atRoot, filepath.FromSlash(rows[1]))+" " {
		t.Errorf("the second row inserted %q, rows %v", got, rows)
	}
}

func TestAtStates(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	for q, want := range map[string]string{
		"@/etc":   "outside the project: type the path yourself",
		"@../x":   "outside the project: type the path yourself",
		"@.env":   "environment file: not offered",
		"@~bob/x": "only your own home (~/)",
		"@~/a/..": "type the path yourself",
	} {
		m := typeAt(t, atShell(t, 140, 40, f), q)
		if p := m.ui.compose.at; p == nil || p.state != want {
			t.Errorf("%s: state %+v, want %q", q, p, want)
		}
		if !strings.Contains(m.grid().plain(), want) {
			t.Errorf("%s: the screen lacks %q", q, want)
		}
	}
	f = &atFake{err: filepick.ErrNoCheckout}
	m := typeAt(t, atShell(t, 140, 40, f), "@")
	if want := "no checkout of agent-bus-monitor on this machine"; m.ui.compose.at.state != want {
		t.Errorf("no checkout: %q", m.ui.compose.at.state)
	}
	m = fixtureShell(t, 140, 40) // no source at all
	m = typeAt(t, keys(t, m, 'I', kTab), "@")
	if want := "no checkout of agent-bus-monitor on this machine"; m.ui.compose.at.state != want {
		t.Errorf("no source: %q", m.ui.compose.at.state)
	}
	f = &atFake{res: filepick.Result{Entries: atFixtureFiles, Incomplete: "50000 entries"}}
	m = typeAt(t, atShell(t, 140, 40, f), "@c")
	if p := m.ui.compose.at; p.state != "list incomplete (50000 entries)" || len(p.rows) == 0 {
		t.Errorf("incomplete: %+v", p)
	}
}

func TestAtWorktreeOnDemand(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles},
		sub: map[string]filepick.Result{".worktrees/x": {Entries: []filepick.Entry{{Path: ".worktrees/x/main.go"}}}}}
	m := typeAt(t, atShell(t, 140, 40, f), "@.worktrees/x/ma")
	if got := atRows(m); len(got) != 1 || got[0] != ".worktrees/x/main.go" {
		t.Errorf("rows %v", got)
	}
}

func TestAtHome(t *testing.T) {
	f := &atFake{home: map[string]filepick.Result{
		"":          {Entries: []filepick.Entry{{Path: "Documents", Dir: true}, {Path: "notes.md"}}},
		"Documents": {Entries: []filepick.Entry{{Path: "Documents/report.md"}, {Path: "Documents/drafts", Dir: true}}},
	}}
	m := typeAt(t, atShell(t, 140, 40, f), "@~/")
	if got := atRows(m); len(got) != 2 || got[0] != "~/Documents/" {
		t.Fatalf("rows %v", got)
	}
	m = pressAt(t, m, kTab)
	if got := m.ui.compose.area.value(); got != "@~/Documents/" {
		t.Fatalf("tab: %q", got)
	}
	m = typeAt(t, m, "rep")
	if got := atRows(m); len(got) != 1 || got[0] != "~/Documents/report.md" {
		t.Fatalf("rows %v", got)
	}
	m.cfg.settings.AtPath = "relative" // a home path is absolute whatever the form
	m = pressAt(t, m, kEnter)
	if got := m.ui.compose.area.value(); got != filepath.Join(atHome, "Documents/report.md")+" " {
		t.Errorf("text %q", got)
	}
	m = typeAt(t, atShell(t, 140, 40, f), "@~/.ssh/config")
	if got := m.ui.compose.at.state; !strings.Contains(got, "not offered") && !strings.Contains(got, "hidden") {
		t.Errorf("a hidden file: %q", got)
	}
	src := f.source()
	src.home = ""
	m = fixtureShell(t, 140, 40)
	m.cfg.files = src
	m = typeAt(t, keys(t, m, 'I', kTab), "@~/")
	if m.ui.compose.at.state != "no home directory" {
		t.Errorf("no home: %q", m.ui.compose.at.state)
	}
}

func TestAtTheCapRefusesAnInsert(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := atShell(t, 140, 40, f)
	m.ui.compose.area.limit = 10
	m = pressAt(t, typeAt(t, m, "@READ"), kEnter)
	if got := m.ui.compose.area.value(); got != "@READ" {
		t.Errorf("text %q", got)
	}
	if p := m.ui.compose.at; p == nil || p.note != "the message is full" {
		t.Errorf("state %+v", p)
	}
	if !strings.Contains(m.grid().plain(), "the message is full") {
		t.Error("the footer does not say the message is full")
	}
}

func TestAtTwoFilesInOneMessage(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := pressAt(t, typeAt(t, atShell(t, 140, 40, f), "@READ"), kEnter)
	m = pressAt(t, typeAt(t, m, "and @tea_"), kEnter)
	want := filepath.Join(atRoot, "README.md") + " and " + filepath.Join(atRoot, "cmd/busmon/tea_compose.go") + " "
	if got := m.ui.compose.area.value(); got != want {
		t.Errorf("text %q", got)
	}
}

func TestAtInTheAnswerWindow(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := shellWith(t, 140, 40, shellConfig{}, false)
	m.cfg.files = f.source()
	m = keys(t, m, '!', 'o') // Other: the answer window on the first question
	if c := m.ui.compose; c == nil || c.kind != composeAnswer {
		t.Fatalf("o: %+v", m.ui.compose)
	}
	m = pressAt(t, typeAt(t, m, "@READ"), kEnter)
	if got := m.ui.compose.area.value(); got != filepath.Join(atRoot, "README.md")+" " {
		t.Errorf("text %q", got)
	}
}

func TestAtToFieldKeepsRecipients(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := fixtureShell(t, 140, 40)
	m.cfg.files = f.source()
	m = typeAt(t, keys(t, m, 'I'), "@cod")
	if m.ui.compose.at != nil || f.calls != 0 {
		t.Error("@ in the to field opened the file popup")
	}
	if len(m.toCompletionsOf(m.ui.compose)) == 0 {
		t.Error("the to field lost its completions")
	}
}

func TestAtMouse(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := typeAt(t, atShell(t, 140, 40, f), "@")
	_, rows := m.atRects()
	if rows.H == 0 {
		t.Fatal("no popup rect")
	}
	want := m.ui.compose.at.rows[1]
	m, cmd := clickCmd(t, m, rows.X+1, rows.Y+1)
	m = run(t, m, cmd)
	wantText := filepath.Join(atRoot, filepath.FromSlash(want.rel))
	if want.dir {
		wantText += string(filepath.Separator)
	}
	if got := m.ui.compose.area.value(); got != wantText+" " {
		t.Errorf("click inserted %q, want %q", got, wantText+" ")
	}
	many := make([]filepick.Entry, 40)
	for i := range many {
		many[i] = filepick.Entry{Path: "f" + strings.Repeat("x", i)}
	}
	f = &atFake{res: filepick.Result{Entries: many}}
	m = typeAt(t, atShell(t, 140, 40, f), "@")
	_, rows = m.atRects()
	m = step(t, m, tea.MouseWheelMsg{X: rows.X + 1, Y: rows.Y, Button: tea.MouseWheelDown})
	if m.ui.compose.at.top == 0 {
		t.Error("the wheel did not scroll the list")
	}
}

func TestAtFooterIsTheTable(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := typeAt(t, atShell(t, 140, 40, f), "@")
	plain := m.grid().plain()
	for _, b := range composeFileBindings {
		if b.Hint != "" && !strings.Contains(plain, b.Keys+" "+b.Hint) {
			t.Errorf("footer lacks %q", b.Keys+" "+b.Hint)
		}
	}
	if n := len(m.ui.compose.at.rows); !strings.Contains(plain, fmt.Sprintf("%d files", n)) {
		t.Errorf("the footer lacks the count %d", n)
	}
	if !strings.Contains(plainOf(helpText()), "the typed @query kept") {
		t.Error("the help lacks the popup's keys")
	}
}

func TestAtTiersAndFlip(t *testing.T) {
	many := make([]filepick.Entry, 40)
	for i := range many {
		many[i] = filepick.Entry{Path: "f" + strings.Repeat("x", i)}
	}
	f := &atFake{res: filepick.Result{Entries: many}}
	for _, c := range []struct{ w, h, rows int }{{140, 40, 8}, {100, 40, 6}, {70, 30, 4}} {
		m := typeAt(t, atShell(t, c.w, c.h, f), "@")
		_, r := m.atRects()
		if r.H != c.rows {
			t.Errorf("%dx%d: %d rows, want %d", c.w, c.h, r.H, c.rows)
		}
	}
	// The cursor near the bottom: the popup flips above its row.
	m := atShell(t, 140, 40, f)
	m = typeAt(t, m, strings.Repeat("\n", 0))
	for range 9 {
		m = pressAt(t, m, kEnter)
	}
	m = typeAt(t, m, "@")
	_, tr := m.composeRects()
	frame, _ := m.atRects()
	rs := m.ui.compose.area.rowsAt(tr.W)
	cy := tr.Y + rowOf(rs, m.ui.compose.area.cur) - m.ui.compose.area.top
	if frame.Y+frame.H > cy {
		t.Errorf("the popup (y %d h %d) is not above the cursor row %d", frame.Y, frame.H, cy)
	}
}

// atGolden is the window with its @ popup in a golden state: below the
// cursor's row, flipped above it, reading, outside the project, incomplete.
func atGolden(t testing.TB, m Model, state string) Model {
	t.Helper()
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	if state == "compose-at-incomplete" {
		f.res.Incomplete = "the 2s deadline"
	}
	m.cfg.files = f.source()
	m = typeText(t, keys(t, m, 'I'), "@cod")
	m = typeText(t, keys(t, m, kTab), "Please read")
	switch state {
	case "compose-at-above": // the cursor on the window's last text row
		_, tr := m.composeRects()
		for range tr.H - 1 {
			m = keys(t, m, kEnter)
		}
	case "compose-at-reading": // the listing not answered yet
		m, _ = pressCmd(t, keys(t, m, ' '), '@')
		return m
	case "compose-at-outside":
		return typeAt(t, m, " @/etc")
	}
	return pressAt(t, typeAt(t, m, " @c"), kDown)
}

// A message's text that is only @prefix of an agent: tab keeps making it the
// recipient (the window's existing rule, tea_compose.go prefixToRecipient),
// popup or not; enter still picks a file. In an answer window, or once the
// text holds more, tab is the popup's.
func TestAtTabOnARecipientPrefixKeepsTheRecipient(t *testing.T) {
	f := &atFake{res: filepick.Result{Entries: atFixtureFiles}}
	m := pressAt(t, typeAt(t, atShell(t, 140, 40, f), "@co"), kTab)
	if c := m.ui.compose; c.to.value() != "@coder" || c.area.value() != "" || c.at != nil {
		t.Errorf("to %q text %q popup %v", c.to.value(), c.area.value(), c.at != nil)
	}
	m = pressAt(t, typeAt(t, atShell(t, 140, 40, f), "@co"), kEnter)
	if got := m.ui.compose.area.value(); !strings.HasPrefix(got, atRoot) {
		t.Errorf("enter on @co: %q", got)
	}
	m = pressAt(t, typeAt(t, atShell(t, 140, 40, f), "see @cmd"), kTab)
	if got := m.ui.compose.area.value(); got != "see @cmd/" {
		t.Errorf("tab after text: %q", got)
	}
}

// atSourceFor: the template's working_dir, $HOME, and roles.toml's shared
// Read denies (a role's own are not every agent's); no roles.toml is no
// deny; one that cannot be read offers nothing (fail closed).
func TestAtSourceFor(t *testing.T) {
	dir := t.TempDir()
	root, home := filepath.Join(dir, "work"), filepath.Join(dir, "home")
	projects := filepath.Join(dir, "projects")
	for _, d := range []string{root, home, projects} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projects, "demo.toml"), []byte("working_dir = \""+root+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"p\"\n[profiles.p]\nclient = \"codex\"\nprovider = \"openai\"\nmodel = \"m\"\n" +
		"[tool_rules]\ndeny = [\"Read(**/secrets/**)\"]\n" +
		"[roles.local]\ntier = \"pop\"\n[roles.local.tool_rules]\ndeny = [\"Read(docs/**)\"]\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"AGENTBUS_PROJECTS_DIR": projects, "HOME": home, "ROLES_TOML": manifest}
	getenv := func(k string) string { return env[k] }
	src := atSourceFor("demo", getenv)
	if src.root != root || src.home != home || src.denyErr != "" || src.deny == nil {
		t.Fatalf("source %+v", src)
	}
	if !src.deny(filepath.Join(root, "secrets/a.txt")) || src.deny(filepath.Join(root, "docs/a.md")) {
		t.Error("the shared deny is not the one applied")
	}
	if src := atSourceFor("other", getenv); src.root != "" {
		t.Errorf("a project with no template has root %q", src.root)
	}
	env["ROLES_TOML"] = filepath.Join(dir, "missing.toml")
	if src := atSourceFor("demo", getenv); src.deny != nil || src.denyErr != "" {
		t.Errorf("no roles.toml: %+v", src)
	}
	if err := os.WriteFile(manifest, []byte("[tool_rules]\ndeny = [\"Read(\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env["ROLES_TOML"] = manifest
	src = atSourceFor("demo", getenv)
	if src.denyErr == "" {
		t.Fatal("an unreadable roles.toml did not fail closed")
	}
	m := fixtureShell(t, 140, 40)
	m.cfg.files = src
	m = typeAt(t, keys(t, m, 'I', kTab), "@")
	if p := m.ui.compose.at; p.state != src.denyErr || len(p.rows) != 0 {
		t.Errorf("fail closed: %+v", p)
	}
	env["HOME"] = "relative"
	if src := atSourceFor("demo", getenv); src.home != "" {
		t.Errorf("a relative HOME: %q", src.home)
	}
}
