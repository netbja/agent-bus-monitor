package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Class C (palette spec §4.1, slice 2): the person's agentbus commands, run
// as argv after y.

// palCWorld is the palette shell with a fake agentbus runner (as the
// Projects page tests run it) and identity.as set.
type palCWorld struct {
	m    Model
	runs [][]string
	logs []string
}

func newPalCWorld(t *testing.T, as string) *palCWorld {
	t.Helper()
	w := &palCWorld{}
	m, _ := palShell(t)
	m.cfg.settings.Identity = as
	m.cfg.sleepWait = func() time.Duration { return 7 * time.Minute }
	m.cfg.sleepLog = func(p string) string { return "/state/agentbus/" + p + "/suspend.log" }
	m.cfg.sleepRunner = func(args []string, log string) (<-chan error, error) {
		w.runs = append(w.runs, args)
		w.logs = append(w.logs, log)
		return make(chan error), nil
	}
	w.m = m
	return w
}

// run types the line, Enter, then each key; a command a key returns is
// run and its message fed back (sleepAskMsg, sleepStartedMsg).
func (w *palCWorld) run(t *testing.T, line string, then ...rune) {
	t.Helper()
	w.m = palette(t, w.m, line)
	w.press(t, tea.KeyPressMsg(kEnter))
	for _, r := range then {
		w.press(t, tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
}

func (w *palCWorld) press(t *testing.T, msg tea.Msg) {
	t.Helper()
	next, cmd := w.m.Update(msg)
	w.m = next.(Model)
	w.feed(t, cmd)
}

func (w *palCWorld) feed(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case sleepAskMsg:
		next, more := w.m.Update(msg)
		w.m = next.(Model)
		w.feed(t, more)
	case sleepStartedMsg:
		next, _ := w.m.Update(msg)
		w.m = next.(Model)
	case tea.BatchMsg:
		for _, c := range msg {
			w.feed(t, c)
		}
	}
}

// Each row's exact argv (§4.1's table), shown before y and run after it.
func TestPaletteClassCArgv(t *testing.T) {
	for line, want := range map[string]string{
		"/reload coder":                  "--project agent-bus-monitor reload coder --as charlie",
		"/reload coder fresh":            "--project agent-bus-monitor reload coder --fresh --as charlie",
		"/pause 2h":                      "--project agent-bus-monitor pause --for 2h",
		"/resume":                        "--project agent-bus-monitor resume --yes",
		"/suspend":                       "--project agent-bus-monitor suspend --yes",
		"/backup":                        "--project agent-bus-monitor backup",
		"/self-repair reset coder":       "--project agent-bus-monitor self-repair reset coder --as charlie",
		"/priority slash-palette-c high": "--project agent-bus-monitor request priority slash-palette-c high --as charlie",
		"/prio herdr-7b low":             "--project agent-bus-monitor request priority herdr-7b low --as charlie",
	} {
		w := newPalCWorld(t, "charlie")
		w.run(t, line)
		if w.m.ui.sleepAsk == nil || !strings.Contains(w.m.ui.flash, "agentbus "+want) || !strings.Contains(w.m.ui.flash, "y / n") {
			t.Errorf("%s: no confirmation naming the argv: flash %q", line, w.m.ui.flash)
			continue
		}
		if len(w.runs) != 0 {
			t.Errorf("%s: ran before y: %q", line, w.runs)
		}
		w.press(t, tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
		if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != want {
			t.Errorf("%s: ran %q, want %q", line, w.runs, want)
		}
		if len(w.logs) == 1 && w.logs[0] != "/state/agentbus/agent-bus-monitor/suspend.log" {
			t.Errorf("%s: log %q", line, w.logs[0])
		}
		if w.m.ui.line != lineNone {
			t.Errorf("%s: the line is still open", line)
		}
	}
}

// Any key but y runs nothing.
func TestPaletteClassCNoRunsNothing(t *testing.T) {
	w := newPalCWorld(t, "charlie")
	w.run(t, "/backup", 'n')
	if len(w.runs) != 0 || w.m.ui.sleepAsk != nil || w.m.ui.flash != "nothing done" {
		t.Fatalf("runs %q, ask %v, flash %q", w.runs, w.m.ui.sleepAsk, w.m.ui.flash)
	}
}

// With no identity.as, the three --as rows are refused before y, the
// setting named; the others still ask.
func TestPaletteClassCNeedsIdentity(t *testing.T) {
	for _, line := range []string{"/reload coder", "/self-repair reset coder", "/priority t1 high"} {
		w := newPalCWorld(t, "")
		w.run(t, line)
		if len(w.runs) != 0 || w.m.ui.sleepAsk != nil || !strings.Contains(w.m.ui.flash, "identity.as") || !w.m.ui.flashErr || w.m.ui.edit != line {
			t.Errorf("%s: runs %q, flash %q", line, w.runs, w.m.ui.flash)
		}
	}
	w := newPalCWorld(t, "")
	w.run(t, "/backup", 'y')
	if len(w.runs) != 1 {
		t.Fatalf("backup without identity: runs %q", w.runs)
	}
}

// A known command with a missing or invalid argument runs nothing, says
// why, and keeps the line (§4.3.1 case 2).
func TestPaletteClassCChecksArguments(t *testing.T) {
	for line, want := range map[string]string{
		"/reload":               "reload needs its role",
		"/reload Coder":         "not a role name",
		"/reload coder now":     "fresh",
		"/pause":                "pause needs its duration",
		"/pause soon":           "not a duration",
		"/pause -5m":            "not a duration",
		"/self-repair coder":    "reset",
		"/self-repair reset":    "needs its agent",
		"/priority t1":          "needs its level",
		"/priority t1 urgent":   "low, normal or high",
		"/priority --as x high": "not a task",
		"/priority bad! high":   "not a task name",
		"/priority Task1 high":  "not a task name",
		"/backup now":           "takes no argument",
	} {
		w := newPalCWorld(t, "charlie")
		w.run(t, line)
		if w.m.ui.sleepAsk != nil || !strings.Contains(w.m.ui.flash, want) || w.m.ui.edit != line {
			t.Errorf("%s: ask %v, flash %q (want %q), line %q", line, w.m.ui.sleepAsk, w.m.ui.flash, want, w.m.ui.edit)
		}
	}
}

// /suspend and /resume are Z by name: the same run, argv and log.
func TestPaletteSuspendIsZ(t *testing.T) {
	viaZ := newSleepWorld(t, nil)
	viaZ.press(t, 'Z')
	viaZ.press(t, 'y')
	w := newPalCWorld(t, "charlie")
	w.run(t, "/suspend", 'y')
	if len(w.runs) != 1 || !slices.Equal(w.runs[0], viaZ.runs[0]) || w.logs[0] != viaZ.logs[0] {
		t.Fatalf("/suspend ran %q in %q, Z %q in %q", w.runs, w.logs, viaZ.runs, viaZ.logs)
	}
	if w.m.ui.sleepRun == nil || w.m.ui.sleepRun.verb != "suspend" || w.m.ui.sleepRun.project != "" {
		t.Fatalf("run %+v", w.m.ui.sleepRun)
	}
}

// One run at a time, as Z: a second command while one runs says so.
func TestPaletteClassCOneRunAtATime(t *testing.T) {
	w := newPalCWorld(t, "charlie")
	w.run(t, "/backup", 'y')
	w.run(t, "/pause 1h")
	if w.m.ui.sleepAsk != nil || !strings.Contains(w.m.ui.flash, "already running") {
		t.Fatalf("ask %v, flash %q", w.m.ui.sleepAsk, w.m.ui.flash)
	}
}

// Started from a page with Ctrl+P, a class C row closes it (§4.1).
func TestPaletteClassCClosesThePage(t *testing.T) {
	w := newPalCWorld(t, "charlie")
	w.m = keys(t, w.m, tea.Key{Code: tea.KeyF2}, kCtrlP)
	w.m = typeText(t, w.m, "backup")
	w.press(t, tea.KeyPressMsg(kEnter))
	if _, open := palTop(w.m); open || w.m.ui.sleepAsk == nil {
		t.Fatalf("page open %v, ask %v", open, w.m.ui.sleepAsk)
	}
}

// Tab completes a role, a literal word and a task.
func TestPaletteClassCCompletes(t *testing.T) {
	w := newPalCWorld(t, "charlie")
	w.m = palette(t, w.m, "/self-repair r", tea.Key{Code: tea.KeyTab})
	if w.m.ui.edit != "/self-repair reset " {
		t.Fatalf("literal: %q", w.m.ui.edit)
	}
	w.m.ui.line, w.m.ui.edit = lineNone, ""
	w.m.snap.requests = []request{{Task: "slash-palette-c"}}
	w.m = palette(t, w.m, "/priority sla", tea.Key{Code: tea.KeyTab})
	if w.m.ui.edit != "/priority slash-palette-c " {
		t.Fatalf("task: %q", w.m.ui.edit)
	}
	w.m = typeText(t, w.m, "h")
	w.m = keys(t, w.m, tea.Key{Code: tea.KeyTab})
	if w.m.ui.edit != "/priority slash-palette-c high " {
		t.Fatalf("level: %q", w.m.ui.edit)
	}
}

// The class C rows have no key and show none (§4.1); their usage names
// their arguments.
func TestPaletteClassCRows(t *testing.T) {
	for _, n := range []string{"reload", "pause", "resume", "suspend", "backup", "self-repair", "priority"} {
		c := paletteFind(n)
		if c == nil || c.Argv == nil {
			t.Fatalf("%s: no class C row", n)
		}
		if h := paletteKeyHint(*c, false); h != "" {
			t.Errorf("%s: key hint %q", n, h)
		}
	}
	if u := paletteUsage(*paletteFind("self-repair")); u != "/self-repair reset <agent>" {
		t.Errorf("usage %q", u)
	}
	if u := paletteUsage(*paletteFind("reload")); u != "/reload <role> [fresh]" {
		t.Errorf("usage %q", u)
	}
}

// A fake agentbus on PATH runs the row's argv: the bare name resolves on
// PATH when none sits beside busmon (§4.3, agentbusBeside's fallback).
func TestPaletteClassCRunsAgentbusOnPath(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + strconv.Quote(out) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "agentbus"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	w := newPalCWorld(t, "charlie")
	w.m.cfg.sleepRunner = realSleepRunner("agentbus")
	w.m.cfg.sleepLog = func(string) string { return filepath.Join(dir, "suspend.log") }
	w.run(t, "/reload coder fresh")
	next, cmd := w.m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	w.m = next.(Model)
	started, ok := cmd().(sleepStartedMsg)
	if !ok || started.err != nil {
		t.Fatalf("not started: %+v", started)
	}
	if err := <-started.done; err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if got := strings.TrimSpace(string(b)); got != "--project agent-bus-monitor reload coder --fresh --as charlie" {
		t.Fatalf("argv %q", got)
	}
}

// The registry guard (§5): every class C argv names a command of agentbus's
// commands table and uses only flags that entry or globalFlags declares.
// The table is read from cmd/agentbus/commands.go's source: busmon cannot
// import a main package.
func TestPaletteClassCArgvIsInTheAgentbusTable(t *testing.T) {
	flags := agentbusTableFlags(t)
	n := 0
	for _, c := range paletteTable {
		if c.Argv == nil {
			continue
		}
		n++
		argv := c.Argv(sampleArgs(c))
		cmd := argv[0]
		declared, ok := flags[cmd]
		if !ok {
			t.Errorf("%s: agentbus has no command %q", c.Name, cmd)
			continue
		}
		used := argv
		if c.As {
			used = append(used, "--as", "charlie")
		}
		for _, a := range used {
			if strings.HasPrefix(a, "--") && !declared[strings.TrimPrefix(a, "--")] && !flags[""][strings.TrimPrefix(a, "--")] {
				t.Errorf("%s: %s does not declare %s", c.Name, cmd, a)
			}
		}
	}
	if n != 7 {
		t.Fatalf("%d class C rows, want 7", n)
	}
}

// sampleArgs are valid arguments for a row.
func sampleArgs(c palCmd) []string {
	var out []string
	for _, a := range c.Args {
		switch {
		case a.Literal != "":
			out = append(out, a.Literal)
		case a.Name == "duration":
			out = append(out, "1h")
		case a.Name == "level":
			out = append(out, "high")
		default:
			out = append(out, "coder")
		}
	}
	return out
}

// agentbusTableFlags reads commands.go: each entry's name and its flags'
// Long names (a flag variable such as fJSON resolved); "" holds globalFlags.
func agentbusTableFlags(t *testing.T) map[string]map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("..", "agentbus", "commands.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	str := func(e ast.Expr) string {
		if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
			s, _ := strconv.Unquote(b.Value)
			return s
		}
		return ""
	}
	longOf := func(lit *ast.CompositeLit) string {
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Long" {
					return str(kv.Value)
				}
			}
		}
		return ""
	}
	vars := map[string]string{} // a flag variable's Long
	out := map[string]map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok {
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.CompositeLit); ok {
						vars[name.Name] = longOf(lit)
					}
				}
			}
		}
		return true
	})
	flagsIn := func(e ast.Expr) map[string]bool {
		set := map[string]bool{}
		if lit, ok := e.(*ast.CompositeLit); ok {
			for _, el := range lit.Elts {
				switch x := el.(type) {
				case *ast.CompositeLit:
					set[longOf(x)] = true
				case *ast.Ident:
					set[vars[x.Name]] = true
				}
			}
		}
		return set
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok && len(vs.Names) == 1 && vs.Names[0].Name == "globalFlags" {
			out[""] = flagsIn(vs.Values[0])
		}
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		var name string
		var fl ast.Expr
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok {
					switch id.Name {
					case "Name":
						name = str(kv.Value)
					case "Flags":
						fl = kv.Value
					}
				}
			}
		}
		if name != "" {
			out[name] = flagsIn(fl)
		}
		return true
	})
	if len(out) < 30 || len(out[""]) == 0 {
		t.Fatalf("read only %d commands from commands.go, the walk is broken", len(out))
	}
	return out
}
