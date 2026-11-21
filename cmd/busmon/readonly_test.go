package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// A stopped project reached by a switch is read-only (the person's word on
// the banner; master's decision after reviewer-codex): one guard refuses
// every write, whatever page asks for it; S, its y/n and navigation work.

// busWriterReads are busWriter's methods that read: every other method is
// a write and must be refused. A method added later is a write until it is
// listed here.
var busWriterReads = map[string]bool{"Asks": true, "Thread": true, "Ideas": true}

func TestReadOnlyWriterRefusesEveryWrite(t *testing.T) {
	ro := &readOnlyFlag{}
	ro.set(true)
	var inner busWriter = &trackingWriter{}
	w := guardWriter(inner, ro)
	typ := reflect.TypeOf((*busWriter)(nil)).Elem()
	v := reflect.ValueOf(w)
	writes := 0
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if busWriterReads[m.Name] {
			continue
		}
		writes++
		args := make([]reflect.Value, m.Type.NumIn())
		for j := range args {
			args[j] = reflect.Zero(m.Type.In(j))
		}
		args[0] = reflect.ValueOf(context.Background())
		out := v.MethodByName(m.Name).Call(args)
		err, _ := out[len(out)-1].Interface().(error)
		if !errors.Is(err, errReadOnly) {
			t.Errorf("%s while read-only: %v", m.Name, err)
		}
	}
	if writes < 10 || len(inner.(*trackingWriter).calls) != 0 {
		t.Fatalf("%d writes checked, the broker reached by %v", writes, inner.(*trackingWriter).calls)
	}
	ro.set(false)
	if _, err := w.TypedNotify(context.Background(), "me", "hi"); err != nil || len(inner.(*trackingWriter).calls) != 1 {
		t.Fatalf("running again: %v %v", err, inner.(*trackingWriter).calls)
	}
}

// trackingWriter records the writes that reach it.
type trackingWriter struct {
	busWriter
	calls []string
}

func (w *trackingWriter) TypedNotify(context.Context, string, string) (string, error) {
	w.calls = append(w.calls, "TypedNotify")
	return "1-0", nil
}

// The other paths: agentbus runs (all but S's own), settings, attach, the
// Projects page's delete; each says the one line and does nothing.
func TestReadOnlyRefusesTheOtherWrites(t *testing.T) {
	w := startWorld(t, nil, "down") // services_start_test.go: demo, switched from trading, dispatch down
	if !w.m.readOnly() {
		t.Fatal("a stopped project reached by a switch is not read-only")
	}
	for name, try := range map[string]func(m *Model){
		"an agentbus run (suspend)": func(m *Model) { m.startRun("suspend", []string{"--project", "demo", "suspend", "--yes"}, "/x.log") },
		"settings": func(m *Model) {
			m.settingsCmd(func() error { t.Fatal("settings written"); return nil }, "display.limit")
		},
		"attach":                     func(m *Model) { m.attachCmd("coder") },
		"the Projects page's delete": func(m *Model) { m.runDelete() },
	} {
		w.m.ui.flash = ""
		try(&w.m)
		if w.m.ui.flash != readOnlyLine {
			t.Errorf("%s: flash %q", name, w.m.ui.flash)
		}
	}
	if len(w.runs) != 0 {
		t.Fatalf("ran %v", w.runs)
	}
	w.press(t, 'S', 'y') // S's own run passes
	if len(w.runs) != 1 || !strings.HasSuffix(strings.Join(w.runs[0], " "), "service start") {
		t.Fatalf("S: %v", w.runs)
	}
}

// Running again, nothing is refused.
func TestReadOnlyLiftsWhenRunning(t *testing.T) {
	w := startWorld(t, nil, "ready")
	if w.m.readOnly() {
		t.Fatal("a running project is read-only")
	}
	w.m = run(t, w.m, w.m.startRun("suspend", []string{"--project", "demo", "suspend", "--yes"}, "/x.log"))
	if len(w.runs) != 1 {
		t.Fatalf("runs %v", w.runs)
	}
}

// The shell's own writer is the guarded one, its flag set by refresh.
func TestReadOnlyShellWriter(t *testing.T) {
	inner := &trackingWriter{}
	m := shellWith(t, 140, 40, shellConfig{w: inner, from: "trading"}, false)
	m.snap.dispatch = "down"
	m.refresh()
	if _, err := m.cfg.w.TypedNotify(context.Background(), "me", "hi"); !errors.Is(err, errReadOnly) || len(inner.calls) != 0 {
		t.Fatalf("stopped: %v %v", err, inner.calls)
	}
	m.snap.dispatch = "ready"
	m.refresh()
	if _, err := m.cfg.w.TypedNotify(context.Background(), "me", "hi"); err != nil || len(inner.calls) != 1 {
		t.Fatalf("running: %v %v", err, inner.calls)
	}
}

// The read receipt is a write: skipped, quietly, while read-only.
func TestReadOnlySkipsTheReceipt(t *testing.T) {
	m := shellWith(t, 140, 40, shellConfig{w: &fakeWriter{}, from: "trading"}, false)
	m.snap.dispatch = "down"
	m.refresh()
	m.sig.seenPending = []string{"1-0"}
	if cmd := m.writeSeen(); cmd != nil || m.ui.flash != "" {
		t.Fatalf("a receipt while read-only: cmd %v flash %q", cmd != nil, m.ui.flash)
	}
}
