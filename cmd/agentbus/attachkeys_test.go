package main

import (
	"reflect"
	"testing"
)

// ev is what the scanner says, in order.
func evs(es ...keyEvent) []keyEvent { return es }

func send(s string) keyEvent      { return keyEvent{Kind: keySend, Data: []byte(s)} }
func ctl(k keyEventKind) keyEvent { return keyEvent{Kind: k} }

// feed runs the scanner over chunks (each one read) and returns its events,
// a final Flush included.
func feed(t *testing.T, k string, held bool, chunks ...string) []keyEvent {
	t.Helper()
	s, err := newKeyScanner(k)
	if err != nil {
		t.Fatal(err)
	}
	s.held = held
	var out []keyEvent
	for _, c := range chunks {
		out = append(out, s.Feed([]byte(c))...)
	}
	return append(out, s.Flush()...)
}

// merged joins consecutive sends, so the tests read as the program sees them.
func merged(es []keyEvent) []keyEvent {
	var out []keyEvent
	for _, e := range es {
		if n := len(out); n > 0 && e.Kind == keySend && out[n-1].Kind == keySend {
			out[n-1].Data = append(append([]byte{}, out[n-1].Data...), e.Data...)
			continue
		}
		out = append(out, e)
	}
	return out
}

// The prefix (spec 4.6): <prefix> d detaches; <prefix> <prefix> sends one;
// r and x release and discard held keys, only while keys are held; any
// other key after it is dropped with a bell. F12 in both encodings, a
// sequence split across reads, a kitty release or repeat of it ignored.
func TestKeyScanner(t *testing.T) {
	f12, f12k, f12kRelease, f12kRepeat := "\x1b[24~", "\x1b[24;1:1~", "\x1b[24;1:3~", "\x1b[24;1:2~"
	for name, c := range map[string]struct {
		key    string
		held   bool
		chunks []string
		want   []keyEvent
	}{
		"plain text passes":        {"f12", false, []string{"abc\r"}, evs(send("abc\r"))},
		"other keys pass":          {"f12", false, []string{"\x1b[A\x1b[15~"}, evs(send("\x1b[A\x1b[15~"))},
		"f12 d detaches":           {"f12", false, []string{f12 + "d"}, evs(ctl(keyPending), ctl(keyDetach))},
		"kitty f12 d":              {"f12", false, []string{f12k + "d"}, evs(ctl(keyPending), ctl(keyDetach))},
		"kitty release dropped":    {"f12", false, []string{f12k, f12kRelease, "d"}, evs(ctl(keyPending), ctl(keyDetach))},
		"kitty repeat dropped":     {"f12", false, []string{f12k + f12kRepeat + "d"}, evs(ctl(keyPending), ctl(keyDetach))},
		"split across reads":       {"f12", false, []string{"ab\x1b[2", "4~", "d"}, evs(send("ab"), ctl(keyPending), ctl(keyDetach))},
		"prefix twice sends one":   {"f12", false, []string{f12 + f12 + "z"}, evs(ctl(keyPending), send(f12), ctl(keyDone), send("z"))},
		"r while held releases":    {"f12", true, []string{f12 + "r"}, evs(ctl(keyPending), ctl(keyRelease))},
		"x while held discards":    {"f12", true, []string{f12 + "x"}, evs(ctl(keyPending), ctl(keyDiscard))},
		"r with nothing held":      {"f12", false, []string{f12 + "r"}, evs(ctl(keyPending), ctl(keyBell))},
		"other key dropped":        {"f12", false, []string{f12 + "q" + "w"}, evs(ctl(keyPending), ctl(keyBell), send("w"))},
		"other sequence dropped":   {"f12", false, []string{f12 + "\x1b[A" + "w"}, evs(ctl(keyPending), ctl(keyBell), send("w"))},
		"ctrl+g as C0":             {"ctrl+g", false, []string{"a\x07d"}, evs(send("a"), ctl(keyPending), ctl(keyDetach))},
		"ctrl+g as kitty":          {"ctrl+g", false, []string{"\x1b[103;5ud"}, evs(ctl(keyPending), ctl(keyDetach))},
		"ctrl+g kitty release":     {"ctrl+g", false, []string{"\x1b[103;5u\x1b[103;5:3ud"}, evs(ctl(keyPending), ctl(keyDetach))},
		"ctrl+g twice sends C0":    {"ctrl+g", false, []string{"\x07\x07"}, evs(ctl(keyPending), send("\x07"), ctl(keyDone))},
		"ctrl+] as C0":             {"ctrl+]", false, []string{"\x1dd"}, evs(ctl(keyPending), ctl(keyDetach))},
		"f1 legacy SS3":            {"f1", false, []string{"\x1bOPd"}, evs(ctl(keyPending), ctl(keyDetach))},
		"f12 with ctrl is not f12": {"f12", false, []string{"\x1b[24;5~d"}, evs(send("\x1b[24;5~d"))},
		"kitty d after prefix":     {"f12", false, []string{f12 + "\x1b[100u"}, evs(ctl(keyPending), ctl(keyDetach))},
		"lone escape flushed":      {"f12", false, []string{"\x1b"}, evs(send("\x1b"))},
		// The outer terminal's focus reports are the holder's (holder.Focus),
		// never keys for the program (reviewer-codex).
		"focus out and in":    {"f12", false, []string{"ab\x1b[Ocd\x1b[I"}, evs(send("ab"), ctl(keyFocusOut), send("cd"), ctl(keyFocusIn))},
		"focus while pending": {"f12", false, []string{f12 + "\x1b[O" + "d"}, evs(ctl(keyPending), ctl(keyFocusOut), ctl(keyDetach))},
		// The release or repeat of the key that ended a pending prefix never
		// reaches the program (a release for a press it never saw; foureyes).
		"release of the second key dropped": {"f12", true, []string{f12k + "\x1b[114;1:1u", "\x1b[114;1:3u", "z"}, evs(ctl(keyPending), ctl(keyRelease), send("z"))},
		"repeat then release dropped":       {"f12", false, []string{f12k + "\x1b[113u\x1b[113;1:2u\x1b[113;1:3u", "z"}, evs(ctl(keyPending), ctl(keyBell), send("z"))},
		"another key's release passes":      {"f12", false, []string{f12k + "\x1b[113u\x1b[97;1:3u"}, evs(ctl(keyPending), ctl(keyBell), send("\x1b[97;1:3u"))},
		"escape then text":                  {"f12", false, []string{"\x1b", "a"}, evs(send("\x1ba"))},
	} {
		if got := merged(feed(t, c.key, c.held, c.chunks...)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %v\nwant %v", name, got, c.want)
		}
	}
	if _, err := newKeyScanner("ctrl+c"); err == nil {
		t.Error("ctrl+c taken as a prefix")
	}
}

// The program may turn focus reporting off on the outer terminal (?1004l in
// its output); attach turns it back on, or the holder would never learn of a
// blur again (foureyes).
func TestFocusReportingReasserted(t *testing.T) {
	for out, want := range map[string]bool{"hello": false, "a\x1b[?1004lb": true, "\x1b[?1004h": false, "\x1b[?1004;1049l": true, "\x1b[?10040l": false} {
		if got := turnsFocusOff([]byte(out)); got != want {
			t.Errorf("%q: %v, want %v", out, got, want)
		}
	}
}
