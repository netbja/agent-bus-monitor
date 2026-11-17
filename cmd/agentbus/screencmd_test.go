package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/termhost"
)

// fakeScreens is a holder seen through what screen may call: List and
// Screen, nothing that types.
type fakeScreens struct {
	terms  []termhost.Term
	screen map[termhost.TermID]termhost.Screen
	err    map[termhost.TermID]error
	asked  []string
}

func (f *fakeScreens) List(context.Context, string) ([]termhost.Term, error) {
	f.asked = append(f.asked, "list")
	return f.terms, nil
}
func (f *fakeScreens) Screen(_ context.Context, id termhost.TermID, n int) (termhost.Screen, error) {
	f.asked = append(f.asked, fmt.Sprintf("screen %s %d", id, n))
	if e := f.err[id]; e != nil {
		return termhost.Screen{}, e
	}
	return f.screen[id], nil
}

func screenWorld() *fakeScreens {
	return &fakeScreens{
		terms: []termhost.Term{
			{ID: "k888/t1", Project: "demo", Name: "coder", Alive: true, Build: "v888"},
			{ID: "k888/t4", Project: "demo", Name: "master", Alive: true, Build: "v888"},
			{ID: "k888/t6", Project: "demo", Name: "sentinel", Alive: true, Build: "v888"},
			{ID: "k888/t2", Project: "demo", Name: "old", Alive: false, Build: "v888"},
		},
		screen: map[termhost.TermID]termhost.Screen{"k888/t1": {Lines: []string{"Do you want to proceed?", "\x1b[31m❯ 1. Yes\x07", " 2. No", "", ""}}},
		err:    map[termhost.TermID]error{},
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// testKeyOf is the build key of a holder socket in screenWorld.
func testKeyOf(socket string) string {
	if socket == "/run/x/h-888.sock" {
		return "k888"
	}
	return ""
}

// What the holder really sets in an agent's terminal: its own terminal id
// and its socket (holder/server.go), not the qualified "<key>/<id>".
func inTerm(id string) func(string) string {
	return envOf(map[string]string{"AGENTBUS_TERM_ID": id, "AGENTBUS_HOST": "/run/x/h-888.sock"})
}

func TestScreenCommand(t *testing.T) {
	ctx := context.Background()
	t.Run("master reads an agent's screen", func(t *testing.T) {
		f := screenWorld()
		var out, errw bytes.Buffer
		if code := runScreen(ctx, testKeyOf, f, "demo", envOf(map[string]string{"AGENTBUS_TERM_ID": "k888/t4"}), []string{"coder"}, &out, &errw); code != 0 {
			t.Fatalf("code %d: %s", code, errw.String())
		}
		got := out.String()
		for _, w := range []string{"coder on k888/t1 (build v888)", "Do you want to proceed?", "❯ 1. Yes", " 2. No"} {
			if !strings.Contains(got, w) {
				t.Errorf("lacks %q:\n%s", w, got)
			}
		}
		if strings.ContainsAny(got, "\x1b\x07") || strings.HasSuffix(got, "\n\n") {
			t.Errorf("control bytes or trailing blank lines reach the reader's terminal: %q", got)
		}
		if strings.Join(f.asked, ",") != "list,screen k888/t1 40" {
			t.Errorf("asked %v", f.asked)
		}
	})
	t.Run("master in its real terminal (holder-local id and socket)", func(t *testing.T) {
		var out, errw bytes.Buffer
		if code := runScreen(ctx, testKeyOf, screenWorld(), "demo", inTerm("t4"), []string{"coder"}, &out, &errw); code != 0 {
			t.Fatalf("code %d: %s", code, errw.String())
		}
		errw.Reset()
		if code := runScreen(ctx, testKeyOf, screenWorld(), "demo", inTerm("t1"), []string{"master"}, &out, &errw); code != 1 || !strings.Contains(errw.String(), "you are coder") {
			t.Fatalf("coder in its real terminal: code %d err %q", code, errw.String())
		}
	})
	t.Run("an agent's shell without its terminal id is still that agent", func(t *testing.T) {
		var out, errw bytes.Buffer
		code := runScreen(ctx, testKeyOf, screenWorld(), "demo", envOf(map[string]string{"AGENT_BUS_AGENT": "coder"}), []string{"master"}, &out, &errw)
		if code != 1 || !strings.Contains(errw.String(), "you are coder") {
			t.Fatalf("code %d err %q", code, errw.String())
		}
		if code := runScreen(ctx, testKeyOf, screenWorld(), "demo", envOf(map[string]string{"AGENT_BUS_AGENT": "master"}), []string{"coder"}, &out, &errw); code != 0 {
			t.Fatalf("master by name: code %d", code)
		}
	})
	t.Run("sentinel may", func(t *testing.T) {
		var out, errw bytes.Buffer
		if code := runScreen(ctx, testKeyOf, screenWorld(), "demo", envOf(map[string]string{"AGENTBUS_TERM_ID": "k888/t6"}), []string{"coder"}, &out, &errw); code != 0 {
			t.Fatalf("code %d: %s", code, errw.String())
		}
	})
	t.Run("the person outside any terminal may (Q2)", func(t *testing.T) {
		var out, errw bytes.Buffer
		if code := runScreen(ctx, testKeyOf, screenWorld(), "demo", envOf(nil), []string{"coder", "--lines", "3"}, &out, &errw); code != 0 {
			t.Fatalf("code %d: %s", code, errw.String())
		}
	})
	t.Run("another agent is refused", func(t *testing.T) {
		f := screenWorld()
		var out, errw bytes.Buffer
		code := runScreen(ctx, testKeyOf, f, "demo", envOf(map[string]string{"AGENTBUS_TERM_ID": "k888/t1"}), []string{"master"}, &out, &errw)
		if code != 1 || !strings.Contains(errw.String(), "screen is master's and sentinel's; you are coder") || out.Len() != 0 {
			t.Fatalf("code %d out %q err %q", code, out.String(), errw.String())
		}
		for _, a := range f.asked {
			if strings.HasPrefix(a, "screen") {
				t.Fatalf("read a screen for a refused caller: %v", f.asked)
			}
		}
	})
	t.Run("a terminal not of this project is refused", func(t *testing.T) {
		var out, errw bytes.Buffer
		code := runScreen(ctx, testKeyOf, screenWorld(), "demo", envOf(map[string]string{"AGENTBUS_TERM_ID": "kzzz/t9"}), []string{"coder"}, &out, &errw)
		if code != 1 || !strings.Contains(errw.String(), "kzzz/t9 is not a live terminal of demo") {
			t.Fatalf("code %d err %q", code, errw.String())
		}
	})
	t.Run("no live terminal", func(t *testing.T) {
		var out, errw bytes.Buffer
		code := runScreen(ctx, testKeyOf, screenWorld(), "demo", envOf(nil), []string{"old"}, &out, &errw)
		if code != 1 || !strings.Contains(errw.String(), "no live terminal of old") {
			t.Fatalf("code %d err %q", code, errw.String())
		}
	})
	t.Run("an untrusted screen prints no lines", func(t *testing.T) {
		f := screenWorld()
		f.err["k888/t1"] = fmt.Errorf("%w (resynchronising)", termhost.ErrScreenUntrusted)
		var out, errw bytes.Buffer
		code := runScreen(ctx, testKeyOf, f, "demo", envOf(nil), []string{"coder"}, &out, &errw)
		if code != 1 || !strings.Contains(errw.String(), "screen not trusted") || strings.Contains(out.String(), "proceed") {
			t.Fatalf("code %d out %q err %q", code, out.String(), errw.String())
		}
	})
	t.Run("lines bounded", func(t *testing.T) {
		f := screenWorld()
		var out, errw bytes.Buffer
		if code := runScreen(ctx, testKeyOf, f, "demo", envOf(nil), []string{"coder", "--lines", "500"}, &out, &errw); code != 0 || f.asked[1] != "screen k888/t1 200" {
			t.Fatalf("code %d asked %v err %q", code, f.asked, errw.String())
		}
		for _, bad := range []string{"0", "x", "-3"} {
			if code := runScreen(ctx, testKeyOf, screenWorld(), "demo", envOf(nil), []string{"coder", "--lines", bad}, &out, &errw); code != 2 {
				t.Errorf("--lines %s: code %d", bad, code)
			}
		}
		if code := runScreen(ctx, testKeyOf, screenWorld(), "demo", envOf(nil), nil, &out, &errw); code != 2 {
			t.Errorf("no agent: code %d", code)
		}
	})
	t.Run("json", func(t *testing.T) {
		var out, errw bytes.Buffer
		if code := runScreen(ctx, testKeyOf, screenWorld(), "demo", envOf(nil), []string{"coder", "--json"}, &out, &errw); code != 0 {
			t.Fatalf("code %d %s", code, errw.String())
		}
		var v struct {
			Agent, Term, Build string
			Lines              []string
		}
		if err := json.Unmarshal(out.Bytes(), &v); err != nil || v.Agent != "coder" || v.Term != "k888/t1" || v.Build != "v888" || len(v.Lines) != 3 || strings.Contains(v.Lines[1], "\x1b") {
			t.Fatalf("json %+v %v: %s", v, err, out.String())
		}
	})
}

// The command on a machine with no holder of the project: it lists through
// the real holder client and says there is no terminal, without a broker.
func TestScreenCmdWithoutAHolder(t *testing.T) {
	run := t.TempDir()
	env := envOf(map[string]string{"XDG_RUNTIME_DIR": run})
	var out, errw bytes.Buffer
	if code := runScreenCmd("demo", env, []string{"coder"}, &out, &errw); code != 1 || !strings.Contains(errw.String(), "no live terminal of coder") {
		t.Fatalf("code %d err %q", code, errw.String())
	}
}
